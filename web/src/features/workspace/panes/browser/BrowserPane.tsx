import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from 'react'
import {
  Globe2,
  Image as ImageIcon,
  LoaderCircle,
  Monitor,
  RefreshCw,
  TriangleAlert,
} from 'lucide-react'
import { browserStreamUrl, getBrowserFrame, performBrowserAction } from '@/api'
import { downloadBrowserRecording } from '@/lib/browserRecording'
import { useDesktopSurfaceBounds } from '@/lib/useDesktopSurfaceBounds'
import type {
  BrowserActionOperation,
  BrowserActionParams,
  ConnectionStatus,
} from '@/types'
import type { BrowserRecording, BrowserStatusResult } from '@/wire/domain'
import { useSubscription } from '@/wire/react'
import { BrowserStatusTopic } from '@/wire/topics'
import { Button } from '@/ui/buttons'
import type { StatusBadgeTone } from '@/ui/feedback'
import { BrowserPlaybackOverlay } from './BrowserPlaybackOverlay'
import { BrowserRecordingsBar } from './BrowserRecordingsBar'
import { BrowserStreamCanvas } from './BrowserStreamCanvas'
import { BrowserTabStrip } from './BrowserTabStrip'
import { BrowserToolbar } from './BrowserToolbar'
import {
  connectionStatusFor,
  currentPageFor,
  errorMessage,
  framePollIntervalMs,
  navigationOperation,
  navigationURL,
  validRecordingTitle,
} from './browserHelpers'

export type BrowserPaneProps = {
  projectId: string
  threadId: string
  threadTitle: string
  active: boolean
  suppressed?: boolean
  onStatusChange?: (status: ConnectionStatus) => void
  onWorkspaceShortcut?: (index: number) => void
}


