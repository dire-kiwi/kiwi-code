import { writeSystemClipboard } from '@/lib/clipboard'
import { useEffect, useRef, useState } from 'react'
import { WTerm } from '@wterm/dom'
import { GhosttyCore } from '@wterm/ghostty'
import ghosttyWasmUrl from '@wterm/ghostty/ghostty-vt.wasm?url'
import { LoaderCircle, RefreshCw } from 'lucide-react'
import { uploadPiImage } from '@/api'
import { apiWebSocketUrl } from '@/apiUrl'
import { imageFilesFromClipboard, validateImageAdditions } from '@/lib/promptImages'
import { withMinimumContrast } from '@/lib/terminalContrast'
import { installTerminalLinks } from '@/lib/terminalLinks'
import { installTerminalMousePaste } from '@/lib/terminalMousePaste'
import {
  isTerminalEscapeKey,
  shouldBridgeTerminalControl,
  shouldForwardTerminalBlurAsEscape,
  TERMINAL_ESCAPE_SEQUENCE,
  terminalClipboardAction,
  terminalControlSequence,
} from '@/terminalKeyBridge.mjs'
import { toTerminalTheme, useTheme } from '@/theme'
import type { CodingAgent, ConnectionStatus, TerminalTool, ThemeSettings } from '@/types'
import { Button } from '@/ui/buttons'
import { Surface } from '@/ui/layout'

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
  environmentSetup?: boolean
  onEnvironmentSetupFinished?: (status: 'succeeded' | 'failed') => void
  active: boolean
  onStatusChange: (status: ConnectionStatus) => void
}

type ImagePasteNotice = {
  kind: 'uploading' | 'error'
  message: string
}

const CODING_AGENT_ENDED_CLOSE_REASON = 'Coding agent ended'
const ENVIRONMENT_SETUP_COMPLETED_CLOSE_REASON = 'Environment setup completed'
const ENVIRONMENT_SETUP_FAILED_CLOSE_REASON = 'Environment setup failed'
const RECONNECT_STABLE_AFTER_MS = 5_000
const TERMINAL_LINE_HEIGHT = 1.28
// Keep explicit agent colors (including RGB black) readable on the configured
// background without changing the terminal's ANSI palette.
const TERMINAL_MINIMUM_CONTRAST_RATIO = 4.5

// Scale the font's natural line box like xterm's lineHeight option, rounding
// down to whole pixels so rows stay crisp and the grid fits exactly.
function terminalRowHeight(element: HTMLElement, theme: ThemeSettings) {
  const probe = document.createElement('span')
  probe.style.cssText = 'position:absolute;visibility:hidden;white-space:pre;line-height:normal'
  probe.style.fontFamily = theme.fontFamily
  probe.style.fontSize = `${theme.fontSize}px`
  probe.textContent = 'W'
  element.appendChild(probe)
  const height = probe.getBoundingClientRect().height || theme.fontSize * 1.2
  probe.remove()
  return Math.max(1, Math.floor(height * TERMINAL_LINE_HEIGHT))
}

