import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from 'react'
import {
  ArrowLeft,
  ArrowRight,
  Circle,
  Download,
  Globe2,
  Image as ImageIcon,
  LoaderCircle,
  Monitor,
  Play,
  Plus,
  RefreshCw,
  RotateCw,
  Square,
  Trash2,
  TriangleAlert,
  X,
} from 'lucide-react'
import {
  browserRecordingDownloadUrl,
  browserRecordingPlaybackUrl,
  browserStreamUrl,
  getBrowserFrame,
  getBrowserStatus,
  performBrowserAction,
} from '../../api'
import { isDefaultBackendActive } from '../../backends'
import type {
  BrowserActionOperation,
  BrowserActionParams,
  BrowserCurrentPage,
  BrowserPage,
  BrowserRecording,
  BrowserStatusResult,
  BrowserViewBounds,
  ConnectionStatus,
} from '../../types'
import { Button } from '../atoms/Button'
import { IconButton } from '../atoms/IconButton'
import { BaseInput } from '../atoms/Input'
import { StatusBadge, type StatusBadgeTone } from '../atoms/StatusBadge'

const statusPollIntervalMs = 5_000
const framePollIntervalMs = 5_000

export type BrowserPaneProps = {
  projectId: string
  threadId: string
  threadTitle: string
  active: boolean
  suppressed?: boolean
  onStatusChange?: (status: ConnectionStatus) => void
  onWorkspaceShortcut?: (index: number) => void
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object'
}

function optionalString(value: unknown) {
  return typeof value === 'string' ? value : undefined
}

function optionalBoolean(value: unknown) {
  return typeof value === 'boolean' ? value : undefined
}