export function BrowserPane({
  projectId,
  threadId,
  threadTitle,
  active,
  suppressed = false,
  onStatusChange,
  onWorkspaceShortcut,
}: BrowserPaneProps) {
  const desktopBridge = window.kiwiCodeDesktopBrowser ?? window.direMuxDesktopBrowser
  const guestRef = useRef<HTMLDivElement>(null)
  const streamCanvasRef = useRef<HTMLCanvasElement>(null)
  const streamSocketRef = useRef<WebSocket | null>(null)
  const streamGenerationRef = useRef(0)
  const addressRef = useRef<HTMLInputElement>(null)
  const frameAbortRef = useRef<AbortController | null>(null)
  const actionAbortRef = useRef<AbortController | null>(null)
  const frameURLRef = useRef('')

  const statusSubscription = useSubscription(
    BrowserStatusTopic,
    { projectId, threadId },
    { enabled: active },
  )
  const statusIdentity = `${projectId}\u0000${threadId}`
  const [retainedStatus, setRetainedStatus] = useState<{
    identity: string
    data: BrowserStatusResult
  } | null>(null)
  const liveStatus: BrowserStatusResult | null = statusSubscription.state === 'ready'
    ? statusSubscription.data
    : null
  const status = liveStatus ?? (
    statusSubscription.state === 'loading' && retainedStatus?.identity === statusIdentity
      ? retainedStatus.data
      : null
  )
  const statusLoading = statusSubscription.state === 'loading'
  const statusError = statusSubscription.state === 'error'
    ? statusSubscription.error.message
    : ''
  const [frameURL, setFrameURL] = useState('')
  const [frameLoading, setFrameLoading] = useState(false)
  const [frameError, setFrameError] = useState('')
  const [address, setAddress] = useState('')
  const [addressDirty, setAddressDirty] = useState(false)
  const [busyOperation, setBusyOperation] = useState<BrowserActionOperation | null>(null)
  const [actionError, setActionError] = useState('')
  const [nativeViewError, setNativeViewError] = useState('')
  const [nativeRetryKey, setNativeRetryKey] = useState(0)
  const [streamConnected, setStreamConnected] = useState(false)
  const [streamFrameReady, setStreamFrameReady] = useState(false)
  const [streamController, setStreamController] = useState(false)
  const [streamError, setStreamError] = useState('')
  const [streamFailed, setStreamFailed] = useState(false)
  const [recordingClock, setRecordingClock] = useState(() => Date.now())
  const [playbackRecording, setPlaybackRecording] = useState<BrowserRecording | null>(null)
  const [playbackLoading, setPlaybackLoading] = useState(false)
  const [playbackError, setPlaybackError] = useState('')

  const pages = useMemo(() => {
    const result = [...(status?.pages ?? [])]
    if (status?.current?.id && !result.some((page) => page.id === status.current?.id)) {
      result.unshift(status.current)
    }
    return result
  }, [status])
  const currentPage = currentPageFor(status, pages)
  const currentURL = currentPage?.url?.trim() ?? ''
  const currentLoading = status?.current?.loading === true
  const activeRecording = status?.recording ?? null
  const completedRecordings = status?.recordings ?? []
  const playbackOpen = Boolean(playbackRecording)
  const recordingElapsedMs = activeRecording
    ? Math.max(0, recordingClock - Date.parse(activeRecording.startedAt))
    : 0
  const nativeAdvertised = status?.capabilities?.nativeView
  const usesNativeView = Boolean(desktopBridge) && !nativeViewError && (
    nativeAdvertised === true || (!status?.capabilities && status?.backend === 'electron')
  )
  const streamAdvertised = status?.capabilities?.interactiveStream === true
  const usesStream = !usesNativeView && streamAdvertised && !streamFailed
  const usesFramePreview = !usesNativeView && !usesStream
  const noSession = Boolean(status) && status?.running === false
  const tablessSession = status?.running === true && !currentPage
  const providerUnavailable = Boolean(status?.error)
    || Boolean(statusError)
    || (status?.reachable === false && !noSession)
  const connectionStatus = connectionStatusFor(
    status,
    statusLoading,
    providerUnavailable ? statusError || status?.error || '' : '',
  )

  useEffect(() => {
    if (!liveStatus) return
    setRetainedStatus({ identity: statusIdentity, data: liveStatus })
  }, [liveStatus, statusIdentity])

  useEffect(() => {
    if (!active) return
    onStatusChange?.(connectionStatus)
  }, [active, connectionStatus, onStatusChange])

  useEffect(() => {
    if (!activeRecording) return
    setRecordingClock(Date.now())
    const timer = window.setInterval(() => setRecordingClock(Date.now()), 1_000)
    return () => window.clearInterval(timer)
  }, [activeRecording?.id])

  useEffect(() => {
    setPlaybackRecording(null)
    setPlaybackLoading(false)
    setPlaybackError('')
  }, [projectId, threadId])

  useEffect(() => {
    if (!playbackRecording || !status?.recordings) return
    if (status.recordings.some((recording) => recording.id === playbackRecording.id)) return
    setPlaybackRecording(null)
    setPlaybackLoading(false)
    setPlaybackError('')
  }, [playbackRecording, status?.recordings])

  useEffect(() => {
    if (!currentURL || addressDirty || document.activeElement === addressRef.current) return
    setAddress(currentURL)
  }, [addressDirty, currentURL])

  const clearFrameURL = useCallback(() => {
    const previousURL = frameURLRef.current
    frameURLRef.current = ''
    setFrameURL('')
    if (previousURL) URL.revokeObjectURL(previousURL)
  }, [])

  const refreshFrame = useCallback(async (foreground = false) => {
    const controller = new AbortController()
    frameAbortRef.current?.abort()
    frameAbortRef.current = controller
    if (foreground || !frameURLRef.current) setFrameLoading(true)

    try {
      const blob = await getBrowserFrame(projectId, threadId, controller.signal)
      if (controller.signal.aborted || frameAbortRef.current !== controller) return
      setFrameError('')
      if (!blob) {
        clearFrameURL()
        return
      }

      const nextURL = URL.createObjectURL(blob)
      const previousURL = frameURLRef.current
      frameURLRef.current = nextURL
      setFrameURL(nextURL)
      if (previousURL) URL.revokeObjectURL(previousURL)
    } catch (reason) {
      if (controller.signal.aborted || frameAbortRef.current !== controller) return
      setFrameError(errorMessage(reason, 'Could not refresh the browser preview.'))
    } finally {
      if (frameAbortRef.current === controller) {
        frameAbortRef.current = null
        setFrameLoading(false)
      }
    }
  }, [clearFrameURL, projectId, threadId])

  const copyStreamSelection = useCallback(async () => {
    try {
      const response = await performBrowserAction<{ result?: unknown }>(projectId, threadId, {
        operation: 'evaluate',
        params: { expression: 'String(globalThis.getSelection?.() ?? "")' },
      })
      const selected = response.result.result
      if (typeof selected === 'string' && selected) await navigator.clipboard?.writeText(selected)
    } catch {
      // Clipboard synchronization is best effort; the page still receives the
      // copy/cut chord through CDP.
    }
  }, [projectId, threadId])

  const sendStreamInput = useCallback((message: Record<string, unknown>) => {
    const socket = streamSocketRef.current
    if (!socket || socket.readyState !== WebSocket.OPEN || !streamController) return
    socket.send(JSON.stringify({
      ...message,
      ...((message.type === 'viewport' || message.type === 'focus' || message.type === 'blur')
        ? {}
        : { generation: streamGenerationRef.current }),
    }))
  }, [streamController])

  useEffect(() => {
    if (!desktopBridge) return
    const removeShortcutListener = desktopBridge.onWorkspaceShortcut((index) => {
      if (active) onWorkspaceShortcut?.(index)
    })
    return () => {
      removeShortcutListener()
    }
  }, [active, desktopBridge, onWorkspaceShortcut])

  useEffect(() => {
    if (!active || suppressed || !usesStream || providerUnavailable || status?.running !== true || !currentPage) return
    let disposed = false
    let pendingFrame: { generation: number; width: number; height: number } | null = null
    const socket = new WebSocket(browserStreamUrl(projectId, threadId))
    streamSocketRef.current = socket
    socket.binaryType = 'arraybuffer'
    setStreamError('')
    setStreamConnected(false)
    setStreamFrameReady(false)

    socket.onopen = () => {
      if (!disposed) setStreamConnected(true)
    }
    socket.onmessage = (event) => {
      if (disposed) return
      if (typeof event.data === 'string') {
        try {
          const message = JSON.parse(event.data) as Record<string, unknown>
          if (message.type === 'control') setStreamController(message.controller === true)
          if (
            message.type === 'frame'
            && typeof message.generation === 'number'
            && typeof message.width === 'number'
            && typeof message.height === 'number'
          ) {
            pendingFrame = {
              generation: message.generation,
              width: message.width,
              height: message.height,
            }
          }
        } catch {
          // Ignore malformed private stream metadata.
        }
        return
      }
      const metadata = pendingFrame
      pendingFrame = null
      if (!metadata || !(event.data instanceof ArrayBuffer)) return
      const blob = new Blob([event.data], { type: 'image/jpeg' })
      void createImageBitmap(blob).then((bitmap) => {
        if (disposed) {
          bitmap.close()
          return
        }
        if (metadata.generation < streamGenerationRef.current) {
          bitmap.close()
          return
        }
        const canvas = streamCanvasRef.current
        const context = canvas?.getContext('2d', { alpha: false })
        if (!canvas || !context) {
          bitmap.close()
          return
        }
        canvas.width = metadata.width
        canvas.height = metadata.height
        context.drawImage(bitmap, 0, 0, metadata.width, metadata.height)
        bitmap.close()
        streamGenerationRef.current = metadata.generation
        setStreamFrameReady(true)
      }).catch(() => {
        if (!disposed) setStreamError('Could not decode the browser stream frame.')
      })
    }
    socket.onerror = () => {
      if (!disposed) setStreamError('The interactive browser stream is unavailable.')
    }
    socket.onclose = () => {
      if (disposed) return
      setStreamConnected(false)
      setStreamController(false)
      setStreamFailed(true)
    }

    const element = guestRef.current
    const sendViewport = () => {
      if (!element || socket.readyState !== WebSocket.OPEN) return
      const rect = element.getBoundingClientRect()
      const width = Math.max(200, Math.min(4096, Math.round(rect.width)))
      const height = Math.max(150, Math.min(4096, Math.round(rect.height)))
      socket.send(JSON.stringify({ type: 'viewport', width, height }))
    }
    const observer = new ResizeObserver(sendViewport)
    if (element) observer.observe(element)
    socket.addEventListener('open', sendViewport)

    return () => {
      disposed = true
      observer.disconnect()
      socket.close()
      if (streamSocketRef.current === socket) streamSocketRef.current = null
      streamGenerationRef.current = 0
      setStreamConnected(false)
      setStreamController(false)
    }
  }, [active, currentPage?.id, projectId, providerUnavailable, status?.running, streamAdvertised, suppressed, threadId, usesStream])

  useEffect(() => {
    if (!active || suppressed || !usesFramePreview || providerUnavailable || status?.running === false || !currentPage) return
    let disposed = false
    let timer = 0
    async function poll() {
      await refreshFrame()
      if (!disposed) timer = window.setTimeout(() => void poll(), framePollIntervalMs)
    }
    void poll()
    return () => {
      disposed = true
      window.clearTimeout(timer)
      frameAbortRef.current?.abort()
      frameAbortRef.current = null
    }
  }, [active, currentPage, providerUnavailable, refreshFrame, status?.running, suppressed, usesFramePreview])

  useEffect(() => {
    if (!usesFramePreview || providerUnavailable || status?.running === false || tablessSession) clearFrameURL()
  }, [clearFrameURL, providerUnavailable, status?.running, tablessSession, usesFramePreview])

  useEffect(() => {
    setStreamFailed(false)
    setStreamError('')
  }, [projectId, status?.backend, threadId])

  useEffect(() => () => {
    frameAbortRef.current?.abort()
    actionAbortRef.current?.abort()
    frameAbortRef.current = null
    actionAbortRef.current = null
    if (frameURLRef.current) URL.revokeObjectURL(frameURLRef.current)
    frameURLRef.current = ''
  }, [])

  useDesktopSurfaceBounds<unknown>({
    surfaceRef: guestRef,
    owner: usesNativeView ? desktopBridge : undefined,
    identityKey: `${projectId}\0${threadId}\0${nativeRetryKey}`,
    enabled: Boolean(
      desktopBridge
      && usesNativeView
      && active
      && !suppressed
      && !playbackOpen
      && !providerUnavailable
      && status?.running === true
    ),
    stopOnError: true,
    show: (bounds) => desktopBridge!.show({ projectId, threadId, bounds }),
    setBounds: (bounds) => desktopBridge!.setBounds({ projectId, threadId, bounds }),
    hide: () => desktopBridge!.hide({ projectId, threadId }),
    onBeforeShow: () => setNativeViewError(''),
    onError: (reason) => setNativeViewError(
      errorMessage(reason, 'The desktop browser view could not be displayed.'),
    ),
  })

  const runAction = useCallback(async (
    operation: BrowserActionOperation,
    params: BrowserActionParams = {},
  ) => {
    if (actionAbortRef.current) return
    const controller = new AbortController()
    actionAbortRef.current = controller
    setBusyOperation(operation)
    setActionError('')

    try {
      await performBrowserAction(projectId, threadId, { operation, params }, controller.signal)
      if (controller.signal.aborted) return
      if (usesFramePreview) await refreshFrame()
      if (controller.signal.aborted) return
      if (desktopBridge && operation === 'session.start') {
        setNativeViewError('')
        setNativeRetryKey((value) => value + 1)
      }
    } catch (reason) {
      if (!controller.signal.aborted) {
        setActionError(errorMessage(reason, `The browser could not ${operation}.`))
      }
    } finally {
      if (actionAbortRef.current === controller) {
        actionAbortRef.current = null
        setBusyOperation(null)
      }
    }
  }, [desktopBridge, projectId, refreshFrame, threadId, usesFramePreview])

  function handleNavigate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const url = navigationURL(address)
    if (!url || busyOperation || statusLoading || providerUnavailable) return
    setAddress(url)
    setAddressDirty(false)
    void runAction(navigationOperation(status?.running === true, Boolean(currentPage)), { url })
  }

  function startRecording() {
    const suggested = currentPage?.title?.trim() && currentPage.title !== 'about:blank'
      ? `Demonstrate ${currentPage.title.trim()}`
      : 'Demonstrate browser task'
    const response = window.prompt(
      'Name the point of this recording in 2–12 words:',
      suggested.slice(0, 80),
    )
    if (response === null) return
    const title = validRecordingTitle(response)
    if (!title) {
      setActionError('Recording titles must be 2–12 words and at most 80 characters.')
      return
    }
    void runAction('recording.start', { targetId: currentPage?.id, title })
  }

  function downloadRecording(recording: BrowserRecording) {
    downloadBrowserRecording(projectId, threadId, recording)
  }

  function playRecording(recording: BrowserRecording) {
    setPlaybackError('')
    setPlaybackLoading(true)
    setPlaybackRecording(recording)
  }

  function closePlayback() {
    setPlaybackRecording(null)
    setPlaybackLoading(false)
    setPlaybackError('')
  }

  function retryAll() {
    setActionError('')
    if (nativeViewError) {
      setNativeViewError('')
      setNativeRetryKey((value) => value + 1)
    }
    if (streamError || streamFailed) {
      setStreamError('')
      setStreamFailed(false)
    }
    statusSubscription.retry()
    if (usesFramePreview) void refreshFrame(true)
  }

  const backendLabel = status?.backend?.trim() || (desktopBridge ? 'Desktop' : 'Browser')
  const viewModeLabel = usesNativeView
    ? 'Native view'
    : usesStream
      ? streamController ? 'Interactive stream' : 'View-only stream'
      : 'Preview'
  const backendTone: StatusBadgeTone = providerUnavailable
    ? 'error'
    : connectionStatus === 'open'
      ? 'success'
      : connectionStatus === 'connecting'
        ? 'warning'
        : 'neutral'

  return (
    <section
      role="tabpanel"
      aria-label={`${threadTitle} browser workspace`}
      aria-hidden={!active}
      className={`absolute inset-0 flex flex-col bg-ghost-background transition-opacity duration-150 ${
        active ? 'visible opacity-100' : 'pointer-events-none invisible opacity-0'
      }`}
    >
      <BrowserTabStrip
        pages={pages}
        selectedTargetId={status?.currentTargetId ?? currentPage?.id}
        recordingTargetId={activeRecording?.targetId}
        busy={Boolean(busyOperation)}
        statusLoading={statusLoading}
        providerUnavailable={providerUnavailable}
        sessionRunning={status?.running === true}
        onSelectTab={(targetId) => void runAction('tabs.select', { targetId })}
        onCloseTab={(targetId) => void runAction('tabs.close', { targetId })}
        onNewTab={() => void runAction(status?.running === true ? 'tabs.new' : 'session.start')}
      />

      <BrowserToolbar
        runAction={(operation, params) => void runAction(operation, params)}
        busyOperation={busyOperation}
        currentPage={currentPage}
        currentLoading={currentLoading}
        statusLoading={statusLoading}
        frameLoading={frameLoading}
        providerUnavailable={providerUnavailable}
        noSession={noSession}
        addressRef={addressRef}
        address={address}
        onAddressChange={(value) => {
          setAddress(value)
          setAddressDirty(true)
        }}
        onAddressBlur={() => {
          if (currentURL) setAddress(currentURL)
          setAddressDirty(false)
        }}
        onSubmitAddress={handleNavigate}
        activeRecording={activeRecording}
        recordingElapsedMs={recordingElapsedMs}
        recordingSupported={status?.capabilities?.recording === true}
        onStartRecording={startRecording}
        backendTone={backendTone}
        backendLabel={backendLabel}
        viewModeLabel={viewModeLabel}
        viewModeActive={usesNativeView || streamConnected}
        onRetryAll={retryAll}
      />

      {statusError && !providerUnavailable && (
        <div
          role="status"
          className="flex shrink-0 items-center gap-2 border-b border-ghost-yellow/25 bg-ghost-yellow/[0.06] px-3 py-1.5 text-[10px] text-ghost-yellow"
        >
          <TriangleAlert size={12} className="shrink-0" />
          <span className="min-w-0 flex-1 truncate" title={statusError}>
            Browser status is temporarily stale. {statusError}
          </span>
        </div>
      )}

      {(actionError || nativeViewError) && (
        <div
          role="alert"
          className="flex shrink-0 items-center gap-2 border-b border-ghost-bright-red/25 bg-ghost-bright-red/[0.07] px-3 py-1.5 text-[10px] text-ghost-bright-red"
        >
          <TriangleAlert size={12} className="shrink-0" />
          <span className="min-w-0 flex-1 truncate" title={actionError || nativeViewError}>
            {actionError || nativeViewError}
          </span>
          <Button
            type="button"
            variant="text"
            onClick={retryAll}
            className="shrink-0 font-semibold text-ghost-bright-red hover:text-ghost-bright-white"
          >
            Retry
          </Button>
        </div>
      )}

      <BrowserRecordingsBar
        recordings={completedRecordings}
        playingRecordingId={playbackRecording?.id}
        busy={Boolean(busyOperation)}
        onPlay={playRecording}
        onDownload={downloadRecording}
        onDelete={(recording) => {
          if (!window.confirm(`Delete “${recording.title}”?`)) return
          if (playbackRecording?.id === recording.id) closePlayback()
          void runAction('recording.delete', { recordingId: recording.id })
        }}
      />

      <div
        ref={guestRef}
        id="browser-guest-rectangle"
        className="relative min-h-0 flex-1 overflow-hidden bg-ghost-black"
        aria-label={usesNativeView ? 'Native browser content' : usesStream ? 'Interactive browser stream' : 'Browser preview'}
      >
        {playbackRecording && (
          <BrowserPlaybackOverlay
            projectId={projectId}
            threadId={threadId}
            recording={playbackRecording}
            loading={playbackLoading}
            error={playbackError}
            onLoadingChange={setPlaybackLoading}
            onError={setPlaybackError}
            onDownload={downloadRecording}
            onClose={closePlayback}
          />
        )}

        {usesStream && (
          <BrowserStreamCanvas
            canvasRef={streamCanvasRef}
            controller={streamController}
            generation={streamGenerationRef.current}
            sendInput={sendStreamInput}
            onFocusAddressBar={() => {
              addressRef.current?.focus()
              addressRef.current?.select()
            }}
            onReload={() => void runAction('navigate.reload')}
            onCopySelection={() => void copyStreamSelection()}
            onWorkspaceShortcut={onWorkspaceShortcut}
          />
        )}

        {usesFramePreview && frameURL && (
          <img
            src={frameURL}
            alt={`Browser preview${currentPage?.title ? ` of ${currentPage.title}` : ''}`}
            className="h-full w-full object-contain"
          />
        )}

        {usesFramePreview && (
          <div className="pointer-events-none absolute left-3 top-3 flex items-center gap-1.5 rounded-full border border-ghost-border/75 bg-ghost-panel/90 px-2.5 py-1 text-[8px] font-semibold uppercase tracking-[0.12em] text-ghost-muted shadow-lg shadow-ghost-black/40 backdrop-blur">
            <ImageIcon size={10} className="text-ghost-green" />
            Browser preview
          </div>
        )}

        {usesNativeView && !suppressed && !providerUnavailable && !noSession && !tablessSession && (
          <div className="absolute inset-0 grid place-items-center" aria-hidden="true">
            <div className="flex items-center gap-2 text-[10px] text-ghost-faint">
              <Monitor size={14} />
              Native browser surface
            </div>
          </div>
        )}

        {usesStream && !streamFrameReady && !suppressed && !providerUnavailable && !noSession && !tablessSession && (
          <BrowserEmptyState
            icon={<LoaderCircle size={22} className="animate-spin" />}
            title="Connecting browser stream"
            description={streamError || 'Waiting for the first interactive Chrome frame.'}
          />
        )}

        {usesNativeView && suppressed && (
          <BrowserEmptyState
            icon={<Monitor size={22} />}
            title="Browser view temporarily hidden"
            description="Close the open sidebar or finder to return to the native browser surface."
          />
        )}

        {!suppressed && providerUnavailable && (
          <BrowserEmptyState
            icon={<TriangleAlert size={22} />}
            tone="error"
            title="Browser provider unavailable"
            description={statusError || status?.error || 'The configured browser backend cannot be reached.'}
            actionLabel="Retry connection"
            onAction={retryAll}
          />
        )}

        {!suppressed && !providerUnavailable && noSession && (
          <BrowserEmptyState
            icon={<Globe2 size={22} />}
            title="No browser session yet"
            description="Enter an address above or open a new tab to start this thread’s browser."
            actionLabel="Open new tab"
            onAction={() => void runAction('session.start')}
          />
        )}

        {!suppressed && !providerUnavailable && tablessSession && (
          <BrowserEmptyState
            icon={<Globe2 size={22} />}
            title="No open browser tabs"
            description="Enter an address above or open a new tab to continue this thread’s browser session."
            actionLabel="Open new tab"
            onAction={() => void runAction('tabs.new')}
          />
        )}

        {!suppressed && !providerUnavailable && !noSession && !tablessSession && usesFramePreview && !frameURL && (
          <BrowserEmptyState
            icon={frameLoading ? <LoaderCircle size={22} className="animate-spin" /> : <ImageIcon size={22} />}
            title={frameError ? 'Preview unavailable' : 'Waiting for browser preview'}
            description={frameError || 'This web browser cannot host the native view. The latest JPEG preview will appear here.'}
            actionLabel="Refresh preview"
            onAction={() => void refreshFrame(true)}
          />
        )}

        {usesFramePreview && frameURL && frameError && !providerUnavailable && (
          <div
            role="status"
            className="absolute bottom-3 left-1/2 flex max-w-[calc(100%_-_1.5rem)] -translate-x-1/2 items-center gap-2 rounded-lg border border-ghost-yellow/30 bg-ghost-panel/95 px-3 py-2 text-[9px] text-ghost-yellow shadow-xl shadow-ghost-black/50"
          >
            <TriangleAlert size={11} className="shrink-0" />
            <span className="truncate">Showing the last preview. {frameError}</span>
          </div>
        )}

        <p className="sr-only" aria-live="polite">
          {providerUnavailable
            ? 'Browser provider unavailable.'
            : noSession
              ? 'No browser session.'
              : currentLoading
                ? 'Browser page loading.'
                : 'Browser ready.'}
        </p>
      </div>
    </section>
  )
}