// wterm re-measures its grid whenever these metrics change the size of its
// probe cell, so typography updates resize the terminal without a manual fit.
function applyTerminalAppearance(terminal: WTerm, theme: ThemeSettings) {
  const { element } = terminal
  element.style.setProperty('--term-font-family', theme.fontFamily)
  element.style.setProperty('--term-font-size', `${theme.fontSize}px`)
  element.style.setProperty('--term-row-height', `${terminalRowHeight(element, theme)}px`)
  terminal.setThemeColors(toTerminalTheme(theme))
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
  environmentSetup = false,
  onEnvironmentSetupFinished,
  active,
  onStatusChange,
}: TerminalSessionProps) {
  const { theme } = useTheme()
  const hostRef = useRef<HTMLDivElement>(null)
  const terminalRef = useRef<WTerm | null>(null)
  const themeRef = useRef(theme)
  const activeRef = useRef(active)
  const threadTitleRef = useRef(threadTitle)
  const onStatusChangeRef = useRef(onStatusChange)
  const initialModelRef = useRef(initialModel ?? '')
  const initialThinkingLevelRef = useRef(initialThinkingLevel ?? '')
  const initialPromptRef = useRef(initialPrompt ?? '')
  const initialPromptSentRef = useRef(false)
  const onInitialPromptSentRef = useRef(onInitialPromptSent)
  const onEnvironmentSetupFinishedRef = useRef(onEnvironmentSetupFinished)
  const reconnectAttemptsRef = useRef(0)
  const [connectionAttempt, setConnectionAttempt] = useState({ generation: 0, restartCodingAgent: false })
  const [status, setStatus] = useState<ConnectionStatus>('connecting')
  const [imagePasteNotice, setImagePasteNotice] = useState<ImagePasteNotice | null>(null)
  const sessionLabel = terminalLabel ?? (tool === 'pi'
    ? codingAgent === 'claude'
      ? 'Claude Code'
      : codingAgent === 'claude-gpt'
        ? 'Claude Code (with gpt)'
        : codingAgent === 'codex'
          ? 'Codex CLI'
          : codingAgent === 'grok'
            ? 'Grok CLI'
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
  onEnvironmentSetupFinishedRef.current = onEnvironmentSetupFinished

  useEffect(() => {
    const host = hostRef.current
    if (!host) return

    let disposed = false
    let disposeTerminal = () => {}
    setStatus('connecting')
    setImagePasteNotice(null)
    onStatusChangeRef.current('connecting')

    void startTerminal().catch((reason) => {
      if (disposed) return
      console.error('Could not start the terminal.', reason)
      setStatus('error')
      onStatusChangeRef.current('error')
    })

    async function startTerminal() {
      // React's development-only Strict Mode probe disposes this effect before
      // the asynchronous setup finishes, so every await rechecks disposal.
      const core = withMinimumContrast(
        await GhosttyCore.load({ wasmPath: ghosttyWasmUrl }),
        TERMINAL_MINIMUM_CONTRAST_RATIO,
        toTerminalTheme(themeRef.current),
      )
      if (disposed || !host) {
        core.dispose()
        return
      }

      let socketErrored = false
      let reconnectTimer: ReturnType<typeof window.setTimeout> | undefined
      let reconnectStableTimer: ReturnType<typeof window.setTimeout> | undefined
      let pasteNoticeTimer: ReturnType<typeof window.setTimeout> | undefined
      const uploadController = new AbortController()
      const terminalHost: HTMLDivElement = host
      const element = document.createElement('div')
      element.setAttribute('aria-label', `${threadTitleRef.current} ${sessionLabel} terminal input`)
      terminalHost.appendChild(element)
      const terminal = new WTerm(element, {
        core,
        // Inactive panes keep parsing output but skip painting until shown.
        renderingPaused: !activeRef.current,
      })
      applyTerminalAppearance(terminal, themeRef.current)

      function releaseTerminal() {
        terminal.destroy()
        core.dispose()
        element.remove()
      }

      const focusBeforeInit = document.activeElement
      try {
        await terminal.init()
      } catch (reason) {
        releaseTerminal()
        throw reason
      }
      if (disposed) {
        releaseTerminal()
        return
      }
      // The pane can switch while init awaits, before the active effect can
      // reach this terminal.
      terminal.setRenderingPaused(!activeRef.current)
      // WTerm focuses its input during init. Background panes must not take
      // focus from the pane, prompt, or control the user is working in.
      if (!activeRef.current && element.contains(document.activeElement)) {
        if (focusBeforeInit instanceof HTMLElement && focusBeforeInit.isConnected) {
          focusBeforeInit.focus({ preventScroll: true })
        } else if (document.activeElement instanceof HTMLElement) {
          document.activeElement.blur()
        }
      }

      const disposeMousePaste = installTerminalMousePaste(terminalHost, () => terminal.focus())
      const disposeLinks = installTerminalLinks(element, () => terminal.bridge, (url) => {
        window.open(url, '_blank', 'noopener,noreferrer')
      })

      function handleClipboardKeyDown(event: KeyboardEvent) {
        const selection = terminal.getSelectionText()
        const action = terminalClipboardAction(event, Boolean(selection))
        if (!action) return
        // Keep clipboard shortcuts away from wterm, which would otherwise send
        // Ctrl-X or an encoded Shift+Insert to the application.
        event.stopPropagation()
        // Omarchy sends Ctrl-C/X/V to browser windows. Let the browser's native
        // paste reach the terminal input (and the Pi image handler) without ^V.
        if (action === 'paste' || selection === null) return
        event.preventDefault()
        void writeSystemClipboard(selection).then(() => {
          // Terminal output is immutable; cut copies and dismisses selection.
          if (!disposed && action === 'cut' && terminal.getSelectionText() === selection) {
            terminal.clearSelection()
          }
        }).catch((reason) => {
          console.warn('Could not copy the terminal selection to the system clipboard.', reason)
        })
      }
      // Capture on the wterm element so this runs before its input's handler.
      element.addEventListener('keydown', handleClipboardKeyDown, true)

      // tmux forwards completed copy-mode selections with OSC 52. wterm leaves
      // clipboard access to its embedder, so bridge those writes to the browser.
      terminal.onClipboardWrite = (text) => {
        void writeSystemClipboard(text).catch((reason) => {
          console.warn('Could not copy the tmux selection to the system clipboard.', reason)
        })
      }

      terminalRef.current = terminal

      const params = new URLSearchParams({
        cols: String(terminal.cols),
        rows: String(terminal.rows),
        protocol: '2',
      })
      if (import.meta.env.DEV) params.set('diagnostics', '1')
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
          if (environmentSetup) params.set('environmentSetup', '1')
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
      const socketStartedAt = performance.now()
      const socket = new WebSocket(socketUrl)
      socket.binaryType = 'arraybuffer'
      const inputEncoder = new TextEncoder()
      let terminalReady = false
      let firstOutputReceived = false
      let pendingAcknowledgementBytes = 0
      let acknowledgementTimer: ReturnType<typeof window.setTimeout> | undefined
      let escapeHandledOnKeyDown = false
      let lastEscapeKeyboardEventAt = Number.NEGATIVE_INFINITY
      let lastPointerDownAt = Number.NEGATIVE_INFINITY
      let terminalBlurFrame = 0

      function terminalHasKeyboardFocus(event: KeyboardEvent) {
        const target = event.target
        return terminalHost.contains(document.activeElement)
          || (target instanceof Node && terminalHost.contains(target))
      }

      function pageHasNeutralActiveElement() {
        const activeElement = document.activeElement
        return activeElement === document.body || activeElement === document.documentElement
      }

      function pageHasNeutralKeyboardFocus(event: KeyboardEvent) {
        const target = event.target
        const targetIsPage = target === document.body || target === document.documentElement
        return pageHasNeutralActiveElement() && targetIsPage
      }

      function terminalShouldHandle(data: string, event: KeyboardEvent) {
        return shouldBridgeTerminalControl(
          data,
          terminalHasKeyboardFocus(event),
          pageHasNeutralKeyboardFocus(event),
        )
      }

      function sendTerminalInput(data: string) {
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(inputEncoder.encode(data))
        }
      }

      function flushOutputAcknowledgement() {
        if (acknowledgementTimer !== undefined) {
          window.clearTimeout(acknowledgementTimer)
          acknowledgementTimer = undefined
        }
        if (pendingAcknowledgementBytes <= 0 || socket.readyState !== WebSocket.OPEN) return
        const bytes = pendingAcknowledgementBytes
        pendingAcknowledgementBytes = 0
        socket.send(JSON.stringify({ type: 'ack', bytes }))
      }

      function acknowledgeOutput(bytes: number) {
        if (disposed || bytes <= 0) return
        pendingAcknowledgementBytes += bytes
        if (pendingAcknowledgementBytes >= 32 * 1024) {
          flushOutputAcknowledgement()
          return
        }
        if (acknowledgementTimer === undefined) {
          acknowledgementTimer = window.setTimeout(flushOutputAcknowledgement, 2)
        }
      }

      function consumeTerminalKey(event: KeyboardEvent, data: string) {
        event.preventDefault()
        event.stopImmediatePropagation()
        sendTerminalInput(data)
      }

      function handleTerminalKeyDown(event: KeyboardEvent) {
        const data = terminalControlSequence(event)
        if (data === TERMINAL_ESCAPE_SEQUENCE) lastEscapeKeyboardEventAt = performance.now()
        if (!activeRef.current || data === null || !terminalShouldHandle(data, event)) return

        // Capture browser-reserved keys before the browser or wterm can handle
        // them, then write directly to the terminal socket. Meta+W is included
        // for macOS browsers, while Escape needs an explicit bridge in web mode.
        if (data === TERMINAL_ESCAPE_SEQUENCE) escapeHandledOnKeyDown = true
        consumeTerminalKey(event, data)
      }

      function handleTerminalKeyUp(event: KeyboardEvent) {
        if (!isTerminalEscapeKey(event)) return

        lastEscapeKeyboardEventAt = performance.now()
        const handledOnKeyDown = escapeHandledOnKeyDown
        escapeHandledOnKeyDown = false
        if (!activeRef.current || !terminalShouldHandle(TERMINAL_ESCAPE_SEQUENCE, event)) return

        // Some browser/window-manager combinations reserve Escape on keydown but
        // still expose keyup. Use it as a fallback without sending two escapes in
        // browsers where the keydown bridge already ran.
        event.preventDefault()
        event.stopImmediatePropagation()
        if (!handledOnKeyDown) sendTerminalInput(TERMINAL_ESCAPE_SEQUENCE)
      }

      function handlePagePointerDown() {
        lastPointerDownAt = performance.now()
      }

      function handleTerminalFocusOut() {
        cancelAnimationFrame(terminalBlurFrame)
        terminalBlurFrame = requestAnimationFrame(() => {
          if (disposed) return

          const now = performance.now()
          if (!shouldForwardTerminalBlurAsEscape({
            active: activeRef.current,
            isPiTerminal: tool === 'pi',
            pageStillFocused: document.hasFocus(),
            pageHasNeutralFocus: pageHasNeutralActiveElement(),
            recentPointerDown: now - lastPointerDownAt < 250,
            recentEscapeEvent: now - lastEscapeKeyboardEventAt < 100,
          })) return

          // Full Chrome can consume the first Escape before dispatching any
          // keyboard event, leaving the terminal's hidden textarea focused on <body>.
          // A keyboard-caused blur with no pointer or window focus change is the
          // only observable signal, so forward that swallowed Escape once.
          escapeHandledOnKeyDown = true
          lastEscapeKeyboardEventAt = now
          terminal.focus()
          sendTerminalInput(TERMINAL_ESCAPE_SEQUENCE)
        })
      }

      window.addEventListener('keydown', handleTerminalKeyDown, true)
      window.addEventListener('keyup', handleTerminalKeyUp, true)
      window.addEventListener('pointerdown', handlePagePointerDown, true)
      terminalHost.addEventListener('focusout', handleTerminalFocusOut, true)

      function updateStatus(next: ConnectionStatus) {
        if (disposed) return
        setStatus(next)
        onStatusChangeRef.current(next)
      }

      function markTerminalReady(source: 'control' | 'output') {
        if (terminalReady || disposed) return
        terminalReady = true
        updateStatus('open')
        if (import.meta.env.DEV) {
          console.debug('Terminal became ready.', {
            projectId,
            threadId,
            tool,
            source,
            milliseconds: Math.round((performance.now() - socketStartedAt) * 10) / 10,
          })
        }
        if (activeRef.current) terminal.focus()

        const prompt = initialPromptRef.current
        if (tool === 'pi' && !initialPromptSentRef.current && prompt.trim()) {
          // The server passes the prompt as a positional argument when it
          // launches Pi or Claude Code, so the first turn does not race TUI
          // startup through synthetic terminal input.
          initialPromptSentRef.current = true
          onInitialPromptSentRef.current?.()
        }
      }

      socket.addEventListener('open', () => {
        if (disposed) {
          socket.close(1000, 'Terminal pane closed')
          return
        }
        reconnectStableTimer = window.setTimeout(() => {
          if (!disposed && socket.readyState === WebSocket.OPEN) reconnectAttemptsRef.current = 0
        }, RECONNECT_STABLE_AFTER_MS)
        socket.send(
          JSON.stringify({ type: 'resize', cols: terminal.cols, rows: terminal.rows }),
        )
      })

      socket.addEventListener('message', (event) => {
        if (disposed) return
        if (typeof event.data === 'string') {
          try {
            const control = JSON.parse(event.data) as { type?: unknown; protocol?: unknown; data?: unknown }
            if (control.type === 'terminal_ready' && control.protocol === 2) {
              markTerminalReady('control')
              return
            }
            if (control.type === 'terminal_output' && typeof control.data === 'string') {
              markTerminalReady('control')
              terminal.write(control.data)
              return
            }
          } catch {
            // Protocol v1 servers can still send terminal output as text.
          }
          markTerminalReady('output')
          terminal.write(event.data)
          return
        }

        const output = new Uint8Array(event.data as ArrayBuffer)
        markTerminalReady('output')
        if (!firstOutputReceived) {
          firstOutputReceived = true
          if (import.meta.env.DEV) {
            console.debug('Terminal received first output.', {
              projectId,
              threadId,
              tool,
              milliseconds: Math.round((performance.now() - socketStartedAt) * 10) / 10,
            })
          }
        }
        // wterm parses synchronously and coalesces painting into the next
        // frame, so output is consumed as soon as write returns.
        terminal.write(output)
        acknowledgeOutput(output.byteLength)
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
        const environmentSetupCompleted = environmentSetup
          && event.code === 1000
          && event.reason === ENVIRONMENT_SETUP_COMPLETED_CLOSE_REASON
        const environmentSetupFailed = environmentSetup
          && event.code === 1000
          && event.reason === ENVIRONMENT_SETUP_FAILED_CLOSE_REASON
        const shouldReconnect = environmentSetup
          ? !environmentSetupCompleted && !environmentSetupFailed && event.code !== 1000
          : tool === 'pi'
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

        updateStatus(environmentSetupFailed || socketErrored
          ? 'error'
          : codingAgentExited ? 'closed' : shouldReconnect ? 'connecting' : 'closed')
        if (environmentSetupCompleted) onEnvironmentSetupFinishedRef.current?.('succeeded')
        if (environmentSetupFailed) onEnvironmentSetupFinishedRef.current?.('failed')
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

      function pasteTerminalText(text: string) {
        // Match wterm's own paste handling: honor bracketed paste, and strip ESC
        // so pasted text cannot end the bracket early.
        const safe = text.replace(/\x1b/g, '')
        sendTerminalInput(terminal.bridge?.bracketedPaste() ? `\x1b[200~${safe}\x1b[201~` : safe)
      }

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
          pasteTerminalText(uploads.map((upload) => upload.path).join(' '))
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
      terminalHost.addEventListener('paste', handlePaste, true)

      terminal.onData = sendTerminalInput
      // X10 mouse reports can contain bytes that are not valid UTF-8 text.
      terminal.onBinary = (data) => {
        if (socket.readyState === WebSocket.OPEN) socket.send(Uint8Array.from(data))
      }
      terminal.onResize = (cols, rows) => {
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: 'resize', cols, rows }))
        }
      }

      // Web fonts change the natural line box measured for the row height.
      void document.fonts.ready.then(() => {
        if (!disposed) applyTerminalAppearance(terminal, themeRef.current)
      })

      disposeTerminal = () => {
        disposed = true
        cancelAnimationFrame(terminalBlurFrame)
        if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
        if (reconnectStableTimer !== undefined) window.clearTimeout(reconnectStableTimer)
        if (pasteNoticeTimer !== undefined) window.clearTimeout(pasteNoticeTimer)
        if (acknowledgementTimer !== undefined) window.clearTimeout(acknowledgementTimer)
        uploadController.abort()
        window.removeEventListener('keydown', handleTerminalKeyDown, true)
        window.removeEventListener('keyup', handleTerminalKeyUp, true)
        window.removeEventListener('pointerdown', handlePagePointerDown, true)
        terminalHost.removeEventListener('focusout', handleTerminalFocusOut, true)
        terminalHost.removeEventListener('paste', handlePaste, true)
        element.removeEventListener('keydown', handleClipboardKeyDown, true)
        disposeMousePaste()
        disposeLinks()
        if (socket.readyState < WebSocket.CLOSING) socket.close(1000, 'Terminal pane closed')
        terminalRef.current = null
        releaseTerminal()
      }
    }

    return () => {
      disposed = true
      disposeTerminal()
    }
  }, [codingAgent, connectionAttempt, environmentSetup, processId, projectId, sessionLabel, threadId, tmuxSession, tmuxWindow, tool])

  useEffect(() => {
    const terminal = terminalRef.current
    if (terminal) applyTerminalAppearance(terminal, theme)
  }, [theme])

  useEffect(() => {
    // wterm names its input from the host element's label.
    terminalRef.current?.element.setAttribute('aria-label', `${threadTitle} ${sessionLabel} terminal input`)
  }, [sessionLabel, threadTitle])

  useEffect(() => {
    terminalRef.current?.setRenderingPaused(!active)
    if (!active) return
    const frame = requestAnimationFrame(() => {
      terminalRef.current?.fit()
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

      {(status === 'error' || status === 'closed') && !environmentSetup && (
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
