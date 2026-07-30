import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
  type KeyboardEvent,
} from 'react'
import { ArrowDown, Bot } from 'lucide-react'
import { uploadPiImage } from '@/api'
import { apiWebSocketUrl } from '@/apiUrl'
import { piThinkingLevelIds } from '@/codingAgents'
import { classNames } from '@/lib/classNames'
import { formatDuration } from '@/lib/formatDuration'
import { imageFilesFromClipboard, piNativePromptImagePolicy } from '@/lib/promptImages'
import {
  readPiNativeDraft,
  readPiNativePastes,
  writePiNativeDraft,
  writePiNativePastes,
} from '@/lib/promptDrafts'
import { useImageAttachments } from '@/lib/useImageAttachments'
import { useNativeActivityLog } from '@/lib/useNativeActivityLog'
import { useNativeAgentSocket } from '@/lib/useNativeAgentSocket'
import {
  collapsePromptPaste,
  expandPromptPastes,
  prunePromptPastes,
} from '@/prompt-pastes.mjs'
import type { AgentContextStatus, ConnectionStatus } from '@/types'
import { NativeAgentActivityMonitor, type NativeAgentDescriptor } from './NativeAgentActivityMonitor'
import { PiNativeComposer } from './PiNativeComposer'
import { PiSessionUsage } from './PiSessionUsage'
import { PiNativeTimelineEntry } from './PiNativeTimeline'
import { deriveAgentActivity } from './agentActivity'
import {
  buildComposerSuggestions,
  modelIdentifier,
  normalizePiCommands,
  normalizePiModels,
} from './piCommands'
import {
  assistantMessagePhase,
  assistantUpdatePhase,
  isPiWorkEvent,
  piRpcEventLabel,
} from './piEvents'
import { formatSessionStats, nativeContextStatus, piLatestCacheHitRate } from './piFormatting'
import { piNativeStyles } from './piNativeStyles'
import { runPiSlashCommand } from './piSlashCommands'
import {
  assistantRunDiagnostic,
  buildTimeline,
  upsertAgentMessage,
} from './piTimeline'
import type {
  ComposerSuggestion,
  PiAgentMessage,
  PiEventStamp,
  PiModel,
  PiRpcEvent,
  PiSessionStats,
  PiSlashCommand,
  PiToolState,
} from './piTypes'

type PiNativePaneProps = {
  projectId: string
  threadId: string
  threadTitle: string
  initialModel?: string
  initialThinkingLevel?: string
  initialPrompt?: string
  initialImagePaths?: string[]
  onInitialPromptSent?: () => void
  active: boolean
  onStatusChange: (status: ConnectionStatus) => void
  onContextStatusChange: (status: AgentContextStatus | null) => void
}

const PI_AGENT: NativeAgentDescriptor = {
  name: 'Pi',
  channelLabel: 'Pi RPC',
  responseSubject: 'Pi',
}