function optionalNumber(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function normalizeCapabilities(value: unknown) {
  if (!isRecord(value)) return undefined
  return {
    nativeView: optionalBoolean(value.nativeView),
    interactiveStream: optionalBoolean(value.interactiveStream),
    preview: optionalBoolean(value.preview),
    recording: optionalBoolean(value.recording),
  }
}

function normalizePage(value: unknown): BrowserPage | null {
  if (!isRecord(value) || typeof value.id !== 'string' || !value.id.trim()) return null
  return {
    id: value.id,
    title: optionalString(value.title),
    url: optionalString(value.url),
  }
}

function normalizeCurrentPage(value: unknown): BrowserCurrentPage | undefined {
  const page = normalizePage(value)
  if (!page || !isRecord(value)) return undefined
  return {
    ...page,
    canGoBack: optionalBoolean(value.canGoBack),
    canGoForward: optionalBoolean(value.canGoForward),
    loading: optionalBoolean(value.loading),
  }
}

function normalizeRecording(value: unknown): BrowserRecording | null {
  if (
    !isRecord(value)
    || typeof value.id !== 'string'
    || typeof value.targetId !== 'string'
    || typeof value.title !== 'string'
    || typeof value.startedAt !== 'string'
    || typeof value.state !== 'string'
    || !['starting', 'recording', 'finalizing', 'completed'].includes(value.state)
  ) return null
  return {
    id: value.id,
    targetId: value.targetId,
    title: value.title,
    startedAt: value.startedAt,
    state: value.state as BrowserRecording['state'],
    finishedAt: optionalString(value.finishedAt),
    durationMs: optionalNumber(value.durationMs),
    bytes: optionalNumber(value.bytes),
    mimeType: optionalString(value.mimeType),
    filename: optionalString(value.filename),
    idleTimeoutMs: optionalNumber(value.idleTimeoutMs),
    idleDeadlineAt: optionalString(value.idleDeadlineAt),
  }
}

function normalizeBrowserStatus(value: unknown): BrowserStatusResult {
  if (!isRecord(value)) return {}
  const result = isRecord(value.result) ? value.result : value
  const providerStatus = isRecord(result.status) ? result.status : {}
  const rawPages = Array.isArray(result.pages)
    ? result.pages
    : Array.isArray(result.pageList)
      ? result.pageList
      : undefined
  const pages = rawPages
    ?.map(normalizePage)
    .filter((page): page is BrowserPage => Boolean(page))
  const currentTargetId = optionalString(result.currentTargetId)
    ?? optionalString(providerStatus.currentTargetId)
  const selectedPage = pages?.find((page) => page.id === currentTargetId)
  const normalizedCurrent = normalizeCurrentPage(result.current)
  const currentDetails = isRecord(result.current)
    ? result.current
    : isRecord(result.currentPage)
      ? result.currentPage
      : {}
  const current = normalizedCurrent ?? (selectedPage ? {
    ...selectedPage,
    canGoBack: optionalBoolean(currentDetails.canGoBack),
    canGoForward: optionalBoolean(currentDetails.canGoForward),
    loading: optionalBoolean(currentDetails.loading),
  } : undefined)

  return {
    backend: optionalString(result.backend),
    presentation: optionalString(result.presentation) ?? optionalString(providerStatus.presentation),
    capabilities: normalizeCapabilities(result.capabilities) ?? normalizeCapabilities(providerStatus.capabilities),
    reachable: optionalBoolean(result.reachable) ?? optionalBoolean(providerStatus.reachable),
    running: optionalBoolean(result.running),
    pages,
    currentTargetId,
    current,
    recording: result.recording === null ? null : normalizeRecording(result.recording),
    recordings: Array.isArray(result.recordings)
      ? result.recordings.map(normalizeRecording).filter((item): item is BrowserRecording => Boolean(item))
      : undefined,
    error: optionalString(result.error),
  }
}

function currentPageFor(
  status: BrowserStatusResult | null,
  pages: BrowserPage[],
): BrowserCurrentPage | null {
  if (status?.current?.id) return status.current
  const page = pages.find((candidate) => candidate.id === status?.currentTargetId) ?? pages[0]
  return page ? { ...page } : null
}

function pageLabel(page: BrowserPage) {
  const title = page.title?.trim()
  if (title) return title
  const url = page.url?.trim()
  if (!url || url === 'about:blank') return 'New tab'
  try {
    return new URL(url).hostname || url
  } catch {
    return url
  }
}

function navigationURL(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return ''
  if (/^https?:\/\//i.test(trimmed)) return trimmed
  if (/^(localhost|127(?:\.\d{1,3}){3}|\[::1\])(?::\d+)?(?:\/|$)/i.test(trimmed)) {
    return `http://${trimmed}`
  }
  if (/^[a-z][a-z\d+.-]*:/i.test(trimmed)) return trimmed
  const looksLikeHost = /^(?:[a-z\d-]+\.)+[a-z\d-]+(?::\d+)?(?:\/|$)/i.test(trimmed)
    || /^\[[a-f\d:]+\](?::\d+)?(?:\/|$)/i.test(trimmed)
  if (/\s/.test(trimmed) || !looksLikeHost) {
    return `https://www.google.com/search?q=${encodeURIComponent(trimmed)}`
  }
  return `https://${trimmed}`
}

function connectionStatusFor(
  status: BrowserStatusResult | null,
  loading: boolean,
  error: string,
): ConnectionStatus {
  if (error || status?.error) return 'error'
  if (loading && !status) return 'connecting'
  if (status?.reachable === false && status.running !== false) return 'error'
  if (!status || status.running === false) return 'closed'
  if (
    status.reachable === true
    || status.running === true
    || Boolean(status.current)
    || Boolean(status.pages?.length)
  ) {
    return 'open'
  }
  return 'closed'
}

function browserBounds(rect: DOMRect): BrowserViewBounds | null {
  const width = Math.round(rect.width)
  const height = Math.round(rect.height)
  if (width < 1 || height < 1) return null
  return {
    x: Math.max(0, Math.round(rect.left)),
    y: Math.max(0, Math.round(rect.top)),
    width,
    height,
  }
}

function errorMessage(reason: unknown, fallback: string) {
  return reason instanceof Error && reason.message ? reason.message : fallback
}

function promiseResult(result: void | Promise<unknown>, onError: (reason: unknown) => void) {
  if (result && typeof result.then === 'function') void result.catch(onError)
}

function inputModifiers(event: { altKey: boolean; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean }) {
  return (event.altKey ? 1 : 0) | (event.ctrlKey ? 2 : 0) | (event.metaKey ? 4 : 0) | (event.shiftKey ? 8 : 0)
}

function formatRecordingDuration(milliseconds = 0) {
  const seconds = Math.max(0, Math.floor(milliseconds / 1_000))
  const minutes = Math.floor(seconds / 60)
  return `${minutes}:${String(seconds % 60).padStart(2, '0')}`
}

function formatRecordingBytes(bytes?: number) {
  if (!bytes || bytes < 1) return ''
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function validRecordingTitle(value: string) {
  const title = value.replace(/\s+/g, ' ').trim()
  const words = title.split(' ').filter(Boolean)
  return title.length >= 3 && title.length <= 80 && words.length >= 2 && words.length <= 12 ? title : ''
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
  // The native guest provider is paired with the backend that launched this
  // desktop renderer. Remote backends use their own projected browser preview.
  const desktopBridge = isDefaultBackendActive()
    ? window.kiwiCodeDesktopBrowser ?? window.direMuxDesktopBrowser
    : undefined
  const guestRef = useRef<HTMLDivElement>(null)
  const streamCanvasRef = useRef<HTMLCanvasElement>(null)
  const streamSocketRef = useRef<WebSocket | null>(null)
  const streamGenerationRef = useRef(0)
  const addressRef = useRef<HTMLInputElement>(null)
  const statusAbortRef = useRef<AbortController | null>(null)
  const frameAbortRef = useRef<AbortController | null>(null)
  const actionAbortRef = useRef<AbortController | null>(null)
  const frameURLRef = useRef('')
  const statusRef = useRef<BrowserStatusResult | null>(null)

  const [status, setStatus] = useState<BrowserStatusResult | null>(null)
  const [statusLoading, setStatusLoading] = useState(true)
  const [statusError, setStatusError] = useState('')
  const [statusFailureCount, setStatusFailureCount] = useState(0)
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

  statusRef.current = status

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
    || (Boolean(statusError) && (!status || statusFailureCount >= 3))
    || (status?.reachable === false && !noSession)
  const connectionStatus = connectionStatusFor(
    status,
    statusLoading,
    providerUnavailable ? statusError || status?.error || '' : '',
  )

  useEffect(() => {
    onStatusChange?.(connectionStatus)
  }, [connectionStatus, onStatusChange])

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

  const refreshStatus = useCallback(async (foreground = false) => {
    const controller = new AbortController()
    statusAbortRef.current?.abort()
    statusAbortRef.current = controller
    if (foreground || !statusRef.current) setStatusLoading(true)

    try {
      const nextStatus = normalizeBrowserStatus(
        await getBrowserStatus(projectId, threadId, controller.signal),
      )
      if (statusAbortRef.current !== controller) return
      setStatus(nextStatus)
      setStatusError('')
      setStatusFailureCount(0)
    } catch (reason) {
      if (controller.signal.aborted || statusAbortRef.current !== controller) return
      setStatusError(errorMessage(reason, 'Could not reach the browser provider.'))
      setStatusFailureCount((count) => count + 1)
    } finally {
      if (statusAbortRef.current === controller) {
        statusAbortRef.current = null
        setStatusLoading(false)
      }
    }
  }, [projectId, threadId])

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
    if (!active) return
    let disposed = false
    let timer = 0
    async function poll() {
      await refreshStatus()
      if (!disposed) timer = window.setTimeout(() => void poll(), statusPollIntervalMs)
    }
    void poll()
    return () => {
      disposed = true
      window.clearTimeout(timer)
      statusAbortRef.current?.abort()
      statusAbortRef.current = null
    }
  }, [active, refreshStatus])

  useEffect(() => {
    if (!desktopBridge) return
    let refreshTimer = 0
    const removeStateListener = desktopBridge.onState((nextState) => {
      if (
        !active ||
        nextState.projectId !== projectId ||
        nextState.threadId !== threadId
      ) return
      window.clearTimeout(refreshTimer)
      refreshTimer = window.setTimeout(() => void refreshStatus(), 50)
    })
    const removeShortcutListener = desktopBridge.onWorkspaceShortcut((index) => {
      if (active) onWorkspaceShortcut?.(index)
    })
    return () => {
      window.clearTimeout(refreshTimer)
      removeStateListener()
      removeShortcutListener()
    }
  }, [active, desktopBridge, onWorkspaceShortcut, projectId, refreshStatus, threadId])

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
    statusAbortRef.current?.abort()
    frameAbortRef.current?.abort()
    actionAbortRef.current?.abort()
    statusAbortRef.current = null
    frameAbortRef.current = null
    actionAbortRef.current = null
    if (frameURLRef.current) URL.revokeObjectURL(frameURLRef.current)
    frameURLRef.current = ''
  }, [])

  useLayoutEffect(() => {
    if (!desktopBridge || !usesNativeView) return
    const identity = { projectId, threadId }
    let disposed = false
    let failed = false
    let shown = false
    let animationFrame = 0

    function hideView() {
      shown = false
      try {
        promiseResult(desktopBridge!.hide(identity), () => {})
      } catch {
        // Hiding is best effort during teardown and overlay transitions.
      }
    }

    function reportFailure(reason: unknown) {
      if (disposed || failed) return
      failed = true
      hideView()
      setNativeViewError(errorMessage(reason, 'The desktop browser view could not be displayed.'))
    }

    function syncBounds() {
      animationFrame = 0
      if (disposed || failed) return
      const element = guestRef.current
      const bounds = element ? browserBounds(element.getBoundingClientRect()) : null
      if (!bounds) {
        if (shown) hideView()
        return
      }

      try {
        if (!shown) {
          shown = true
          promiseResult(desktopBridge!.show({ ...identity, bounds }), reportFailure)
        } else {
          promiseResult(desktopBridge!.setBounds({ ...identity, bounds }), reportFailure)
        }
      } catch (reason) {
        reportFailure(reason)
      }
    }

    function scheduleBoundsSync() {
      if (animationFrame) window.cancelAnimationFrame(animationFrame)
      animationFrame = window.requestAnimationFrame(syncBounds)
    }

    if (!active || suppressed || playbackOpen || providerUnavailable || status?.running !== true) {
      hideView()
      return () => {
        disposed = true
        hideView()
      }
    }

    setNativeViewError('')
    const observer = new ResizeObserver(scheduleBoundsSync)
    if (guestRef.current) observer.observe(guestRef.current)
    window.addEventListener('resize', scheduleBoundsSync)
    window.addEventListener('scroll', scheduleBoundsSync, true)
    syncBounds()

    return () => {
      disposed = true
      if (animationFrame) window.cancelAnimationFrame(animationFrame)
      observer.disconnect()
      window.removeEventListener('resize', scheduleBoundsSync)
      window.removeEventListener('scroll', scheduleBoundsSync, true)
      hideView()
    }
  }, [active, desktopBridge, nativeRetryKey, playbackOpen, projectId, providerUnavailable, status?.running, suppressed, threadId, usesNativeView])

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
      await refreshStatus()
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
  }, [desktopBridge, projectId, refreshFrame, refreshStatus, threadId, usesFramePreview])

  function handleNavigate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const url = navigationURL(address)
    if (!url || busyOperation || statusLoading || providerUnavailable) return
    setAddress(url)
    setAddressDirty(false)
    const operation: BrowserActionOperation = status?.running !== true
      ? 'session.start'
      : currentPage
        ? 'navigate.goto'
        : 'tabs.new'
    void runAction(operation, { url })
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
    const link = document.createElement('a')
    link.href = browserRecordingDownloadUrl(projectId, threadId, recording.id)
    link.download = `${recording.title}.webm`
    link.rel = 'noopener'
    link.click()
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
    void refreshStatus(true)
    if (usesFramePreview) void refreshFrame(true)
  }

  const backendLabel = status?.backend?.trim() || (desktopBridge ? 'Desktop' : 'Browser')
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
      <div className="flex h-9 shrink-0 items-center border-b border-ghost-border/65 bg-ghost-panel/80 px-2">
        <div
          className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto"
          role="toolbar"
          aria-label="Browser tabs"
        >
          {pages.map((page) => {
            const selected = page.id === (status?.currentTargetId ?? currentPage?.id)
            const recorded = page.id === activeRecording?.targetId
            const label = pageLabel(page)
            return (
              <div
                key={page.id}
                className={`group flex h-7 min-w-[8rem] max-w-[15rem] shrink-0 items-center rounded-md border ${
                  selected
                    ? 'border-ghost-border/85 bg-ghost-raised text-ghost-bright-white'
                    : 'border-transparent text-ghost-dim hover:bg-ghost-raised/55 hover:text-ghost-white'
                }`}
              >
                <Button
                  type="button"
                  aria-pressed={selected}
                  aria-controls="browser-guest-rectangle"
                  aria-label={`Select tab ${label}`}
                  disabled={Boolean(busyOperation) || providerUnavailable}
                  onClick={() => {
                    if (!selected) void runAction('tabs.select', { targetId: page.id })
                  }}
                  className="flex h-full min-w-0 flex-1 items-center gap-2 pl-2.5 text-left disabled:cursor-wait"
                  title={page.url || label}
                >
                  {recorded
                    ? <Circle size={10} fill="currentColor" className="shrink-0 animate-pulse text-ghost-bright-red" />
                    : <Globe2 size={11} className={selected ? 'shrink-0 text-ghost-green' : 'shrink-0'} />}
                  <span className="truncate text-[10px] font-medium">{label}</span>
                </Button>
                <IconButton
                  type="button"
                  size="xs"
                  variant="subtle"
                  disabled={Boolean(busyOperation) || providerUnavailable || recorded}
                  onClick={() => void runAction('tabs.close', { targetId: page.id })}
                  aria-label={recorded ? `Stop recording before closing tab ${label}` : `Close tab ${label}`}
                  title={recorded ? 'Stop recording before closing this tab' : `Close ${label}`}
                  className="mr-0.5 opacity-70 group-hover:opacity-100 focus:opacity-100 disabled:cursor-wait"
                >
                  <X size={10} />
                </IconButton>
              </div>
            )
          })}
          <IconButton
            type="button"
            size="sm"
            variant="subtle"
            shrink
            disabled={Boolean(busyOperation) || statusLoading || providerUnavailable}
            onClick={() => void runAction(status?.running === true ? 'tabs.new' : 'session.start')}
            aria-label="New browser tab"
            title="New browser tab"
          >
            <Plus size={13} />
          </IconButton>
        </div>
      </div>

      <div
        className="flex min-h-12 shrink-0 items-center gap-1.5 border-b border-ghost-border/65 bg-ghost-panel/95 px-2 py-1.5 sm:px-3"
        role="toolbar"
        aria-label="Browser navigation"
      >
        <div className="flex shrink-0 items-center gap-0.5">
          <IconButton
            type="button"
            size="md"
            variant="subtle"
            disabled={Boolean(busyOperation) || !currentPage || currentPage.canGoBack === false}
            onClick={() => void runAction('navigate.back')}
            aria-label="Go back"
            title="Back"
          >
            <ArrowLeft size={15} />
          </IconButton>
          <IconButton
            type="button"
            size="md"
            variant="subtle"
            disabled={Boolean(busyOperation) || !currentPage || currentPage.canGoForward === false}
            onClick={() => void runAction('navigate.forward')}
            aria-label="Go forward"
            title="Forward"
          >
            <ArrowRight size={15} />
          </IconButton>
          <IconButton
            type="button"
            size="md"
            variant="subtle"
            disabled={Boolean(busyOperation) || !currentPage}
            onClick={() => void runAction('navigate.reload')}
            aria-label="Reload page"
            title="Reload"
          >
            <RotateCw size={14} className={busyOperation === 'navigate.reload' ? 'animate-spin' : ''} />
          </IconButton>
        </div>

        <form onSubmit={handleNavigate} className="relative min-w-0 flex-1">
          <Globe2
            size={13}
            className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-ghost-dim"
          />
          <BaseInput
            ref={addressRef}
            type="text"
            inputMode="url"
            value={address}
            onChange={(event) => {
              setAddress(event.target.value)
              setAddressDirty(true)
            }}
            onKeyDown={(event) => {
              if (event.key !== 'Enter' || event.nativeEvent.isComposing) return
              event.preventDefault()
              event.currentTarget.form?.requestSubmit()
            }}
            onBlur={() => {
              if (currentURL) setAddress(currentURL)
              setAddressDirty(false)
            }}
            disabled={Boolean(busyOperation) || statusLoading || providerUnavailable}
            aria-label="Browser address"
            autoCapitalize="none"
            autoCorrect="off"
            autoComplete="off"
            spellCheck={false}
            placeholder="Enter a URL or search"
            className="h-8 w-full rounded-lg border border-ghost-border/80 bg-ghost-black/45 pl-8 pr-8 font-mono text-[10px] text-ghost-bright-white outline-none transition placeholder:text-ghost-faint focus:border-ghost-green/55 focus:ring-2 focus:ring-ghost-green/10 disabled:cursor-wait disabled:opacity-70"
          />
          {(busyOperation === 'navigate.goto' || busyOperation === 'session.start' || currentLoading) && (
            <LoaderCircle
              size={12}
              className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 animate-spin text-ghost-green"
            />
          )}
        </form>

        {activeRecording ? (
          <div className="flex shrink-0 items-center gap-1" aria-label="Browser recording controls">
            <StatusBadge tone="error">
              <span className="flex items-center gap-1 font-mono" title={activeRecording.title}>
                <Circle size={8} fill="currentColor" className={activeRecording.state === 'recording' ? 'animate-pulse' : ''} />
                {activeRecording.state === 'finalizing' ? 'Saving…' : formatRecordingDuration(recordingElapsedMs)}
              </span>
            </StatusBadge>
            <IconButton
              type="button"
              size="md"
              variant="danger"
              shrink
              disabled={Boolean(busyOperation) || activeRecording.state === 'finalizing'}
              onClick={() => void runAction('recording.stop', { recordingId: activeRecording.id })}
              aria-label="Stop browser recording"
              title={`Stop and save “${activeRecording.title}”`}
            >
              {busyOperation === 'recording.stop'
                ? <LoaderCircle size={13} className="animate-spin" />
                : <Square size={11} fill="currentColor" />}
            </IconButton>
          </div>
        ) : status?.capabilities?.recording === true && (
          <IconButton
            type="button"
            size="md"
            variant="subtle"
            shrink
            disabled={Boolean(busyOperation) || !currentPage || providerUnavailable}
            onClick={startRecording}
            aria-label="Record browser tab"
            title="Record this tab"
            className="text-ghost-bright-red"
          >
            {busyOperation === 'recording.start'
              ? <LoaderCircle size={13} className="animate-spin" />
              : <Circle size={12} fill="currentColor" />}
          </IconButton>
        )}

        <div className="hidden shrink-0 items-center gap-1 lg:flex" aria-label="Browser backend status">
          <StatusBadge tone={backendTone}>{backendLabel}</StatusBadge>
          <StatusBadge tone={usesNativeView || streamConnected ? 'info' : 'neutral'}>
            {usesNativeView ? 'Native view' : usesStream ? (streamController ? 'Interactive stream' : 'View-only stream') : 'Preview'}
          </StatusBadge>
        </div>
        <IconButton
          type="button"
          size="md"
          variant="subtle"
          shrink
          onClick={retryAll}
          disabled={statusLoading || frameLoading}
          aria-label="Refresh browser status and preview"
          title="Refresh browser status"
        >
          <RefreshCw size={13} className={statusLoading || frameLoading ? 'animate-spin' : ''} />
        </IconButton>
        <IconButton
          type="button"
          size="md"
          variant="danger"
          shrink
          disabled={Boolean(busyOperation) || noSession || providerUnavailable}
          onClick={() => {
            if (window.confirm('Close this thread’s browser session?')) void runAction('session.stop')
          }}
          aria-label="Close browser session"
          title="Close browser session"
        >
          <X size={14} />
        </IconButton>
      </div>

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

      {completedRecordings.length > 0 && (
        <div className="flex shrink-0 items-center gap-2 overflow-x-auto border-b border-ghost-border/65 bg-ghost-panel/75 px-3 py-1.5" aria-label="Completed browser recordings">
          <span className="shrink-0 text-[9px] font-semibold uppercase tracking-[0.12em] text-ghost-faint">Recordings</span>
          {completedRecordings.map((recording) => (
            <div
              key={recording.id}
              className={`flex max-w-[22rem] shrink-0 items-center gap-1 rounded-md border px-1.5 py-1 ${playbackRecording?.id === recording.id ? 'border-ghost-green/45 bg-ghost-green/10' : 'border-ghost-border/70 bg-ghost-black/25'}`}
              title={`${recording.title} · ${formatRecordingDuration(recording.durationMs)}${formatRecordingBytes(recording.bytes) ? ` · ${formatRecordingBytes(recording.bytes)}` : ''}`}
            >
              <Button
                type="button"
                variant="text"
                onClick={() => playRecording(recording)}
                className="flex min-w-0 items-center gap-1.5 px-1 text-[9px] text-ghost-white"
              >
                <Play size={9} fill="currentColor" className="shrink-0 text-ghost-green" />
                <span className="max-w-44 truncate">{recording.title}</span>
                <span className="shrink-0 font-mono text-ghost-faint">{formatRecordingDuration(recording.durationMs)}</span>
              </Button>
              <IconButton type="button" size="xs" variant="subtle" onClick={() => downloadRecording(recording)} aria-label={`Download ${recording.title}`} title="Download recording">
                <Download size={10} />
              </IconButton>
              <IconButton
                type="button"
                size="xs"
                variant="subtle"
                disabled={Boolean(busyOperation)}
                onClick={() => {
                  if (!window.confirm(`Delete “${recording.title}”?`)) return
                  if (playbackRecording?.id === recording.id) closePlayback()
                  void runAction('recording.delete', { recordingId: recording.id })
                }}
                aria-label={`Delete ${recording.title}`}
                title="Delete recording"
                className="text-ghost-bright-red"
              >
                <Trash2 size={10} />
              </IconButton>
            </div>
          ))}
        </div>
      )}

      <div
        ref={guestRef}
        id="browser-guest-rectangle"
        className="relative min-h-0 flex-1 overflow-hidden bg-ghost-black"
        aria-label={usesNativeView ? 'Native browser content' : usesStream ? 'Interactive browser stream' : 'Browser preview'}
      >
        {playbackRecording && (
          <div className="absolute inset-0 z-30 flex flex-col bg-ghost-black" aria-label={`Playback of ${playbackRecording.title}`}>
            <div className="flex h-10 shrink-0 items-center gap-2 border-b border-ghost-border/70 bg-ghost-panel px-3">
              <Play size={11} fill="currentColor" className="shrink-0 text-ghost-green" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-[10px] font-semibold text-ghost-bright-white">{playbackRecording.title}</p>
                <p className="text-[8px] text-ghost-faint">{formatRecordingDuration(playbackRecording.durationMs)}{formatRecordingBytes(playbackRecording.bytes) ? ` · ${formatRecordingBytes(playbackRecording.bytes)}` : ''}</p>
              </div>
              <IconButton type="button" size="sm" variant="subtle" onClick={() => downloadRecording(playbackRecording)} aria-label={`Download ${playbackRecording.title}`} title="Download recording">
                <Download size={12} />
              </IconButton>
              <IconButton type="button" size="sm" variant="subtle" onClick={closePlayback} aria-label="Close recording playback" title="Close playback">
                <X size={12} />
              </IconButton>
            </div>
            <div className="relative min-h-0 flex-1">
              <video
                key={playbackRecording.id}
                src={browserRecordingPlaybackUrl(projectId, threadId, playbackRecording.id)}
                controls
                autoPlay
                playsInline
                preload="metadata"
                className="h-full w-full bg-black object-contain"
                aria-label={playbackRecording.title}
                onLoadStart={() => { setPlaybackLoading(true); setPlaybackError('') }}
                onCanPlay={() => setPlaybackLoading(false)}
                onPlaying={() => setPlaybackLoading(false)}
                onWaiting={() => setPlaybackLoading(true)}
                onError={() => {
                  setPlaybackLoading(false)
                  setPlaybackError('This WebM could not be played inline. Download it to view externally.')
                }}
              />
              {playbackLoading && !playbackError && (
                <div className="pointer-events-none absolute inset-0 grid place-items-center bg-ghost-black/45" aria-label="Loading browser recording">
                  <LoaderCircle size={20} className="animate-spin text-ghost-green" />
                </div>
              )}
              {playbackError && (
                <div role="alert" className="absolute inset-x-4 bottom-4 rounded-lg border border-ghost-bright-red/30 bg-ghost-panel/95 px-3 py-2 text-center text-[10px] text-ghost-bright-red">
                  {playbackError}
                </div>
              )}
            </div>
          </div>
        )}

        {usesStream && (
          <canvas
            ref={streamCanvasRef}
            tabIndex={streamController ? 0 : -1}
            aria-label={streamController ? 'Interactive browser content' : 'Browser stream (controlled by another viewer)'}
            className={`h-full w-full object-contain outline-none ${streamController ? 'cursor-default focus:ring-2 focus:ring-inset focus:ring-ghost-green/45' : 'cursor-not-allowed'}`}
            onFocus={() => sendStreamInput({ type: 'focus' })}
            onBlur={() => sendStreamInput({ type: 'blur' })}
            onContextMenu={(event) => event.preventDefault()}
            onPointerDown={(event) => {
              if (!streamController) return
              event.currentTarget.focus()
              event.currentTarget.setPointerCapture(event.pointerId)
              const rect = event.currentTarget.getBoundingClientRect()
              const x = (event.clientX - rect.left) * event.currentTarget.width / rect.width
              const y = (event.clientY - rect.top) * event.currentTarget.height / rect.height
              const button = event.button === 2 ? 'right' : event.button === 1 ? 'middle' : 'left'
              sendStreamInput({ type: 'pointer', event: 'mousePressed', x, y, button, buttons: event.buttons, clickCount: event.detail || 1, modifiers: inputModifiers(event) })
            }}
            onPointerMove={(event) => {
              if (!streamController || !streamGenerationRef.current) return
              const rect = event.currentTarget.getBoundingClientRect()
              const x = (event.clientX - rect.left) * event.currentTarget.width / rect.width
              const y = (event.clientY - rect.top) * event.currentTarget.height / rect.height
              sendStreamInput({ type: 'pointer', event: 'mouseMoved', x, y, button: 'none', buttons: event.buttons, clickCount: 0, modifiers: inputModifiers(event) })
            }}
            onPointerUp={(event) => {
              if (!streamController) return
              const rect = event.currentTarget.getBoundingClientRect()
              const x = (event.clientX - rect.left) * event.currentTarget.width / rect.width
              const y = (event.clientY - rect.top) * event.currentTarget.height / rect.height
              const button = event.button === 2 ? 'right' : event.button === 1 ? 'middle' : 'left'
              sendStreamInput({ type: 'pointer', event: 'mouseReleased', x, y, button, buttons: event.buttons, clickCount: event.detail || 1, modifiers: inputModifiers(event) })
              if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
            }}
            onWheel={(event) => {
              if (!streamController) return
              event.preventDefault()
              const rect = event.currentTarget.getBoundingClientRect()
              const x = (event.clientX - rect.left) * event.currentTarget.width / rect.width
              const y = (event.clientY - rect.top) * event.currentTarget.height / rect.height
              sendStreamInput({ type: 'wheel', x, y, deltaX: event.deltaX, deltaY: event.deltaY, modifiers: inputModifiers(event) })
            }}
            onKeyDown={(event) => {
              const command = event.ctrlKey || event.metaKey
              if (command && event.key.toLowerCase() === 'l') {
                event.preventDefault()
                addressRef.current?.focus()
                addressRef.current?.select()
                return
              }
              if (command && event.key.toLowerCase() === 'r') {
                event.preventDefault()
                void runAction('navigate.reload')
                return
              }
              if (command && (event.key.toLowerCase() === 'c' || event.key.toLowerCase() === 'x')) {
                void copyStreamSelection()
              }
              if (command && !event.altKey && !event.shiftKey && /^[1-7]$/.test(event.key)) {
                event.preventDefault()
                onWorkspaceShortcut?.(Number(event.key))
                return
              }
              event.preventDefault()
              const text = event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey ? event.key : undefined
              sendStreamInput({ type: 'key', event: text ? 'keyDown' : 'rawKeyDown', key: event.key, code: event.code, text, modifiers: inputModifiers(event) })
            }}
            onKeyUp={(event) => {
              event.preventDefault()
              sendStreamInput({ type: 'key', event: 'keyUp', key: event.key, code: event.code, modifiers: inputModifiers(event) })
            }}
            onPaste={(event) => {
              event.preventDefault()
              sendStreamInput({ type: 'text', text: event.clipboardData.getData('text/plain') })
            }}
            onCompositionEnd={(event) => {
              if (event.data) sendStreamInput({ type: 'text', text: event.data })
            }}
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