type BrowserEmptyStateProps = {
  icon: React.ReactNode
  title: string
  description: string
  tone?: 'neutral' | 'error'
  actionLabel?: string
  onAction?: () => void
}

function BrowserEmptyState({
  icon,
  title,
  description,
  tone = 'neutral',
  actionLabel,
  onAction,
}: BrowserEmptyStateProps) {
  return (
    <div className="absolute inset-0 grid place-items-center bg-ghost-black/88 px-6 text-center backdrop-blur-[2px]">
      <div className="max-w-md">
        <span className={`mx-auto grid size-12 place-items-center rounded-xl border bg-ghost-panel ${
          tone === 'error'
            ? 'border-ghost-bright-red/35 text-ghost-bright-red'
            : 'border-ghost-border/80 text-ghost-green'
        }`}>
          {icon}
        </span>
        <p className="mt-4 text-sm font-medium text-ghost-bright-white">{title}</p>
        <p className="mt-1.5 text-[11px] leading-5 text-ghost-muted">{description}</p>
        {actionLabel && onAction && (
          <Button
            type="button"
            variant="bordered"
            onClick={onAction}
            className="mx-auto mt-4 flex h-8 items-center gap-2 rounded-lg px-3 text-[10px]"
          >
            <RefreshCw size={11} />
            {actionLabel}
          </Button>
        )}
      </div>
    </div>
  )
}
