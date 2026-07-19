import { useEffect, useRef, useState } from 'react'
import { CanvasAddon } from '@xterm/addon-canvas'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { Terminal } from '@xterm/xterm'
import { LoaderCircle, RefreshCw } from 'lucide-react'
import { uploadPiImage } from '../../api'
import { apiWebSocketUrl } from '../../apiUrl'
import { imageFilesFromClipboard, validateImageAdditions } from '../../lib/promptImages'
import { toTerminalTheme, useTheme } from '../../theme'
import type { CodingAgent, ConnectionStatus, TerminalTool } from '../../types'
import { Button } from '../atoms/Button'
import { Surface } from '../atoms/Surface'

type TerminalSessionProps = {
  projectId: string
  threadId: string
  threadTitle: string
  tool: TerminalTool
  codingAgent?: CodingAgent
  initialModel?: string
  initialThinkingLevel?: string
  initialPrompt?: string
  onInitialPromptSent?: () => void
  processId?: string
  tmuxSession?: string
  tmuxWindow?: string
  terminalLabel?: string
  active: boolean
  onStatusChange: (status: ConnectionStatus) => void
}

type ImagePasteNotice = {
  kind: 'uploading' | 'error'
  message: string
}

const OSC_CLIPBOARD = 52
const CODING_AGENT_ENDED_CLOSE_REASON = 'Coding agent ended'
const RECONNECT_STABLE_AFTER_MS = 5_000

function decodeOscClipboard(data: string): string | null {
  const separator = data.indexOf(';')
  if (separator < 0) return null

  const encoded = data.slice(separator + 1)
  if (encoded === '?') return null

  try {
    const binary = window.atob(encoded)
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
    return new TextDecoder().decode(bytes)
  } catch {
    return null
  }
}

function copyTextWithLegacyApi(text: string) {
  const previousFocus = document.activeElement instanceof HTMLElement
    ? document.activeElement
    : null
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.readOnly = true
  textarea.style.cssText = 'position:fixed;left:-9999px;top:0;opacity:0;pointer-events:none'
  document.body.append(textarea)

  try {
    textarea.focus({ preventScroll: true })
    textarea.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    textarea.remove()
    previousFocus?.focus({ preventScroll: true })
  }
}

async function writeSystemClipboard(text: string) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return
    }
  } catch {
    // Plain HTTP origins and restrictive browser policies can reject the
    // asynchronous API. The legacy path still works during a mouse gesture.
  }

  if (!copyTextWithLegacyApi(text)) {
    throw new Error('The browser denied clipboard access.')
  }
}