export function PiNativePane({
  projectId,
  threadId,
  threadTitle,
  initialModel,
  initialThinkingLevel,
  initialPrompt,
  initialImagePaths,
  onInitialPromptSent,
  active,
  onStatusChange,
  onContextStatusChange,
}: PiNativePaneProps) {
  const [messages, setMessages] = useState<PiAgentMessage[]>([])
  const [liveAssistant, setLiveAssistant] = useState<PiAgentMessage | null>(null)
  const [toolStates, setToolStates] = useState<Map<string, PiToolState>>(() => new Map())
  const [queuedMessages, setQueuedMessages] = useState<string[]>([])
  const [piCommands, setPiCommands] = useState<PiSlashCommand[]>([])
  const [availableModels, setAvailableModels] = useState<PiModel[]>([])
  const [sessionStats, setSessionStats] = useState<PiSessionStats>()
  const [selectedModel, setSelectedModel] = useState(initialModel ?? '')
  const [selectedThinking, setSelectedThinking] = useState(initialThinkingLevel ?? '')
  const [draft, setDraft] = useState(() => readPiNativeDraft(projectId, threadId))
  const [draftPastes, setDraftPastes] = useState(() => readPiNativePastes(projectId, threadId))
  const {
    attachments: draftImages,
    addFiles: addDraftImageFiles,
    removeAttachment: removeDraftImageAttachment,
    clearAttachments: clearDraftImages,
  } = useImageAttachments()
  const [isUploadingImages, setIsUploadingImages] = useState(false)
  const [slashMenuDismissed, setSlashMenuDismissed] = useState(false)
  const [selectedSlashIndex, setSelectedSlashIndex] = useState(0)
  const [isStreaming, setIsStreaming] = useState(false)
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>('connecting')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [showJumpToLatest, setShowJumpToLatest] = useState(false)
  const [activityExpanded, setActivityExpanded] = useState(false)
  const { activityLog, appendActivity } = useNativeActivityLog()
  const [latestRpcEvent, setLatestRpcEvent] = useState<PiEventStamp | null>(null)
  const [latestWorkEvent, setLatestWorkEvent] = useState<PiEventStamp | null>(null)
  const [runPhase, setRunPhase] = useState('Idle')
  const [runStartedAt, setRunStartedAt] = useState<number | null>(null)
  const [runEventCount, setRunEventCount] = useState(0)
  const [connectedAt, setConnectedAt] = useState<number | null>(null)
  const [lastPiResponseAt, setLastPiResponseAt] = useState<number | null>(null)
  const [lastProbeLatency, setLastProbeLatency] = useState<number | null>(null)
  const [lastProbeSentAt, setLastProbeSentAt] = useState<number | null>(null)
  const [clockNow, setClockNow] = useState(() => Date.now())
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const timelineRef = useRef<HTMLDivElement>(null)
  const activeRef = useRef(active)
  const atBottomRef = useRef(true)
  const isStreamingRef = useRef(false)
  const runPhaseRef = useRef('Idle')
  const runStartedAtRef = useRef<number | null>(null)
  const promptSentAtRef = useRef<number | null>(null)
  const probeSentAtRef = useRef<number | null>(null)
  const sessionStatsNoticePendingRef = useRef(false)
  const displayHistoryAvailableRef = useRef(false)
  const latestAssistantMessageRef = useRef<PiAgentMessage | null>(null)
  const initialModelRef = useRef(initialModel ?? '')
  const initialThinkingRef = useRef(initialThinkingLevel ?? '')
  const initialPromptRef = useRef(initialPrompt ?? '')
  const initialImagePathsRef = useRef([...(initialImagePaths ?? [])])
  const initialPromptSentRef = useRef(false)
  const promptSubmissionRef = useRef(false)
  const imageUploadControllerRef = useRef<AbortController | null>(null)
  const onInitialPromptSentRef = useRef(onInitialPromptSent)
  const onStatusChangeRef = useRef(onStatusChange)
  const onContextStatusChangeRef = useRef(onContextStatusChange)
  const selectedModelRef = useRef(initialModel ?? '')

  activeRef.current = active
  isStreamingRef.current = isStreaming
  onStatusChangeRef.current = onStatusChange
  onContextStatusChangeRef.current = onContextStatusChange
  onInitialPromptSentRef.current = onInitialPromptSent
  if (!initialPromptSentRef.current) {
    if (initialPrompt !== undefined) initialPromptRef.current = initialPrompt
    if (initialImagePaths?.length) initialImagePathsRef.current = [...initialImagePaths]
  }

  const updateConnectionStatus = useCallback((status: ConnectionStatus) => {
    setConnectionStatus(status)
    onStatusChangeRef.current(status)
  }, [])

  const updateRunPhase = useCallback((phase: string, event?: string, summary?: string, at = Date.now()) => {
    if (runPhaseRef.current === phase) return
    runPhaseRef.current = phase
    setRunPhase(phase)
    if (event && summary) appendActivity(event, summary, at)
  }, [appendActivity])

  const beginRun = useCallback((phase: string, at = Date.now()) => {
    if (!isStreamingRef.current) {
      isStreamingRef.current = true
      setIsStreaming(true)
      runStartedAtRef.current = at
      setRunStartedAt(at)
      setRunEventCount(0)
    }
    updateRunPhase(phase)
  }, [updateRunPhase])

  const finishRun = useCallback((event?: string, summary?: string, at = Date.now()) => {
    const wasStreaming = isStreamingRef.current
    isStreamingRef.current = false
    setIsStreaming(false)
    runStartedAtRef.current = null
    setRunStartedAt(null)
    promptSentAtRef.current = null
    runPhaseRef.current = 'Idle'
    setRunPhase('Idle')
    if (wasStreaming && event && summary) appendActivity(event, summary, at)
  }, [appendActivity])

  const markProbeSent = useCallback((at = Date.now()) => {
    probeSentAtRef.current = at
    setLastProbeSentAt(at)
  }, [])

  const handleEvent = useCallback((event: PiRpcEvent, socket: WebSocket) => {
    const receivedAt = Date.now()
    const rpcLabel = piRpcEventLabel(event)
    setLatestRpcEvent({ at: receivedAt, label: rpcLabel })
    if (isPiWorkEvent(event)) {
      setLatestWorkEvent({ at: receivedAt, label: rpcLabel })
      if (event.type !== 'agent_start') setRunEventCount((current) => current + 1)
    }

    switch (event.type) {
      case 'pi_native_ready': {
        updateConnectionStatus('open')
        setConnectedAt(receivedAt)
        setError('')
        appendActivity('pi_native_ready', 'Connected to the native Pi process.', receivedAt)
        markProbeSent(receivedAt)
        socket.send(JSON.stringify({ type: 'refresh' }))
        socket.send(JSON.stringify({ type: 'get_commands' }))
        socket.send(JSON.stringify({ type: 'get_available_models' }))
        const prompt = initialPromptRef.current.trim()
        const imagePaths = initialImagePathsRef.current
        if ((prompt || imagePaths.length > 0) && !initialPromptSentRef.current) {
          promptSentAtRef.current = receivedAt
          beginRun('Sending prompt', receivedAt)
          appendActivity(
            'prompt',
            imagePaths.length > 0
              ? `Initial prompt sent to Pi with ${imagePaths.length} image${imagePaths.length === 1 ? '' : 's'}.`
              : 'Initial prompt sent to Pi.',
            receivedAt,
          )
          socket.send(JSON.stringify({
            type: 'prompt',
            message: prompt,
            ...(imagePaths.length > 0 ? { images: imagePaths.map((path) => ({ path })) } : {}),
          }))
          initialPromptSentRef.current = true
          onInitialPromptSentRef.current?.()
        }
        if (activeRef.current) textareaRef.current?.focus()
        break
      }
      case 'pi_native_restarting':
        updateConnectionStatus('connecting')
        onContextStatusChangeRef.current(null)
        setError('')
        setNotice(typeof event.message === 'string' ? event.message : 'Restarting Pi to reload extensions…')
        appendActivity('pi_native_restarting', 'Restarting the native Pi process.', receivedAt)
        break
      case 'pi_native_reloaded':
        updateConnectionStatus('open')
        setConnectedAt(receivedAt)
        setLastPiResponseAt(null)
        setError('')
        setNotice(typeof event.message === 'string' ? event.message : 'Pi restarted and extensions reloaded.')
        appendActivity('pi_native_reloaded', 'Pi restarted and extensions reloaded.', receivedAt)
        markProbeSent(receivedAt)
        socket.send(JSON.stringify({ type: 'get_commands' }))
        socket.send(JSON.stringify({ type: 'get_available_models' }))
        if (activeRef.current) textareaRef.current?.focus()
        break
      case 'pi_native_error':
        setError(typeof event.message === 'string' ? event.message : 'The native Pi session reported an error.')
        appendActivity('pi_native_error', 'Native Pi reported an error.', receivedAt)
        break
      case 'pi_native_fatal':
        setError(typeof event.message === 'string' ? event.message : 'The native Pi session cannot start.')
        appendActivity('pi_native_fatal', 'Native Pi reported a non-retryable startup error.', receivedAt)
        break
      case 'pi_native_exit':
        setError(typeof event.message === 'string' ? event.message : 'Pi exited.')
        appendActivity('pi_native_exit', 'The native Pi process ended.', receivedAt)
        finishRun(undefined, undefined, receivedAt)
        onContextStatusChangeRef.current(null)
        updateConnectionStatus('closed')
        break
      case 'response':
        if (event.command === 'get_state') {
          const sentAt = probeSentAtRef.current
          if (event.success !== false) {
            setClockNow(receivedAt)
            setLastPiResponseAt(receivedAt)
            if (sentAt !== null) setLastProbeLatency(Math.max(0, receivedAt - sentAt))
          }
          probeSentAtRef.current = null
          setLastProbeSentAt(null)
        }
        if (event.success === false) {
          setError(event.error || `Pi rejected ${event.command || 'the request'}.`)
          appendActivity('response_error', `Pi rejected ${event.command || 'a request'}.`, receivedAt)
          if (event.command === 'get_session_stats') sessionStatsNoticePendingRef.current = false
          if (event.command === 'prompt' && promptSentAtRef.current !== null) {
            finishRun(undefined, undefined, receivedAt)
          }
          break
        }
        if (
          event.command === 'get_messages'
          && Array.isArray(event.data?.messages)
          && !displayHistoryAvailableRef.current
        ) {
          setMessages(event.data.messages)
          setLiveAssistant(null)
        }
        if (event.command === 'get_state') {
          const stateIsStreaming = Boolean(event.data?.isStreaming)
          if (stateIsStreaming) {
            promptSentAtRef.current = null
            beginRun(event.data?.isCompacting ? 'Compacting context' : runPhaseRef.current === 'Idle' ? 'Working' : runPhaseRef.current, receivedAt)
          } else if (
            isStreamingRef.current
            && (promptSentAtRef.current === null || receivedAt - promptSentAtRef.current > 2_000)
          ) {
            finishRun('get_state', 'Pi reports that the run is idle.', receivedAt)
          }
          const model = modelIdentifier(event.data?.model ?? event.data)
          if (model) {
            selectedModelRef.current = model
            setSelectedModel(model)
          }
          if (event.data?.thinkingLevel) setSelectedThinking(event.data.thinkingLevel)
        }
        if (event.command === 'get_commands' && Array.isArray(event.data?.commands)) {
          setPiCommands(normalizePiCommands(event.data.commands))
        }
        if (event.command === 'get_available_models' && Array.isArray(event.data?.models)) {
          setAvailableModels(normalizePiModels(event.data.models))
        }
        if (event.command === 'compact') {
          setNotice('Conversation context compacted.')
        }
        if (event.command === 'new_session') {
          if (event.data?.cancelled) {
            setNotice('Pi kept the current session.')
          } else {
            displayHistoryAvailableRef.current = false
            setMessages([])
            setLiveAssistant(null)
            setToolStates(new Map())
            setQueuedMessages([])
            setSessionStats(undefined)
            onContextStatusChangeRef.current(null)
            setNotice('Started a new Pi session.')
          }
        }
        if (event.command === 'set_model') {
          const model = modelIdentifier(event.data)
          if (model) {
            selectedModelRef.current = model
            setSelectedModel(model)
          }
          setNotice(model ? `Model switched to ${model}.` : 'Pi model updated.')
        }
        if (event.command === 'set_thinking_level') {
          if (event.data?.thinkingLevel) setSelectedThinking(event.data.thinkingLevel)
          setNotice('Pi thinking level updated.')
        }
        if (event.command === 'get_session_stats') {
          setSessionStats(event.data)
          onContextStatusChangeRef.current(nativeContextStatus(event.data?.contextUsage, selectedModelRef.current))
          if (sessionStatsNoticePendingRef.current) {
            sessionStatsNoticePendingRef.current = false
            setNotice(formatSessionStats(event.data))
          }
        }
        break
      case 'pi_native_history':
        if (Array.isArray(event.data?.messages)) {
          displayHistoryAvailableRef.current = true
          setMessages(event.data.messages)
          setLiveAssistant(null)
        }
        break
      case 'agent_start':
        promptSentAtRef.current = null
        latestAssistantMessageRef.current = null
        beginRun('Waiting for model', receivedAt)
        setRunEventCount(1)
        appendActivity('agent_start', 'Pi started processing the run.', receivedAt)
        setNotice('')
        break
      case 'agent_end':
        updateRunPhase(
          event.willRetry ? 'Waiting to retry' : 'Settling run',
          'agent_end',
          event.willRetry ? 'The model run ended and will retry.' : 'The model run ended; Pi is settling.',
          receivedAt,
        )
        break
      case 'agent_settled': {
        const startedAt = runStartedAtRef.current
        appendActivity(
          'agent_settled',
          startedAt === null
            ? 'Pi settled and is idle.'
            : `Pi settled after ${formatDuration(receivedAt - startedAt)}.`,
          receivedAt,
        )
        const diagnostic = assistantRunDiagnostic(latestAssistantMessageRef.current)
        if (diagnostic) {
          const detail = `${diagnostic.label}: ${diagnostic.text}`
          if (diagnostic.tone === 'error') setError(detail)
          else setNotice(detail)
        }
        latestAssistantMessageRef.current = null
        finishRun(undefined, undefined, receivedAt)
        setLiveAssistant(null)
        setToolStates(new Map())
        break
      }
      case 'turn_start':
        beginRun('Waiting for model', receivedAt)
        appendActivity('turn_start', 'A new model turn started.', receivedAt)
        break
      case 'turn_end':
        updateRunPhase('Processing turn result', 'turn_end', 'The current model turn completed.', receivedAt)
        break
      case 'message_start':
      case 'message_end':
        if (event.message && typeof event.message === 'object') {
          if (event.message.role === 'assistant') {
            setLiveAssistant(event.message)
            const phase = assistantMessagePhase(event.message)
            if (phase) updateRunPhase(phase, event.type, `${phase}.`, receivedAt)
            if (event.type === 'message_end') {
              latestAssistantMessageRef.current = event.message
              const diagnostic = assistantRunDiagnostic(event.message)
              if (diagnostic) {
                appendActivity(
                  `assistant_${event.message.stopReason || 'ended'}`,
                  `${diagnostic.label}: ${diagnostic.text}`,
                  receivedAt,
                )
              }
            }
          }
          setMessages((current) => upsertAgentMessage(current, event.message as PiAgentMessage))
        }
        break
      case 'message_update':
        if (event.message && typeof event.message === 'object' && event.message.role === 'assistant') {
          setLiveAssistant(event.message)
          const phase = assistantUpdatePhase(event)
          if (phase) updateRunPhase(phase, 'message_update', `${phase}.`, receivedAt)
        }
        break
      case 'tool_execution_start':
      case 'tool_execution_update':
      case 'tool_execution_end': {
        const callId = event.toolCallId
        if (!callId) break
        const toolName = event.toolName || 'tool'
        if (event.type === 'tool_execution_end') {
          updateRunPhase(
            `Processing ${toolName} result`,
            event.type,
            `${toolName} ${event.isError ? 'failed' : 'finished'}.`,
            receivedAt,
          )
        } else {
          beginRun(`Running ${toolName}`, receivedAt)
          if (event.type === 'tool_execution_start') {
            appendActivity(event.type, `${toolName} is running.`, receivedAt)
          }
        }
        setToolStates((current) => {
          const next = new Map(current)
          const previous = next.get(callId)
          next.set(callId, {
            callId,
            name: event.toolName || previous?.name || 'tool',
            args: event.args ?? previous?.args,
            output: event.type === 'tool_execution_end'
              ? event.result
              : event.type === 'tool_execution_update'
                ? event.partialResult
                : previous?.output,
            status: event.type === 'tool_execution_end'
              ? event.isError ? 'error' : 'success'
              : 'running',
            timestamp: previous?.timestamp ?? Date.now(),
          })
          return next
        })
        break
      }
      case 'queue_update': {
        const queued = [...(event.steering ?? []), ...(event.followUp ?? [])]
        setQueuedMessages(queued)
        if (queued.length > 0) appendActivity('queue_update', `${queued.length} prompt${queued.length === 1 ? '' : 's'} queued.`, receivedAt)
        break
      }
      case 'compaction_start':
        beginRun('Compacting context', receivedAt)
        appendActivity('compaction_start', 'Pi started compacting conversation context.', receivedAt)
        setNotice('Compacting conversation context…')
        break
      case 'compaction_end':
        if (event.errorMessage) {
          updateRunPhase('Compaction failed', 'compaction_end', `Context compaction failed: ${event.errorMessage}`, receivedAt)
          setNotice('')
          setError(`Context compaction failed: ${event.errorMessage}`)
        } else if (event.aborted) {
          updateRunPhase('Compaction stopped', 'compaction_end', 'Conversation context compaction was stopped.', receivedAt)
          setNotice('Conversation context compaction stopped.')
        } else {
          updateRunPhase('Resuming work', 'compaction_end', 'Conversation context compaction finished.', receivedAt)
          setNotice('Conversation context compacted.')
        }
        break
      case 'auto_retry_start': {
        beginRun('Waiting to retry', receivedAt)
        const detail = event.errorMessage?.trim()
        appendActivity(
          'auto_retry_start',
          detail ? `Pi is retrying after: ${detail}` : 'Pi is waiting to retry a provider request.',
          receivedAt,
        )
        setNotice(detail
          ? `Pi is retrying after a temporary provider error: ${detail}`
          : 'Pi is retrying after a temporary provider error…')
        break
      }
      case 'auto_retry_end':
        if (event.success === false) {
          const detail = event.finalError?.trim() || 'The provider request still failed after automatic retries.'
          updateRunPhase('Retry failed', 'auto_retry_end', `Provider retry failed: ${detail}`, receivedAt)
          setNotice('')
          setError(detail)
        } else {
          updateRunPhase('Resuming work', 'auto_retry_end', 'Pi finished the provider retry.', receivedAt)
          setNotice('')
        }
        break
      case 'extension_error':
        setError(event.error || 'A Pi extension failed.')
        appendActivity('extension_error', 'A Pi extension failed.', receivedAt)
        break
      case 'extension_ui_request':
        if (event.method === 'notify' && typeof event.message === 'string') {
          if (event.notifyType === 'error') setError(event.message)
          else setNotice(event.message)
        }
        break
    }
  }, [appendActivity, beginRun, finishRun, markProbeSent, updateConnectionStatus, updateRunPhase])

  useEffect(() => () => imageUploadControllerRef.current?.abort(), [])

  useEffect(() => {
    writePiNativeDraft(projectId, threadId, draft)
    writePiNativePastes(projectId, threadId, draftPastes)
  }, [draft, draftPastes, projectId, threadId])

  const nativeSocketUrl = useMemo(() => {
    const params = new URLSearchParams()
    if (initialModelRef.current) {
      params.set('model', initialModelRef.current)
    }
    if (initialThinkingRef.current) {
      params.set('thinking', initialThinkingRef.current)
    }
    const url = apiWebSocketUrl(
      `/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(threadId)}/pi/native`,
    )
    url.search = params.toString()
    return url.toString()
  }, [projectId, threadId])

  const resetConnectionDiagnostics = useCallback(() => {
    setConnectedAt(null)
    setLastPiResponseAt(null)
    setLastProbeLatency(null)
    setLastProbeSentAt(null)
    setError('')
  }, [])

  const { send: sendSocketCommand, socketRef } = useNativeAgentSocket<PiRpcEvent>({
    agentName: 'Pi',
    fatalEventType: 'pi_native_fatal',
    onActivity: appendActivity,
    onAttempt: resetConnectionDiagnostics,
    onContextReset: () => onContextStatusChangeRef.current(null),
    onError: setError,
    onEvent: handleEvent,
    onProbeSent: markProbeSent,
    onStatusChange: updateConnectionStatus,
    probeSentAtRef,
    readyEventType: 'pi_native_ready',
    url: nativeSocketUrl,
  })

  const timeline = useMemo(
    () => buildTimeline(messages, liveAssistant, toolStates, isStreaming),
    [isStreaming, liveAssistant, messages, toolStates],
  )
  const expandedDraft = expandPromptPastes(draft, draftPastes)
  const latestCacheHitRate = useMemo(() => piLatestCacheHitRate(messages), [messages])
  const composerSuggestions = useMemo(
    () => buildComposerSuggestions(draft, piCommands, availableModels),
    [availableModels, draft, piCommands],
  )
  const visibleComposerSuggestions = slashMenuDismissed ? [] : composerSuggestions

  useEffect(() => {
    setSelectedSlashIndex((current) => Math.min(current, Math.max(0, composerSuggestions.length - 1)))
  }, [composerSuggestions.length])

  useEffect(() => {
    const pane = timelineRef.current
    if (!pane || !atBottomRef.current) return
    const frame = window.requestAnimationFrame(() => {
      pane.scrollTop = pane.scrollHeight
    })
    return () => window.cancelAnimationFrame(frame)
  }, [timeline])

  useEffect(() => {
    if (!isStreaming && !activityExpanded && connectionStatus === 'open' && lastProbeSentAt === null) return
    setClockNow(Date.now())
    const timer = window.setInterval(() => setClockNow(Date.now()), 1_000)
    return () => window.clearInterval(timer)
  }, [activityExpanded, connectionStatus, isStreaming, lastProbeSentAt])

  useEffect(() => {
    const textarea = textareaRef.current
    if (!textarea) return
    textarea.style.height = '0px'
    textarea.style.height = `${Math.min(180, Math.max(54, textarea.scrollHeight))}px`
  }, [draft])

  useEffect(() => {
    if (!active) return
    const frame = window.requestAnimationFrame(() => textareaRef.current?.focus())
    return () => window.cancelAnimationFrame(frame)
  }, [active])

  function clearSubmittedDraft() {
    setDraft('')
    setDraftPastes([])
    clearDraftImages()
    setError('')
    setSlashMenuDismissed(false)
    setSelectedSlashIndex(0)
    atBottomRef.current = true
  }

  async function sendPrompt(queueMode?: 'steer' | 'followUp') {
    const message = expandedDraft.trim()
    const images = [...draftImages]
    if ((!message && images.length === 0) || promptSubmissionRef.current) return
    const socket = socketRef.current
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      setError('Pi is still connecting.')
      return
    }

    promptSubmissionRef.current = true
    const uploadController = new AbortController()
    imageUploadControllerRef.current = uploadController
    setIsUploadingImages(images.length > 0)
    setError('')
    try {
      const uploads = await Promise.all(images.map((image) =>
        uploadPiImage(projectId, image.file, uploadController.signal),
      ))
      if (uploadController.signal.aborted) return

      const wasStreaming = isStreamingRef.current
      if (!sendSocketCommand({
        type: 'prompt',
        message,
        ...(uploads.length > 0 ? { images: uploads.map(({ path }) => ({ path })) } : {}),
        ...(queueMode ? { streamingBehavior: queueMode } : {}),
      })) return

      const sentAt = Date.now()
      if (!wasStreaming) {
        promptSentAtRef.current = sentAt
        beginRun('Sending prompt', sentAt)
      }
      appendActivity(
        queueMode ? 'prompt_queued' : 'prompt',
        queueMode
          ? `Prompt${uploads.length > 0 ? ' with images' : ''} queued for the active run.`
          : `Prompt sent to Pi${uploads.length > 0 ? ` with ${uploads.length} image${uploads.length === 1 ? '' : 's'}` : ''}.`,
        sentAt,
      )
      clearSubmittedDraft()
      setNotice('')
    } catch (reason) {
      if (!uploadController.signal.aborted) {
        setError(reason instanceof Error ? reason.message : 'Could not attach the selected images.')
      }
    } finally {
      if (imageUploadControllerRef.current === uploadController) {
        imageUploadControllerRef.current = null
        promptSubmissionRef.current = false
        setIsUploadingImages(false)
      }
    }
  }

  function handleSlashCommand(message: string): boolean {
    return runPiSlashCommand(message, {
      isStreaming,
      hasImageAttachments: draftImages.length > 0,
      selectedModel,
      selectedThinking,
      send: sendSocketCommand,
      setError,
      setNotice,
      clearSubmittedDraft,
      markSessionStatsPending: () => {
        sessionStatsNoticePendingRef.current = true
      },
    })
  }

  function submitDraft(queueMode?: 'steer' | 'followUp') {
    const message = expandedDraft.trim()
    if ((!message && draftImages.length === 0) || (message && handleSlashCommand(message))) return
    void sendPrompt(queueMode)
  }

  function addDraftImages(files: File[]) {
    if (files.length === 0 || isUploadingImages) return
    setError(addDraftImageFiles(files, piNativePromptImagePolicy))
  }

  function handleImageInput(event: ChangeEvent<HTMLInputElement>) {
    addDraftImages(Array.from(event.target.files ?? []))
    event.target.value = ''
  }

  function handleComposerPaste(event: ClipboardEvent<HTMLTextAreaElement>) {
    addDraftImages(imageFilesFromClipboard(event.clipboardData))
    const pastedText = event.clipboardData.getData('text/plain')
    if (!pastedText) return
    const textarea = event.currentTarget
    const collapsed = collapsePromptPaste({
      value: draft,
      selectionStart: textarea.selectionStart,
      selectionEnd: textarea.selectionEnd,
      pastedText,
      pastes: draftPastes,
    })
    if (!collapsed) return

    event.preventDefault()
    setDraft(collapsed.value)
    setDraftPastes(collapsed.pastes)
    setError('')
    window.requestAnimationFrame(() => {
      textarea.setSelectionRange(collapsed.selectionStart, collapsed.selectionStart)
    })
  }

  function handleDraftChange(value: string) {
    const nextPastes = prunePromptPastes(value, draftPastes)
    setDraft(value)
    setDraftPastes(nextPastes)
    setSlashMenuDismissed(false)
    setSelectedSlashIndex(0)
  }

  function handleComposerDrop(event: DragEvent<HTMLTextAreaElement>) {
    const files = Array.from(event.dataTransfer.files)
    if (files.length === 0) return
    event.preventDefault()
    addDraftImages(files)
  }

  function removeDraftImage(id: number) {
    if (isUploadingImages) return
    removeDraftImageAttachment(id)
    setError('')
  }

  function abortRun() {
    if (sendSocketCommand({ type: 'abort' })) {
      updateRunPhase('Stopping', 'abort', 'Stop requested.', Date.now())
    }
  }

  function inspectNow() {
    const sentAt = Date.now()
    if (sendSocketCommand({ type: 'get_state' })) markProbeSent(sentAt)
  }

  function selectModel(identifier: string) {
    const separator = identifier.indexOf('/')
    if (separator <= 0) return
    const provider = identifier.slice(0, separator)
    const modelId = identifier.slice(separator + 1)
    if (!sendSocketCommand({ type: 'set_model', provider, modelId })) return
    selectedModelRef.current = identifier
    setSelectedModel(identifier)
    setNotice(`Switching Pi to ${identifier}…`)
  }

  function selectThinking(level: string) {
    if (!piThinkingLevelIds.some((candidate) => candidate === level)) return
    if (!sendSocketCommand({ type: 'set_thinking_level', level })) return
    setSelectedThinking(level)
    setNotice(`Setting Pi thinking to ${level}…`)
  }

  function applyComposerSuggestion(suggestion: ComposerSuggestion) {
    setDraft(suggestion.completion)
    setDraftPastes([])
    setSelectedSlashIndex(0)
    setSlashMenuDismissed(!suggestion.completion.endsWith(' '))
    window.requestAnimationFrame(() => {
      const textarea = textareaRef.current
      if (!textarea) return
      textarea.focus()
      textarea.setSelectionRange(suggestion.completion.length, suggestion.completion.length)
    })
  }

  function handleComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.nativeEvent.isComposing) return
    if (visibleComposerSuggestions.length > 0) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault()
        const direction = event.key === 'ArrowDown' ? 1 : -1
        setSelectedSlashIndex((current) => (
          current + direction + visibleComposerSuggestions.length
        ) % visibleComposerSuggestions.length)
        return
      }
      if (event.key === 'Escape') {
        event.preventDefault()
        setSlashMenuDismissed(true)
        return
      }
      const selectedSuggestion = visibleComposerSuggestions[selectedSlashIndex]
      if (event.key === 'Tab' && selectedSuggestion) {
        event.preventDefault()
        applyComposerSuggestion(selectedSuggestion)
        return
      }
      if (event.key === 'Enter' && !event.shiftKey && selectedSuggestion && draft !== selectedSuggestion.completion) {
        event.preventDefault()
        applyComposerSuggestion(selectedSuggestion)
        return
      }
    }
    if (event.key !== 'Enter' || event.shiftKey) return
    event.preventDefault()
    if ((!draft.trim() && draftImages.length === 0) || isUploadingImages) return
    if (isStreaming) {
      submitDraft(event.metaKey || event.ctrlKey ? 'steer' : 'followUp')
      return
    }
    submitDraft()
  }

  function handleTimelineScroll() {
    const pane = timelineRef.current
    if (!pane) return
    const atBottom = pane.scrollHeight - pane.scrollTop - pane.clientHeight < 48
    atBottomRef.current = atBottom
    setShowJumpToLatest(!atBottom)
  }

  const hasDraftContent = draft.trim().length > 0 || draftImages.length > 0
  const canSend = connectionStatus === 'open' && hasDraftContent && !isUploadingImages
  const primaryActionIsStop = isStreaming && !hasDraftContent && !isUploadingImages
  const activity = deriveAgentActivity({
    clockNow,
    connectedAt,
    connectionStatus,
    isStreaming,
    lastResponseAt: lastPiResponseAt,
    lastProbeSentAt,
    latestWorkEvent,
    runPhase,
    runStartedAt,
  })
  const { activityToggleLabel, monitorTone } = activity
  const activityPanel = activityExpanded ? (
    <NativeAgentActivityMonitor
      agent={PI_AGENT}
      view={activity}
      connectionStatus={connectionStatus}
      isStreaming={isStreaming}
      runPhase={runPhase}
      runEventCount={runEventCount}
      connectedAt={connectedAt}
      lastProbeLatency={lastProbeLatency}
      latestEvent={latestRpcEvent}
      latestWorkEvent={latestWorkEvent}
      clockNow={clockNow}
      activityLog={activityLog}
      sessionUsage={<PiSessionUsage stats={sessionStats} latestCacheHitRate={latestCacheHitRate} />}
      onInspect={inspectNow}
      onHide={() => setActivityExpanded(false)}
      onNotice={setNotice}
      onError={setError}
    />
  ) : null
  const composerModelOptions = availableModels.flatMap((model) => {
    const identifier = modelIdentifier(model)
    return identifier ? [{ value: identifier, label: model.name || identifier }] : []
  })
  const composerHint = isUploadingImages
    ? `Uploading ${draftImages.length} image${draftImages.length === 1 ? '' : 's'}…`
    : connectionStatus !== 'open'
      ? 'Connecting to Pi…'
      : isStreaming
        ? 'Enter to queue · ⌘Enter to steer'
        : 'Enter to send · Shift+Enter for newline'

  return (
    <section
      role="tabpanel"
      aria-label={`${threadTitle} native Pi conversation`}
      aria-hidden={!active}
      className={classNames(
        piNativeStyles.pane,
        active ? piNativeStyles.paneActive : piNativeStyles.paneHidden,
      )}
    >
      <div className={piNativeStyles.timeline} ref={timelineRef} onScroll={handleTimelineScroll}>
        <div className={piNativeStyles.conversation} data-testid="pi-native-conversation">
          {timeline.length === 0 ? (
            <div className={piNativeStyles.empty} data-testid="pi-native-empty">
              <span className={piNativeStyles.emptyGlyph} aria-hidden="true"><Bot size={22} /></span>
              <h2 className={piNativeStyles.emptyTitle}>Start a conversation with Pi</h2>
              <p className={piNativeStyles.emptyCopy}>
                Send a prompt below. Pi’s turns and tool activity will appear here.
              </p>
            </div>
          ) : (
            timeline.map((entry) => <PiNativeTimelineEntry entry={entry} key={entry.key} />)
          )}
        </div>
      </div>

      {showJumpToLatest && (
        <button
          type="button"
          className={piNativeStyles.jump}
          onClick={() => {
            const pane = timelineRef.current
            if (!pane) return
            atBottomRef.current = true
            setShowJumpToLatest(false)
            pane.scrollTo({ top: pane.scrollHeight, behavior: 'smooth' })
          }}
        >
          <ArrowDown size={13} /> New activity
        </button>
      )}

      <PiNativeComposer
        monitorTone={monitorTone}
        activityExpanded={activityExpanded}
        activityToggleLabel={activityToggleLabel}
        isStreaming={isStreaming}
        queuedMessages={queuedMessages}
        notice={notice}
        error={error}
        activityPanel={activityPanel}
        suggestions={visibleComposerSuggestions}
        selectedSuggestionIndex={selectedSlashIndex}
        onToggleActivity={() => setActivityExpanded((value) => !value)}
        onSelectSuggestion={(index) => {
          const suggestion = visibleComposerSuggestions[index]
          if (suggestion) applyComposerSuggestion(suggestion)
        }}
        attachments={draftImages}
        isUploadingImages={isUploadingImages}
        onRemoveAttachment={removeDraftImage}
        textareaRef={textareaRef}
        draft={draft}
        onDraftChange={handleDraftChange}
        onPaste={handleComposerPaste}
        onDrop={handleComposerDrop}
        onKeyDown={handleComposerKeyDown}
        model={selectedModel}
        modelOptions={composerModelOptions}
        modelDisabled={connectionStatus !== 'open' || isStreaming || isUploadingImages}
        onModelChange={selectModel}
        thinking={selectedThinking}
        thinkingOptions={piThinkingLevelIds.map((level) => ({ value: level, label: level }))}
        thinkingDisabled={connectionStatus !== 'open' || isStreaming || isUploadingImages}
        onThinkingChange={selectThinking}
        onImageInput={handleImageInput}
        hint={composerHint}
        primaryActionIsStop={primaryActionIsStop}
        canSend={canSend}
        onPrimaryAction={() => (
          primaryActionIsStop ? abortRun() : submitDraft(isStreaming ? 'followUp' : undefined)
        )}
      />
    </section>
  )
}