export function TerminalSession({
  projectId,
  threadId,
  threadTitle,
  tool,
  codingAgent = 'pi',
  initialModel,
  initialThinkingLevel,
  initialPrompt,
  onInitialPromptSent,
  processId,
  tmuxSession,
  tmuxWindow,
  terminalLabel,
  active,
  onStatusChange,
}: TerminalSessionProps) {
  const { theme } = useTheme()
  const hostRef = useRef<HTMLDivElement>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const terminalRef = useRef<Terminal | null>(null)
  const themeRef = useRef(theme)
  const activeRef = useRef(active)
  const threadTitleRef = useRef(threadTitle)
  const onStatusChangeRef = useRef(onStatusChange)
  const initialModelRef = useRef(initialModel ?? '')
  const initialThinkingLevelRef = useRef(initialThinkingLevel ?? '')
  const initialPromptRef = useRef(initialPrompt ?? '')
  const initialPromptSentRef = useRef(false)
  const onInitialPromptSentRef = useRef(onInitialPromptSent)
  const reconnectAttemptsRef = useRef(0)
  const [connectionAttempt, setConnectionAttempt] = useState({ generation: 0, restartCodingAgent: false })
  const [status, setStatus] = useState<ConnectionStatus>('connecting')
  const [imagePasteNotice, setImagePasteNotice] = useState<ImagePasteNotice | null>(null)
  const sessionLabel = terminalLabel ?? (tool === 'pi'
    ? codingAgent === 'claude'
      ? 'Claude Code'
      : codingAgent === 'claude-gpt'
        ? 'Claude Code (with gpt)'
        : 'Pi'
    : tool === 'terminal' ? 'shell' : tool)

  activeRef.current = active
  themeRef.current = theme
  threadTitleRef.current = threadTitle
  onStatusChangeRef.current = onStatusChange
  if (initialPrompt?.trim() && !initialPromptSentRef.current) {
    initialPromptRef.current = initialPrompt
  }
  onInitialPromptSentRef.current = onInitialPromptSent

  useEffect(() => {
    const host = hostRef.current
    if (!host) return

    let disposed = false
    let disposeTerminal = () => {}
    setStatus('connecting')
    setImagePasteNotice(null)
    onStatusChangeRef.current('connecting')

    // xterm schedules work with a timer when it opens. Deferring initialization
    // lets React's development-only Strict Mode effect probe finish first, so
    // that timer never runs against an already-disposed terminal instance.
    const startFrame = requestAnimationFrame(() => {
      if (disposed) return

      let resizeFrame = 0
      let socketErrored = false
      let reconnectTimer: ReturnType<typeof window.setTimeout> | undefined
      let reconnectStableTimer: ReturnType<typeof window.setTimeout> | undefined
      let pasteNoticeTimer: ReturnType<typeof window.setTimeout> | undefined
      const uploadController = new AbortController()
      const configuredTheme = themeRef.current
      const terminal = new Terminal({
        allowTransparency: false,
        convertEol: false,
        cursorBlink: true,
        cursorStyle: 'bar',
        cursorWidth: 2,
        fontFamily: configuredTheme.fontFamily,
        fontSize: configuredTheme.fontSize,
        fontWeight: '400',
        fontWeightBold: 650,
        letterSpacing: 0,
        lineHeight: 1.28,
        macOptionIsMeta: true,
        minimumContrastRatio: 1,
        overviewRulerWidth: 0,
        rightClickSelectsWord: true,
        scrollback: 10_000,
        smoothScrollDuration: 120,
        theme: toTerminalTheme(configuredTheme),
      })
      const fit = new FitAddon()
      terminal.loadAddon(fit)
      terminal.loadAddon(new WebLinksAddon())
      terminal.open(host)
      terminal.loadAddon(new CanvasAddon())
      terminal.textarea?.setAttribute('aria-label', `${threadTitleRef.current} ${sessionLabel} terminal input`)
      const terminalHost: HTMLDivElement = host
      // tmux forwards completed copy-mode selections with OSC 52. xterm leaves
      // clipboard access to its embedder, so bridge that sequence to the browser.
      const clipboardDisposable = terminal.parser.registerOscHandler(OSC_CLIPBOARD, (data) => {
        const text = decodeOscClipboard(data)
        if (text !== null) {
          void writeSystemClipboard(text).catch((reason) => {
            console.warn('Could not copy the tmux selection to the system clipboard.', reason)
          })
        }
        return true
      })

      function handleTerminalKeyDown(event: KeyboardEvent) {
        const isEscape = event.key === 'Escape' || event.code === 'Escape'
        const isWordErase = (event.ctrlKey || event.metaKey)
          && !event.altKey
          && !event.shiftKey
          && (event.key.toLowerCase() === 'w' || event.code === 'KeyW')
        const target = event.target
        const terminalHasFocus = terminalHost.contains(document.activeElement)
          || (target instanceof Node && terminalHost.contains(target))
        if (!activeRef.current || !terminalHasFocus || (!isEscape && !isWordErase)) return

        // Capture browser-reserved keys before the browser or xterm can handle
        // them, then emit the terminal sequence exactly once. Meta+W is included
        // for macOS browsers, while Escape needs an explicit bridge in web mode.
        event.preventDefault()
        event.stopImmediatePropagation()
        terminal.input(isEscape ? '\x1b' : '\x17')
      }
      window.addEventListener('keydown', handleTerminalKeyDown, true)

      fitRef.current = fit
      terminalRef.current = terminal
      fit.fit()

      const params = new URLSearchParams({
        cols: String(terminal.cols),
        rows: String(terminal.rows),
      })
      let terminalPath: string
      if (tmuxSession && tmuxWindow) {
        params.set('session', tmuxSession)
        params.set('window', tmuxWindow)
        terminalPath = '/api/tmux/terminal'
      } else {
        params.set('tool', tool)
        if (processId) params.set('processId', processId)
        if (tool === 'pi') {
          params.set('agent', codingAgent)
          if (initialModelRef.current) params.set('model', initialModelRef.current)
          if (initialThinkingLevelRef.current) params.set('thinking', initialThinkingLevelRef.current)
          if (initialPromptRef.current.trim()) {
            params.set('prompt', initialPromptRef.current.trim())
          }
          if (connectionAttempt.restartCodingAgent) params.set('restart', '1')
        }
        terminalPath = `/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(threadId)}/terminal`
      }
      const socketUrl = apiWebSocketUrl(terminalPath)
      socketUrl.search = params.toString()
      const socket = new WebSocket(socketUrl)
      socket.binaryType = 'arraybuffer'

      function updateStatus(next: ConnectionStatus) {
        if (disposed) return
        setStatus(next)
        onStatusChangeRef.current(next)
      }

      socket.addEventListener('open', () => {
        if (disposed) {
          socket.close(1000, 'Terminal pane closed')
          return
        }
        updateStatus('open')
        reconnectStableTimer = window.setTimeout(() => {
          if (!disposed && socket.readyState === WebSocket.OPEN) reconnectAttemptsRef.current = 0
        }, RECONNECT_STABLE_AFTER_MS)
        socket.send(
          JSON.stringify({ type: 'resize', cols: terminal.cols, rows: terminal.rows }),
        )
        if (activeRef.current) terminal.focus()

        const prompt = initialPromptRef.current
        if (tool === 'pi' && !initialPromptSentRef.current && prompt.trim()) {
          // The server passes the prompt as a positional argument when it
          // launches Pi or Claude Code, so the first turn does not race TUI
          // startup through synthetic terminal input.
          initialPromptSentRef.current = true
          onInitialPromptSentRef.current?.()
        }
      })

      socket.addEventListener('message', (event) => {
        if (disposed) return
        if (typeof event.data === 'string') {
          terminal.write(event.data)
        } else {
          terminal.write(new Uint8Array(event.data as ArrayBuffer))
        }
      })

      socket.addEventListener('error', () => {
        socketErrored = true
        updateStatus('error')
      })

      socket.addEventListener('close', (event) => {
        if (reconnectStableTimer !== undefined) window.clearTimeout(reconnectStableTimer)
        const codingAgentExited = tool === 'pi'
          && event.code === 1000
          && event.reason === CODING_AGENT_ENDED_CLOSE_REASON
        const shouldReconnect = tool === 'pi'
          ? !codingAgentExited
          : tool === 'nvim' || tool === 'lazygit' || event.code !== 1000

        console.info('Terminal WebSocket closed.', {
          projectId,
          threadId,
          tool,
          codingAgent: tool === 'pi' ? codingAgent : undefined,
          code: event.code,
          reason: event.reason,
          wasClean: event.wasClean,
          reconnecting: !disposed && shouldReconnect,
        })

        updateStatus(codingAgentExited ? 'closed' : shouldReconnect ? 'connecting' : socketErrored ? 'error' : 'closed')
        if (disposed || !shouldReconnect) return

        const delay = Math.min(250 * 2 ** reconnectAttemptsRef.current, 2_000)
        reconnectAttemptsRef.current += 1
        reconnectTimer = window.setTimeout(() => {
          if (!disposed) {
            setConnectionAttempt(({ generation }) => ({
              generation: generation + 1,
              restartCodingAgent: false,
            }))
          }
        }, delay)
      })

      function showImagePasteError(message: string) {
        if (pasteNoticeTimer !== undefined) window.clearTimeout(pasteNoticeTimer)
        setImagePasteNotice({ kind: 'error', message })
        pasteNoticeTimer = window.setTimeout(() => {
          if (!disposed) setImagePasteNotice(null)
        }, 5_000)
      }

      async function pasteImages(images: File[]) {
        if (pasteNoticeTimer !== undefined) window.clearTimeout(pasteNoticeTimer)
        setImagePasteNotice({
          kind: 'uploading',
          message: images.length === 1 ? 'Adding pasted image…' : `Adding ${images.length} pasted images…`,
        })

        try {
          const uploads = await Promise.all(
            images.map((image) => uploadPiImage(projectId, image, uploadController.signal)),
          )
          if (disposed) return
          if (socket.readyState !== WebSocket.OPEN) {
            throw new Error('The Pi terminal disconnected before the image could be added.')
          }
          // Pi's native clipboard handler also inserts a temporary image path into its editor.
          terminal.paste(uploads.map((upload) => upload.path).join(' '))
          terminal.focus()
          setImagePasteNotice(null)
        } catch (reason) {
          if (disposed) return
          showImagePasteError(
            reason instanceof Error ? reason.message : 'Could not add the pasted image.',
          )
        }
      }

      function handlePaste(event: ClipboardEvent) {
        if (tool !== 'pi' || !event.clipboardData) return

        const images = imageFilesFromClipboard(event.clipboardData)
        if (images.length === 0) return

        event.preventDefault()
        event.stopPropagation()
        const validation = validateImageAdditions([], images)
        if (validation.accepted.length === 0) {
          showImagePasteError(validation.error || 'Could not add the pasted image.')
          return
        }
        void pasteImages(validation.accepted)
      }
      host.addEventListener('paste', handlePaste, true)

      const inputDisposable = terminal.onData((data) => {
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: 'input', data }))
        }
      })

      const resizeDisposable = terminal.onResize(({ cols, rows }) => {
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: 'resize', cols, rows }))
        }
      })

      const observer = new ResizeObserver(() => {
        cancelAnimationFrame(resizeFrame)
        resizeFrame = requestAnimationFrame(() => {
          if (!disposed && host.clientWidth > 0 && host.clientHeight > 0) fit.fit()
        })
      })
      observer.observe(host)

      void document.fonts.ready.then(() => {
        if (disposed) return
        fit.fit()
        terminal.refresh(0, terminal.rows - 1)
      })

      disposeTerminal = () => {
        disposed = true
        cancelAnimationFrame(resizeFrame)
        if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
        if (reconnectStableTimer !== undefined) window.clearTimeout(reconnectStableTimer)
        if (pasteNoticeTimer !== undefined) window.clearTimeout(pasteNoticeTimer)
        uploadController.abort()
        window.removeEventListener('keydown', handleTerminalKeyDown, true)
        host.removeEventListener('paste', handlePaste, true)
        observer.disconnect()
        clipboardDisposable.dispose()
        inputDisposable.dispose()
        resizeDisposable.dispose()
        if (socket.readyState < WebSocket.CLOSING) socket.close(1000, 'Terminal pane closed')
        fitRef.current = null
        terminalRef.current = null
        terminal.dispose()
      }
    })

    return () => {
      disposed = true
      cancelAnimationFrame(startFrame)
      disposeTerminal()
    }
  }, [codingAgent, connectionAttempt, processId, projectId, sessionLabel, threadId, tmuxSession, tmuxWindow, tool])

  useEffect(() => {
    const terminal = terminalRef.current
    if (!terminal) return
    terminal.options.fontFamily = theme.fontFamily
    terminal.options.fontSize = theme.fontSize
    terminal.options.theme = toTerminalTheme(theme)
    const frame = requestAnimationFrame(() => fitRef.current?.fit())
    return () => cancelAnimationFrame(frame)
  }, [theme])

  useEffect(() => {
    terminalRef.current?.textarea?.setAttribute('aria-label', `${threadTitle} ${sessionLabel} terminal input`)
  }, [sessionLabel, threadTitle])

  useEffect(() => {
    if (!active) return
    const frame = requestAnimationFrame(() => {
      fitRef.current?.fit()
      terminalRef.current?.focus()
    })
    return () => cancelAnimationFrame(frame)
  }, [active])

  return (
    <section
      role="tabpanel"
      aria-label={`${threadTitle} ${sessionLabel} session`}
      aria-hidden={!active}
      className={`absolute inset-0 transition-opacity duration-150 ${
        active ? 'visible opacity-100' : 'pointer-events-none invisible opacity-0'
      }`}
    >
      <div
        ref={hostRef}
        className={`terminal-host h-full w-full overflow-hidden bg-ghost-background ${
          status === 'error' || status === 'closed' ? 'pointer-events-none' : ''
        }`}
      />

      {imagePasteNotice && (
        <div
          role={imagePasteNotice.kind === 'error' ? 'alert' : 'status'}
          className={`pointer-events-none absolute right-4 top-4 flex max-w-[calc(100%_-_2rem)] items-center gap-2 rounded-lg border px-3 py-2 text-[10px] shadow-xl shadow-ghost-black/45 backdrop-blur sm:max-w-sm ${
            imagePasteNotice.kind === 'error'
              ? 'border-ghost-bright-red/40 bg-ghost-panel/95 text-ghost-bright-red'
              : 'border-ghost-border/80 bg-ghost-panel/95 text-ghost-muted'
          }`}
        >
          {imagePasteNotice.kind === 'uploading' && <LoaderCircle size={12} className="animate-spin" />}
          {imagePasteNotice.message}
        </div>
      )}

      {status === 'connecting' && (
        <div className="pointer-events-none absolute inset-x-0 top-5 flex justify-center">
          <div className="flex items-center gap-2 rounded-full border border-ghost-border/80 bg-ghost-panel/95 px-3 py-1.5 text-[10px] text-ghost-muted shadow-xl shadow-ghost-black/45 backdrop-blur">
            <span className="size-1.5 animate-pulse rounded-full bg-ghost-yellow" />
            Starting {sessionLabel}…
          </div>
        </div>
      )}

      {(status === 'error' || status === 'closed') && (
        <div className="absolute inset-0 z-20 grid place-items-center bg-ghost-black/85 backdrop-blur-[2px]">
          <Surface variant="raised-panel" className="px-7 py-6 text-center">
            <span
              className={`mx-auto mb-3 block size-2 rounded-full ${
                status === 'error' ? 'bg-ghost-bright-red' : 'bg-ghost-faint'
              }`}
            />
            <p className="text-sm font-medium text-ghost-bright-white">
              {status === 'error' ? 'Could not connect' : tool === 'pi' ? `${sessionLabel} exited` : 'Session ended'}
            </p>
            <p className="mt-1 text-[11px] text-ghost-muted">
              {status === 'error'
                ? 'Make sure the Go server is running.'
                : tool === 'pi'
                  ? `Start the thread’s ${sessionLabel} session again when you are ready.`
                  : 'The terminal process has exited.'}
            </p>
            <Button
              type="button"
              variant="bordered"
              onClick={() => {
                setConnectionAttempt(({ generation }) => ({
                  generation: generation + 1,
                  restartCodingAgent: tool === 'pi' && status === 'closed',
                }))
              }}
              className="mx-auto mt-4 flex h-8 items-center gap-2 rounded-lg px-3 text-[11px]"
            >
              <RefreshCw size={12} />
              {tool === 'pi' && status === 'closed' ? `Start ${sessionLabel}` : 'Reconnect session'}
            </Button>
          </Surface>
        </div>
      )}
    </section>
  )
}
