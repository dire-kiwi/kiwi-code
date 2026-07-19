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
import { getSettings, uploadPiImage } from '../../api'
import { apiWebSocketUrl } from '../../apiUrl'
import { piThinkingLevelIds } from '../../codingAgents'
import { classNames } from '../../lib/classNames'
import { formatDuration } from '../../lib/formatDuration'
import {
  imageFilesFromClipboard,
  isSupportedPiImageType,
  piNativePromptImagePolicy,
} from '../../lib/promptImages'
import {
  readPiNativeDraft,
  readPiNativeWorkflowDismissed,
  writePiNativeDraft,
  writePiNativeWorkflowDismissed,
} from '../../lib/promptDrafts'
import { useImageAttachments } from '../../lib/useImageAttachments'
import type { AgentContextStatus, ConnectionStatus } from '../../types'
import { piNativeStyles } from './piNativeStyles'
import {
  PiNativeActivityPanel,
  type PiStatusTone,
} from './PiNativeActivityPanel'
import { PiNativeComposer, piSlashSourceLabel } from './PiNativeComposer'
import {
  PiNativeTimelineEntry,
  type PiTimelineEntryValue as TimelineEntry,
} from './PiNativeTimeline'

type PiNativePaneProps = {
  projectId: string
  threadId: string
  threadTitle: string
  initialModel?: string
  initialThinkingLevel?: string
  initialPrompt?: string
  initialImagePaths?: string[]
  onInitialPromptSent?: () => void
  readOnly: boolean
  active: boolean
  onStatusChange: (status: ConnectionStatus) => void
  onContextStatusChange: (status: AgentContextStatus | null) => void
}

type PiRenderedImage = {
  data: string
  mimeType: string
}

type PiContentBlock = {
  type?: string
  text?: string
  thinking?: string
  id?: string
  name?: string
  arguments?: unknown
  data?: string
  mimeType?: string
}

type PiAgentMessage = {
  role?: string
  content?: string | PiContentBlock[]
  summary?: string
  timestamp?: number | string
  toolCallId?: string
  toolName?: string
  isError?: boolean
  stopReason?: string
  errorMessage?: string
  command?: string
  output?: string
  exitCode?: number
  usage?: {
    input?: number
    output?: number
    cacheRead?: number
    cacheWrite?: number
  }
}

type PiToolState = {
  callId: string
  name: string
  args?: unknown
  output?: unknown
  status: 'running' | 'success' | 'error'
  timestamp: number
}

type PiSlashCommandSource = 'native' | 'extension' | 'prompt' | 'skill'

type PiSlashCommand = {
  name: string
  description?: string
  source: PiSlashCommandSource
}

type PiModel = {
  provider?: string
  id?: string
  name?: string
}

type PiContextUsage = {
  tokens: number | null
  contextWindow: number
  percent: number | null
}

type PiSessionStats = {
  totalMessages?: number
  toolCalls?: number
  tokens?: {
    input?: number
    output?: number
    cacheRead?: number
    cacheWrite?: number
    total?: number
  }
  cost?: number
  contextUsage?: PiContextUsage
}

type PiActivityRecord = {
  id: number
  at: number
  event: string
  summary: string
  repeats: number
}

type PiEventStamp = {
  at: number
  label: string
}

type ComposerSuggestion = {
  id: string
  label: string
  description: string
  source: PiSlashCommandSource | 'model' | 'level'
  completion: string
}

type PiRunDiagnostic = {
  label: string
  text: string
  tone: 'warning' | 'error'
}

type PiRpcEvent = {
  type?: string
  command?: string
  success?: boolean
  error?: string
  message?: PiAgentMessage | string
  data?: {
    messages?: PiAgentMessage[]
    isStreaming?: boolean
    isCompacting?: boolean
    pendingMessageCount?: number
    commands?: PiSlashCommand[]
    models?: PiModel[]
    model?: PiModel
    cancelled?: boolean
    provider?: string
    id?: string
    name?: string
    thinkingLevel?: string
    totalMessages?: number
    toolCalls?: number
    tokens?: PiSessionStats['tokens']
    cost?: number
    contextUsage?: PiContextUsage
  }
  toolCallId?: string
  toolName?: string
  args?: unknown
  partialResult?: unknown
  result?: unknown
  isError?: boolean
  steering?: string[]
  followUp?: string[]
  notifyType?: string
  method?: string
  assistantMessageEvent?: { type?: string }
  willRetry?: boolean
  errorMessage?: string
  finalError?: string
  aborted?: boolean
}

const RECONNECT_STABLE_AFTER_MS = 5_000
const PI_INSPECTION_INTERVAL_MS = 4_000
const PI_RESPONSE_STALE_AFTER_MS = 12_000
const PI_ACTIVITY_LOG_LIMIT = 24
const WORKFLOW_DISMISS_MARKER = '\u2063dire-mux-no-ultracode\u2063'
const ULTRACODE_KEYWORD_PATTERN = /\bultracode\b/i

const NATIVE_SLASH_COMMANDS: PiSlashCommand[] = [
  {
    name: 'compact',
    description: 'Summarize older context to make room for more work',
    source: 'native',
  },
  {
    name: 'reload',
    description: 'Restart Pi Native, reload extensions, and resume this conversation',
    source: 'native',
  },
  {
    name: 'restart',
    description: 'Restart Pi Native and resume this saved conversation',
    source: 'native',
  },
  {
    name: 'new',
    description: 'Start a new saved Pi session in this thread',
    source: 'native',
  },
  {
    name: 'model',
    description: 'Switch the model for this Pi session',
    source: 'native',
  },
  {
    name: 'thinking',
    description: 'Set Pi’s thinking level',
    source: 'native',
  },
  {
    name: 'session',
    description: 'Show message, tool, token, cache, and cost totals',
    source: 'native',
  },
]

export function PiNativePane({
  projectId,
  threadId,
  threadTitle,
  initialModel,
  initialThinkingLevel,
  initialPrompt,
  initialImagePaths,
  onInitialPromptSent,
  readOnly,
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
  const {
    attachments: draftImages,
    addFiles: addDraftImageFiles,
    removeAttachment: removeDraftImageAttachment,
    clearAttachments: clearDraftImages,
  } = useImageAttachments()
  const [isUploadingImages, setIsUploadingImages] = useState(false)
  const [slashMenuDismissed, setSlashMenuDismissed] = useState(false)
  const [workflowKeywordDismissed, setWorkflowKeywordDismissed] = useState(() => (
    readPiNativeWorkflowDismissed(projectId, threadId)
    && ULTRACODE_KEYWORD_PATTERN.test(readPiNativeDraft(projectId, threadId))
  ))
  const [workflowKeywordTriggerEnabled, setWorkflowKeywordTriggerEnabled] = useState(false)
  const [workflowsEnabled, setWorkflowsEnabled] = useState(false)
  const [selectedSlashIndex, setSelectedSlashIndex] = useState(0)
  const [isStreaming, setIsStreaming] = useState(false)
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>('connecting')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [showJumpToLatest, setShowJumpToLatest] = useState(false)
  const [activityExpanded, setActivityExpanded] = useState(false)
  const [activityLog, setActivityLog] = useState<PiActivityRecord[]>([])
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
  const [connectionAttempt, setConnectionAttempt] = useState(0)
  const socketRef = useRef<WebSocket | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const timelineRef = useRef<HTMLDivElement>(null)
  const activeRef = useRef(active)
  const readOnlyRef = useRef(readOnly)
  const atBottomRef = useRef(true)
  const reconnectAttemptsRef = useRef(0)
  const activitySequenceRef = useRef(0)
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
  readOnlyRef.current = readOnly
  isStreamingRef.current = isStreaming
  onStatusChangeRef.current = onStatusChange
  onContextStatusChangeRef.current = onContextStatusChange
  onInitialPromptSentRef.current = onInitialPromptSent
  if (!initialPromptSentRef.current) {
    if (initialPrompt !== undefined) initialPromptRef.current = initialPrompt
    if (initialImagePaths?.length) initialImagePathsRef.current = [...initialImagePaths]
  }

  useEffect(() => {
    writePiNativeWorkflowDismissed(projectId, threadId, workflowKeywordDismissed)
  }, [projectId, threadId, workflowKeywordDismissed])

  useEffect(() => {
    const controller = new AbortController()
    getSettings(controller.signal)
      .then((settings) => {
        setWorkflowKeywordTriggerEnabled(settings.workflowKeywordTriggerEnabled)
        setWorkflowsEnabled(!settings.disableWorkflows)
      })
      .catch(() => {
        // Activation remains fail-closed on the backend if settings cannot be loaded.
      })
    return () => controller.abort()
  }, [])

  const updateConnectionStatus = useCallback((status: ConnectionStatus) => {
    setConnectionStatus(status)
    onStatusChangeRef.current(status)
  }, [])

  const appendActivity = useCallback((event: string, summary: string, at = Date.now()) => {
    setActivityLog((current) => {
      const latest = current[0]
      if (latest && latest.event === event && latest.summary === summary && at - latest.at < 1_500) {
        return [{ ...latest, at, repeats: latest.repeats + 1 }, ...current.slice(1)]
      }
      return [{
        id: activitySequenceRef.current += 1,
        at,
        event,
        summary,
        repeats: 1,
      }, ...current].slice(0, PI_ACTIVITY_LOG_LIMIT)
    })
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
        if (!readOnlyRef.current && (prompt || imagePaths.length > 0) && !initialPromptSentRef.current) {
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
        if (activeRef.current && !readOnlyRef.current) textareaRef.current?.focus()
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
        if (activeRef.current && !readOnlyRef.current) textareaRef.current?.focus()
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
  }, [draft, projectId, threadId])

  useEffect(() => {
    if (!readOnly) return
    imageUploadControllerRef.current?.abort()
    setDraft('')
    clearDraftImages()
    setSlashMenuDismissed(true)
    setSelectedSlashIndex(0)
  }, [clearDraftImages, readOnly])

  useEffect(() => {
    let disposed = false
    let reconnectTimer: ReturnType<typeof window.setTimeout> | undefined
    let stableTimer: ReturnType<typeof window.setTimeout> | undefined
    let inspectionTimer: ReturnType<typeof window.setInterval> | undefined
    let piReady = false
    let reconnectAllowed = true
    updateConnectionStatus('connecting')
    onContextStatusChangeRef.current(null)
    setConnectedAt(null)
    setLastPiResponseAt(null)
    setLastProbeLatency(null)
    setLastProbeSentAt(null)
    probeSentAtRef.current = null
    setError('')

    const params = new URLSearchParams()
    if (!readOnlyRef.current && initialModelRef.current) params.set('model', initialModelRef.current)
    if (!readOnlyRef.current && initialThinkingRef.current) params.set('thinking', initialThinkingRef.current)
    const url = apiWebSocketUrl(
      `/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(threadId)}/pi/native`,
    )
    url.search = params.toString()
    const socket = new WebSocket(url)
    socketRef.current = socket

    socket.addEventListener('open', () => {
      if (disposed) {
        socket.close(1000, 'Native Pi pane closed')
        return
      }
      stableTimer = window.setTimeout(() => {
        if (!disposed && socket.readyState === WebSocket.OPEN) reconnectAttemptsRef.current = 0
      }, RECONNECT_STABLE_AFTER_MS)
      inspectionTimer = window.setInterval(() => {
        const now = Date.now()
        const pendingSince = probeSentAtRef.current
        if (
          disposed
          || !piReady
          || socket.readyState !== WebSocket.OPEN
          || (pendingSince !== null && now - pendingSince <= PI_RESPONSE_STALE_AFTER_MS)
        ) return
        const sentAt = now
        markProbeSent(sentAt)
        socket.send(JSON.stringify({ type: 'get_state' }))
      }, PI_INSPECTION_INTERVAL_MS)
    })

    socket.addEventListener('message', (messageEvent) => {
      if (disposed || typeof messageEvent.data !== 'string') return
      try {
        const event = JSON.parse(messageEvent.data) as PiRpcEvent
        if (event.type === 'pi_native_ready') piReady = true
        if (event.type === 'pi_native_fatal') reconnectAllowed = false
        handleEvent(event, socket)
      } catch {
        setError('Pi sent an unreadable conversation update.')
      }
    })

    socket.addEventListener('error', () => {
      if (!disposed) {
        appendActivity('connection_error', 'The native Pi WebSocket reported an error.')
        onContextStatusChangeRef.current(null)
        updateConnectionStatus('error')
      }
    })

    socket.addEventListener('close', () => {
      piReady = false
      if (stableTimer !== undefined) window.clearTimeout(stableTimer)
      if (inspectionTimer !== undefined) window.clearInterval(inspectionTimer)
      if (disposed) return
      if (!reconnectAllowed) {
        appendActivity('connection_closed', 'Connection closed; automatic reconnect is disabled for this startup error.')
        onContextStatusChangeRef.current(null)
        updateConnectionStatus('error')
        return
      }
      appendActivity('connection_closed', 'Connection lost; Dire Mux is reconnecting.')
      onContextStatusChangeRef.current(null)
      updateConnectionStatus('connecting')
      const delay = Math.min(250 * 2 ** reconnectAttemptsRef.current, 2_000)
      reconnectAttemptsRef.current += 1
      reconnectTimer = window.setTimeout(() => {
        if (!disposed) setConnectionAttempt((value) => value + 1)
      }, delay)
    })

    return () => {
      disposed = true
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      if (stableTimer !== undefined) window.clearTimeout(stableTimer)
      if (inspectionTimer !== undefined) window.clearInterval(inspectionTimer)
      if (socket.readyState < WebSocket.CLOSING) socket.close(1000, 'Native Pi pane closed')
      if (socketRef.current === socket) socketRef.current = null
      onContextStatusChangeRef.current(null)
    }
  }, [appendActivity, connectionAttempt, handleEvent, markProbeSent, projectId, threadId, updateConnectionStatus])

  const timeline = useMemo(
    () => buildTimeline(messages, liveAssistant, toolStates, isStreaming),
    [isStreaming, liveAssistant, messages, toolStates],
  )
  const latestCacheHitRate = useMemo(() => piLatestCacheHitRate(messages), [messages])
  const composerSuggestions = useMemo(
    () => buildComposerSuggestions(draft, piCommands, availableModels),
    [availableModels, draft, piCommands],
  )
  const visibleComposerSuggestions = readOnly || slashMenuDismissed ? [] : composerSuggestions

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
    if (!active || readOnly) return
    const frame = window.requestAnimationFrame(() => textareaRef.current?.focus())
    return () => window.cancelAnimationFrame(frame)
  }, [active, readOnly])

  function sendSocketCommand(command: Record<string, unknown> & { type: string }): boolean {
    const socket = socketRef.current
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      setError('Pi is still connecting.')
      return false
    }
    socket.send(JSON.stringify(command))
    return true
  }

  function clearSubmittedDraft() {
    setDraft('')
    clearDraftImages()
    setError('')
    setSlashMenuDismissed(false)
    setWorkflowKeywordDismissed(false)
    setSelectedSlashIndex(0)
    atBottomRef.current = true
  }

  async function sendPrompt(queueMode?: 'steer' | 'followUp') {
    if (readOnlyRef.current) return
    const message = draft.trim()
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
      if (uploadController.signal.aborted || readOnlyRef.current) return

      const wasStreaming = isStreamingRef.current
      if (!sendSocketCommand({
        type: 'prompt',
        message: workflowsEnabled && workflowKeywordTriggerEnabled && workflowKeywordDismissed && ULTRACODE_KEYWORD_PATTERN.test(message)
          ? WORKFLOW_DISMISS_MARKER + message
          : message,
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

  function runNativeSlashCommand(message: string): boolean {
    if (readOnlyRef.current) return true
    const match = message.match(/^\/(compact|reload|restart|new|model|thinking|session)(?:\s+([\s\S]*))?$/)
    if (!match) return false

    const commandName = match[1]
    const argument = (match[2] ?? '').trim()
    if (draftImages.length > 0) {
      setError(`Remove image attachments before running /${commandName}.`)
      return true
    }
    if (isStreaming && commandName !== 'session') {
      setError(`Wait for Pi to finish before running /${commandName}.`)
      return true
    }

    if (commandName === 'compact') {
      if (!sendSocketCommand({
        type: 'compact',
        ...(argument ? { customInstructions: argument } : {}),
      })) return true
      clearSubmittedDraft()
      setNotice('Compacting conversation context…')
      return true
    }

    if (commandName === 'reload' || commandName === 'restart') {
      if (argument) {
        setError(`Use /${commandName} without arguments.`)
        return true
      }
      const modelSeparator = selectedModel.indexOf('/')
      const provider = modelSeparator > 0 ? selectedModel.slice(0, modelSeparator) : ''
      const modelId = modelSeparator > 0 ? selectedModel.slice(modelSeparator + 1) : ''
      if (!sendSocketCommand({
        type: commandName,
        ...(provider && modelId ? { provider, modelId } : {}),
        ...(piThinkingLevelIds.some((level) => level === selectedThinking)
          ? { level: selectedThinking }
          : {}),
      })) return true
      clearSubmittedDraft()
      setNotice(commandName === 'reload'
        ? 'Restarting Pi to reload extensions…'
        : 'Restarting Pi Native…')
      return true
    }

    if (commandName === 'new') {
      if (argument) {
        setError('Use /new without arguments.')
        return true
      }
      if (!sendSocketCommand({ type: 'new_session' })) return true
      clearSubmittedDraft()
      setNotice('Starting a new Pi session…')
      return true
    }

    if (commandName === 'model') {
      const separator = argument.indexOf('/')
      const provider = separator > 0 ? argument.slice(0, separator) : ''
      const modelId = separator > 0 ? argument.slice(separator + 1) : ''
      if (!provider || !modelId) {
        setError('Use /model <provider/model>.')
        return true
      }
      if (!sendSocketCommand({ type: 'set_model', provider, modelId })) return true
      clearSubmittedDraft()
      setNotice(`Switching Pi to ${provider}/${modelId}…`)
      return true
    }

    if (commandName === 'thinking') {
      if (!piThinkingLevelIds.some((level) => level === argument)) {
        setError(`Use /thinking <${piThinkingLevelIds.join('|')}>.`)
        return true
      }
      if (!sendSocketCommand({ type: 'set_thinking_level', level: argument })) return true
      clearSubmittedDraft()
      setNotice(`Setting Pi thinking to ${argument}…`)
      return true
    }

    if (argument) {
      setError('Use /session without arguments.')
      return true
    }
    if (!sendSocketCommand({ type: 'get_session_stats' })) return true
    sessionStatsNoticePendingRef.current = true
    clearSubmittedDraft()
    setNotice('Loading Pi session totals…')
    return true
  }

  function submitDraft(queueMode?: 'steer' | 'followUp') {
    if (readOnlyRef.current) return
    const message = draft.trim()
    if ((!message && draftImages.length === 0) || (message && runNativeSlashCommand(message))) return
    void sendPrompt(queueMode)
  }

  function addDraftImages(files: File[]) {
    if (readOnlyRef.current || files.length === 0 || isUploadingImages) return
    setError(addDraftImageFiles(files, piNativePromptImagePolicy))
  }

  function handleImageInput(event: ChangeEvent<HTMLInputElement>) {
    if (!readOnlyRef.current) addDraftImages(Array.from(event.target.files ?? []))
    event.target.value = ''
  }

  function handleComposerPaste(event: ClipboardEvent<HTMLTextAreaElement>) {
    if (readOnlyRef.current) return
    addDraftImages(imageFilesFromClipboard(event.clipboardData))
  }

  function handleComposerDrop(event: DragEvent<HTMLTextAreaElement>) {
    if (readOnlyRef.current) return
    const files = Array.from(event.dataTransfer.files)
    if (files.length === 0) return
    event.preventDefault()
    addDraftImages(files)
  }

  function removeDraftImage(id: number) {
    if (readOnlyRef.current || isUploadingImages) return
    removeDraftImageAttachment(id)
    setError('')
  }

  function abortRun() {
    if (readOnlyRef.current) return
    if (sendSocketCommand({ type: 'abort' })) {
      updateRunPhase('Stopping', 'abort', 'Stop requested.', Date.now())
    }
  }

  function inspectNow() {
    const sentAt = Date.now()
    if (sendSocketCommand({ type: 'get_state' })) markProbeSent(sentAt)
  }

  function selectModel(identifier: string) {
    if (readOnlyRef.current) return
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
    if (readOnlyRef.current || !piThinkingLevelIds.some((candidate) => candidate === level)) return
    if (!sendSocketCommand({ type: 'set_thinking_level', level })) return
    setSelectedThinking(level)
    setNotice(`Setting Pi thinking to ${level}…`)
  }

  function applyComposerSuggestion(suggestion: ComposerSuggestion) {
    if (readOnlyRef.current) return
    setDraft(suggestion.completion)
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
    if (readOnlyRef.current || event.nativeEvent.isComposing) return
    if (event.altKey && event.key.toLowerCase() === 'w' && workflowsEnabled && workflowKeywordTriggerEnabled && ULTRACODE_KEYWORD_PATTERN.test(draft)) {
      event.preventDefault()
      setWorkflowKeywordDismissed(true)
      setNotice('Ultracode keyword trigger dismissed for this prompt.')
      return
    }
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
  const canSend = !readOnly && connectionStatus === 'open' && hasDraftContent && !isUploadingImages
  const primaryActionIsStop = isStreaming && !hasDraftContent && !isUploadingImages
  const runElapsed = runStartedAt === null ? 0 : Math.max(0, clockNow - runStartedAt)
  const responseAge = lastPiResponseAt === null ? null : Math.max(0, clockNow - lastPiResponseAt)
  const workEventAge = latestWorkEvent === null ? null : Math.max(0, clockNow - latestWorkEvent.at)
  const rpcResponsive = connectionStatus === 'open'
    && responseAge !== null
    && responseAge <= PI_RESPONSE_STALE_AFTER_MS
  const responseOverdue = connectionStatus === 'open' && (
    responseAge !== null
      ? responseAge > PI_RESPONSE_STALE_AFTER_MS
      : connectedAt !== null && clockNow - connectedAt > PI_RESPONSE_STALE_AFTER_MS
  )
  const probePending = lastProbeSentAt !== null
    && clockNow - lastProbeSentAt <= PI_RESPONSE_STALE_AFTER_MS
  const rpcTone: PiStatusTone = rpcResponsive ? 'healthy' : responseOverdue ? 'warning' : 'idle'
  const monitorTone: PiStatusTone = connectionStatus === 'error' || connectionStatus === 'closed'
    ? 'error'
    : connectionStatus !== 'open' || responseOverdue
      ? 'warning'
      : 'healthy'
  const activityToggleLabel = isStreaming
    ? `${runPhase} · ${formatDuration(runElapsed)}`
    : connectionStatus === 'open' && rpcResponsive
      ? 'Activity - Idle'
      : 'Activity · check status'

  async function copyActivityDiagnostics() {
    const lines = [
      'Pi Native activity diagnostics',
      `Captured: ${new Date().toISOString()}`,
      `Transport: ${connectionStatus}`,
      `Agent: ${isStreaming ? `${runPhase} for ${formatDuration(runElapsed)}` : 'idle'}`,
      `Pi RPC: ${rpcResponseDescription(responseAge, connectedAt, clockNow)}`,
      `Last probe latency: ${lastProbeLatency === null ? 'unknown' : `${lastProbeLatency}ms`}`,
      `Last RPC event: ${latestRpcEvent ? `${latestRpcEvent.label} (${formatActivityAge(clockNow - latestRpcEvent.at)})` : 'none'}`,
      `Last work event: ${latestWorkEvent ? `${latestWorkEvent.label} (${formatActivityAge(clockNow - latestWorkEvent.at)})` : 'none'}`,
      `Run events observed: ${runEventCount}`,
      '',
      'Recent lifecycle events:',
      ...activityLog.map((entry) => `${new Date(entry.at).toISOString()}  ${entry.event}${entry.repeats > 1 ? ` ×${entry.repeats}` : ''}  ${entry.summary}`),
    ]
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable')
      await navigator.clipboard.writeText(lines.join('\n'))
      setNotice('Copied Pi activity diagnostics.')
    } catch {
      setError('Could not copy Pi activity diagnostics.')
    }
  }

  const activityPanel = activityExpanded ? (
    <PiNativeActivityPanel
      probePending={probePending}
      probeDisabled={connectionStatus !== 'open' || probePending}
      metrics={[
        {
          label: 'Transport',
          tone: connectionStatus === 'open' ? 'healthy' : monitorTone,
          value: connectionDescription(connectionStatus),
        },
        {
          label: 'Pi RPC',
          tone: rpcTone,
          value: rpcResponseDescription(responseAge, connectedAt, clockNow),
          detail: lastProbeLatency !== null ? `${lastProbeLatency}ms round trip` : undefined,
        },
        {
          label: 'Agent',
          tone: isStreaming ? 'working' : 'idle',
          value: isStreaming ? `${runPhase} · ${formatDuration(runElapsed)}` : 'Idle',
          detail: isStreaming ? `${formatCount(runEventCount)} work events observed` : undefined,
        },
        {
          label: 'Last work event',
          tone: workEventAge !== null && workEventAge < PI_RESPONSE_STALE_AFTER_MS
            ? 'working'
            : 'idle',
          value: latestWorkEvent ? <code>{latestWorkEvent.label}</code> : 'No agent events observed yet',
          detail: latestWorkEvent ? formatActivityAge(workEventAge ?? 0) : undefined,
        },
      ]}
      sessionUsage={<PiSessionUsage stats={sessionStats} latestCacheHitRate={latestCacheHitRate} />}
      activityLog={activityLog.map((entry) => ({
        ...entry,
        clock: formatActivityClock(entry.at),
      }))}
      onInspect={inspectNow}
      onCopy={() => void copyActivityDiagnostics()}
      onHide={() => setActivityExpanded(false)}
    />
  ) : null
  const composerModelOptions = availableModels.flatMap((model) => {
    const identifier = modelIdentifier(model)
    return identifier ? [{ value: identifier, label: model.name || identifier }] : []
  })
  const composerHint = readOnly
    ? 'Read-only · managed by parent thread'
    : isUploadingImages
      ? `Uploading ${draftImages.length} image${draftImages.length === 1 ? '' : 's'}…`
      : connectionStatus !== 'open'
        ? 'Connecting to Pi…'
        : workflowsEnabled && workflowKeywordTriggerEnabled && ULTRACODE_KEYWORD_PATTERN.test(draft)
          ? workflowKeywordDismissed
            ? 'Ultracode keyword trigger dismissed for this prompt'
            : 'Ultracode workflow trigger · Option/Alt+W to dismiss'
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
              <h2 className={piNativeStyles.emptyTitle}>
                {readOnly ? 'Subagent conversation' : 'Start a conversation with Pi'}
              </h2>
              <p className={piNativeStyles.emptyCopy}>
                {readOnly
                  ? 'This delegated run is controlled by its parent thread. Pi’s turns and tool activity will appear here.'
                  : 'Send a prompt below. Pi’s turns and tool activity will appear here.'}
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
        readOnly={readOnly}
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
        onDraftChange={(value) => {
          setDraft(value)
          if (!ULTRACODE_KEYWORD_PATTERN.test(value)) {
            if (workflowKeywordDismissed) setNotice('')
            setWorkflowKeywordDismissed(false)
          }
          setSlashMenuDismissed(false)
          setSelectedSlashIndex(0)
        }}
        onPaste={handleComposerPaste}
        onDrop={handleComposerDrop}
        onKeyDown={handleComposerKeyDown}
        model={selectedModel}
        modelOptions={composerModelOptions}
        modelDisabled={readOnly || connectionStatus !== 'open' || isStreaming || isUploadingImages}
        onModelChange={selectModel}
        thinking={selectedThinking}
        thinkingOptions={piThinkingLevelIds.map((level) => ({ value: level, label: level }))}
        thinkingDisabled={readOnly || connectionStatus !== 'open' || isStreaming || isUploadingImages}
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

function connectionDescription(status: ConnectionStatus): string {
  switch (status) {
    case 'open': return 'WebSocket connected'
    case 'connecting': return 'WebSocket reconnecting'
    case 'error': return 'WebSocket error'
    case 'closed': return 'WebSocket closed'
  }
}

function rpcResponseDescription(responseAge: number | null, connectedAt: number | null, now: number): string {
  if (responseAge !== null) {
    return responseAge <= PI_RESPONSE_STALE_AFTER_MS
      ? `Responded ${formatActivityAge(responseAge)}`
      : `Last response ${formatActivityAge(responseAge)}`
  }
  if (connectedAt === null) return 'Waiting for a connection'
  const wait = Math.max(0, now - connectedAt)
  return wait <= PI_RESPONSE_STALE_AFTER_MS
    ? 'Waiting for the first Pi response'
    : `No Pi response for ${formatDuration(wait)}`
}

function formatActivityAge(ageMs: number): string {
  const safeAge = Math.max(0, ageMs)
  if (safeAge < 1_500) return 'just now'
  return `${formatDuration(safeAge)} ago`
}

function formatActivityClock(timestamp: number): string {
  return new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(timestamp)
}

function piRpcEventLabel(event: PiRpcEvent): string {
  const type = event.type || 'unknown'
  if (type === 'response') return `${type} · ${event.command || 'unknown'}`
  if (type === 'message_update' && event.assistantMessageEvent?.type) {
    return `${type} · ${event.assistantMessageEvent.type}`
  }
  if (type.startsWith('tool_execution_') && event.toolName) return `${type} · ${event.toolName}`
  return type
}

function isPiWorkEvent(event: PiRpcEvent): boolean {
  const type = event.type
  return Boolean(
    type
    && type !== 'response'
    && type !== 'extension_ui_request'
    && !type.startsWith('pi_native_'),
  )
}

function assistantMessagePhase(message: PiAgentMessage): string | null {
  const blocks = contentBlocks(message.content)
  if (blocks.some((block) => block.type === 'text' && Boolean(block.text?.trim()))) return 'Writing response'
  if (blocks.some((block) => block.type === 'toolCall')) return 'Preparing tool call'
  if (blocks.some((block) => block.type === 'thinking')) return 'Thinking'
  return message.role === 'assistant' ? 'Receiving model output' : null
}

function assistantUpdatePhase(event: PiRpcEvent): string | null {
  const updateType = event.assistantMessageEvent?.type || ''
  if (updateType.startsWith('thinking_')) return 'Thinking'
  if (updateType.startsWith('text_')) return 'Writing response'
  if (updateType.startsWith('toolcall_')) return 'Preparing tool call'
  if (updateType === 'start') return 'Receiving model output'
  if (updateType === 'done') return 'Processing model response'
  return event.message && typeof event.message === 'object'
    ? assistantMessagePhase(event.message)
    : null
}

function normalizePiCommands(commands: PiSlashCommand[]): PiSlashCommand[] {
  const nativeNames = new Set(NATIVE_SLASH_COMMANDS.map((command) => command.name))
  const seen = new Set<string>()
  const normalized: PiSlashCommand[] = []
  for (const command of commands) {
    const name = typeof command?.name === 'string' ? command.name.trim() : ''
    if (!name || name.includes('/') || /\s/.test(name) || nativeNames.has(name) || seen.has(name)) continue
    const source = command.source
    if (source !== 'extension' && source !== 'prompt' && source !== 'skill') continue
    seen.add(name)
    normalized.push({
      name,
      source,
      ...(typeof command.description === 'string' && command.description.trim()
        ? { description: command.description.trim() }
        : {}),
    })
  }
  return normalized
}

function normalizePiModels(models: PiModel[]): PiModel[] {
  const seen = new Set<string>()
  const normalized: PiModel[] = []
  for (const model of models) {
    const provider = typeof model?.provider === 'string' ? model.provider.trim() : ''
    const id = typeof model?.id === 'string' ? model.id.trim() : ''
    const identifier = provider && id ? `${provider}/${id}` : ''
    if (!identifier || seen.has(identifier)) continue
    seen.add(identifier)
    normalized.push({
      provider,
      id,
      ...(typeof model.name === 'string' && model.name.trim() ? { name: model.name.trim() } : {}),
    })
  }
  return normalized
}

function buildComposerSuggestions(
  draft: string,
  piCommands: PiSlashCommand[],
  models: PiModel[],
): ComposerSuggestion[] {
  const commandMatch = draft.match(/^\/([^\s]*)$/)
  if (commandMatch) {
    const query = (commandMatch[1] ?? '').toLowerCase()
    return [...NATIVE_SLASH_COMMANDS, ...piCommands]
      .filter((command) => command.name.toLowerCase().startsWith(query))
      .slice(0, 12)
      .map((command, index) => ({
        id: suggestionID('command', command.source, command.name, String(index)),
        label: `/${command.name}`,
        description: command.description || `${piSlashSourceLabel(command.source)} command`,
        source: command.source,
        completion: command.name === 'model' || command.name === 'thinking'
          ? `/${command.name} `
          : `/${command.name}`,
      }))
  }

  const modelMatch = draft.match(/^\/model\s+([^\s]*)$/)
  if (modelMatch) {
    const query = (modelMatch[1] ?? '').toLowerCase()
    return models
      .map((model) => ({ model, identifier: modelIdentifier(model) }))
      .filter((candidate): candidate is { model: PiModel; identifier: string } => Boolean(candidate.identifier))
      .filter(({ identifier, model }) => (
        identifier.toLowerCase().includes(query)
        || (model.name ?? '').toLowerCase().includes(query)
      ))
      .slice(0, 12)
      .map(({ identifier, model }, index) => ({
        id: suggestionID('model', identifier, String(index)),
        label: `/model ${identifier}`,
        description: model.name || `Model from ${model.provider}`,
        source: 'model',
        completion: `/model ${identifier}`,
      }))
  }

  const thinkingMatch = draft.match(/^\/thinking\s+([^\s]*)$/)
  if (thinkingMatch) {
    const query = (thinkingMatch[1] ?? '').toLowerCase()
    return piThinkingLevelIds
      .filter((level) => level.startsWith(query))
      .map((level, index) => ({
        id: suggestionID('level', level, String(index)),
        label: `/thinking ${level}`,
        description: `Use ${level} reasoning effort`,
        source: 'level',
        completion: `/thinking ${level}`,
      }))
  }

  return []
}

function suggestionID(...parts: string[]): string {
  return parts.join('-').replace(/[^a-z0-9_-]/gi, '-')
}

function modelIdentifier(model: PiModel | undefined): string {
  const provider = typeof model?.provider === 'string' ? model.provider.trim() : ''
  const id = typeof model?.id === 'string' ? model.id.trim() : ''
  return provider && id ? `${provider}/${id}` : ''
}

function nativeContextStatus(usage: PiContextUsage | undefined, model: string): AgentContextStatus | null {
  if (!usage || !Number.isFinite(usage.contextWindow) || usage.contextWindow <= 0) return null
  const hasKnownUsage = Number.isFinite(usage.tokens) && Number.isFinite(usage.percent)
  return {
    source: 'pi-native',
    tokens: hasKnownUsage ? usage.tokens : null,
    contextWindow: usage.contextWindow,
    percent: hasKnownUsage ? usage.percent : null,
    ...(model ? { model } : {}),
    updatedAt: new Date().toISOString(),
  }
}

function formatSessionStats(stats: PiSessionStats | undefined): string {
  if (!stats) return 'Pi session totals loaded.'
  const parts: string[] = []
  if (typeof stats.totalMessages === 'number') parts.push(`${formatCount(stats.totalMessages)} messages`)
  if (typeof stats.toolCalls === 'number') parts.push(`${formatCount(stats.toolCalls)} tool calls`)
  if (stats.tokens) {
    const usage = piSessionUsage(stats)
    parts.push(
      `↑${formatPiTokens(usage.input)}`,
      `↓${formatPiTokens(usage.output)}`,
      `R${formatPiTokens(usage.cacheRead)}`,
      `W${formatPiTokens(usage.cacheWrite)}`,
    )
  }
  if (typeof stats.cost === 'number') parts.push(formatPiCost(stats.cost))
  return parts.length > 0 ? `Session · ${parts.join(' · ')}` : 'Pi session totals loaded.'
}

function PiSessionUsage({
  stats,
  latestCacheHitRate,
}: {
  stats: PiSessionStats | undefined
  latestCacheHitRate: number | undefined
}) {
  if (!stats?.tokens) {
    return (
      <div
        className={classNames(piNativeStyles.sessionUsage, piNativeStyles.sessionUsageLoading)}
        role="group"
        aria-label="Loading Pi session token usage and cost"
        data-testid="pi-native-session-usage"
      >
        <span aria-hidden="true">Session usage · loading…</span>
      </div>
    )
  }

  const usage = piSessionUsage(stats)
  const showCacheHitRate = (usage.cacheRead > 0 || usage.cacheWrite > 0)
    && latestCacheHitRate !== undefined
  const cost = piUsageValue(stats.cost)
  const accessibleSummary = [
    `${formatCount(usage.input)} input tokens`,
    `${formatCount(usage.output)} output tokens`,
    `${formatCount(usage.cacheRead)} cache-read tokens`,
    `${formatCount(usage.cacheWrite)} cache-write tokens`,
    ...(showCacheHitRate ? [`${latestCacheHitRate.toFixed(1)} percent latest cache hit rate`] : []),
    `${formatPiCost(cost)} cost`,
  ].join(', ')

  return (
    <div
      className={piNativeStyles.sessionUsage}
      role="group"
      aria-label={`Pi session usage: ${accessibleSummary}`}
      data-testid="pi-native-session-usage"
    >
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(usage.input)} input tokens`} aria-hidden="true">
        <b>↑</b>{formatPiTokens(usage.input)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(usage.output)} output tokens`} aria-hidden="true">
        <b>↓</b>{formatPiTokens(usage.output)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(usage.cacheRead)} cache-read tokens`} aria-hidden="true">
        <b>R</b>{formatPiTokens(usage.cacheRead)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(usage.cacheWrite)} cache-write tokens`} aria-hidden="true">
        <b>W</b>{formatPiTokens(usage.cacheWrite)}
      </span>
      {showCacheHitRate && (
        <span className={piNativeStyles.sessionUsageMetric} title="Latest cache hit rate" aria-hidden="true">
          <b>CH</b>{latestCacheHitRate.toFixed(1)}%
        </span>
      )}
      <span className={piNativeStyles.sessionUsageCost} title="Cumulative session cost" aria-hidden="true">
        {formatPiCost(cost)}
      </span>
    </div>
  )
}

function piSessionUsage(stats: PiSessionStats): {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
} {
  return {
    input: piUsageValue(stats.tokens?.input),
    output: piUsageValue(stats.tokens?.output),
    cacheRead: piUsageValue(stats.tokens?.cacheRead),
    cacheWrite: piUsageValue(stats.tokens?.cacheWrite),
  }
}

function piLatestCacheHitRate(messages: PiAgentMessage[]): number | undefined {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index]
    if (message?.role !== 'assistant' || !message.usage) continue
    const input = piUsageValue(message.usage.input)
    const cacheRead = piUsageValue(message.usage.cacheRead)
    const cacheWrite = piUsageValue(message.usage.cacheWrite)
    const promptTokens = input + cacheRead + cacheWrite
    return promptTokens > 0 ? (cacheRead / promptTokens) * 100 : undefined
  }
  return undefined
}

function piUsageValue(value: number | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

// Keep the compact thresholds and precision aligned with Pi's terminal footer.
function formatPiTokens(value: number): string {
  if (value < 1_000) return value.toString()
  if (value < 10_000) return `${(value / 1_000).toFixed(1)}k`
  if (value < 1_000_000) return `${Math.round(value / 1_000)}k`
  if (value < 10_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  return `${Math.round(value / 1_000_000)}M`
}

function formatPiCost(value: number): string {
  return `$${piUsageValue(value).toFixed(3)}`
}

function formatCount(value: number): string {
  return new Intl.NumberFormat().format(value)
}

function upsertAgentMessage(messages: PiAgentMessage[], message: PiAgentMessage): PiAgentMessage[] {
  const timestamp = normalizedTimestamp(message.timestamp)
  const index = messages.findIndex((candidate) =>
    candidate.role === message.role
    && timestamp > 0
    && normalizedTimestamp(candidate.timestamp) === timestamp,
  )
  if (index < 0) return [...messages, message]
  const next = [...messages]
  next[index] = message
  return next
}

function buildTimeline(
  sourceMessages: PiAgentMessage[],
  liveAssistant: PiAgentMessage | null,
  liveTools: Map<string, PiToolState>,
  runActive: boolean,
): TimelineEntry[] {
  const messages = liveAssistant ? upsertAgentMessage(sourceMessages, liveAssistant) : sourceMessages
  const toolResults = new Map<string, PiAgentMessage>()
  for (const message of messages) {
    if (message.role === 'toolResult' && message.toolCallId) toolResults.set(message.toolCallId, message)
  }

  const entries: TimelineEntry[] = []
  const renderedTools = new Set<string>()
  messages.forEach((message, messageIndex) => {
    const timestamp = normalizedTimestamp(message.timestamp) || messageIndex + 1
    const keyBase = `${message.role || 'message'}:${timestamp}:${messageIndex}`
    if (message.role === 'user') {
      entries.push({
        kind: 'user',
        key: keyBase,
        text: contentText(message.content),
        images: contentImages(message.content),
        timestamp,
      })
      return
    }
    if (message.role === 'assistant') {
      const text = contentText(message.content, false)
      if (text.trim()) entries.push({ kind: 'assistant', key: `${keyBase}:text`, text, timestamp })
      for (const block of contentBlocks(message.content)) {
        if (block.type !== 'toolCall' || !block.id) continue
        const result = toolResults.get(block.id)
        const live = liveTools.get(block.id)
        renderedTools.add(block.id)
        entries.push({
          kind: 'tool',
          key: `${keyBase}:tool:${block.id}`,
          callId: block.id,
          name: block.name || live?.name || result?.toolName || 'tool',
          args: block.arguments ?? live?.args,
          output: result ? result.content : live?.output,
          status: result ? result.isError ? 'error' : 'success' : live?.status ?? 'running',
          timestamp: normalizedTimestamp(result?.timestamp) || live?.timestamp || timestamp,
        })
      }
      const diagnostic = runActive && message === liveAssistant ? null : assistantRunDiagnostic(message)
      if (diagnostic) {
        entries.push({
          kind: 'summary',
          key: `${keyBase}:diagnostic`,
          label: diagnostic.label,
          text: diagnostic.text,
          timestamp,
          tone: diagnostic.tone,
        })
      }
      return
    }
    if (message.role === 'branchSummary' || message.role === 'compactionSummary') {
      entries.push({
        kind: 'summary',
        key: keyBase,
        label: message.role === 'branchSummary'
          ? 'Branch summary'
          : 'Compaction summary · prior messages are display-only',
        text: message.summary ?? contentText(message.content),
        timestamp,
      })
      return
    }
    if (message.role === 'bashExecution') {
      const callId = `bash:${timestamp}:${messageIndex}`
      renderedTools.add(callId)
      entries.push({
        kind: 'tool',
        key: callId,
        callId,
        name: 'bash',
        args: { command: message.command },
        output: message.output,
        status: message.exitCode === 0 ? 'success' : 'error',
        timestamp,
      })
    }
  })

  for (const tool of liveTools.values()) {
    if (renderedTools.has(tool.callId)) continue
    entries.push({ kind: 'tool', key: `live-tool:${tool.callId}`, ...tool })
  }

  return addTurnMarkers(entries)
}

function addTurnMarkers(entries: TimelineEntry[]): TimelineEntry[] {
  const result: TimelineEntry[] = []
  for (let index = 0; index < entries.length;) {
    const entry = entries[index]
    if (!entry) {
      index += 1
      continue
    }
    if (entry.kind !== 'user' || entry.timestamp <= 0) {
      result.push(entry)
      index += 1
      continue
    }

    let end = entry.timestamp
    let next = index + 1
    for (; next < entries.length; next += 1) {
      const candidate = entries[next]
      if (candidate?.kind === 'user') break
      if (!candidate || candidate.kind === 'turn-marker') continue
      end = Math.max(end, candidate.timestamp)
    }
    result.push(...entries.slice(index, next))
    if (end - entry.timestamp >= 1_000) {
      result.push({
        kind: 'turn-marker',
        key: `turn:${entry.key}`,
        durationMs: end - entry.timestamp,
        timestamp: entry.timestamp,
      })
    }
    index = next
  }
  return result
}

function contentBlocks(content: PiAgentMessage['content']): PiContentBlock[] {
  return Array.isArray(content) ? content : []
}

function contentImages(content: PiAgentMessage['content']): PiRenderedImage[] {
  if (!Array.isArray(content)) return []
  return content.flatMap((block) => (
    block.type === 'image'
      && typeof block.data === 'string'
      && block.data.length > 0
      && typeof block.mimeType === 'string'
      && isSupportedPiImageType(block.mimeType)
      ? [{ data: block.data, mimeType: block.mimeType }]
      : []
  ))
}

function contentText(content: PiAgentMessage['content'], includeThinking = true): string {
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return ''
  return content.flatMap((block) => {
    if (block.type === 'text' && typeof block.text === 'string') return [block.text]
    if (includeThinking && block.type === 'thinking' && typeof block.thinking === 'string') return [block.thinking]
    return []
  }).join('\n\n')
}

function assistantRunDiagnostic(message: PiAgentMessage | null): PiRunDiagnostic | null {
  if (!message || message.role !== 'assistant') return null
  const stopReason = message.stopReason?.trim()
  const errorMessage = message.errorMessage?.trim()
  const blocks = contentBlocks(message.content)
  const hasVisibleResponse = Boolean(contentText(message.content, false).trim())
  const hasToolCall = blocks.some((block) => block.type === 'toolCall' && Boolean(block.id))

  if (stopReason === 'error' || (!stopReason && errorMessage)) {
    return {
      label: 'Provider error',
      text: errorMessage || 'The provider request failed before Pi could finish the turn.',
      tone: 'error',
    }
  }
  if (stopReason === 'length') {
    return {
      label: 'Output limit reached',
      text: errorMessage || 'The model response reached its output-token limit. Send “continue” to resume from the saved conversation.',
      tone: 'warning',
    }
  }
  if (stopReason === 'aborted') {
    return {
      label: 'Run stopped',
      text: errorMessage || 'The active run was stopped before Pi produced a final response.',
      tone: 'warning',
    }
  }
  if (stopReason === 'toolUse' && !hasToolCall) {
    return {
      label: 'Tool call missing',
      text: 'The model ended with a tool-use signal but did not return a usable tool call. Send a follow-up to continue.',
      tone: 'warning',
    }
  }
  if (stopReason === 'stop' && !hasVisibleResponse && !hasToolCall) {
    return {
      label: 'No final response',
      text: 'The model ended the turn without returning visible text or another tool call. Send a follow-up to continue.',
      tone: 'warning',
    }
  }
  return null
}

function normalizedTimestamp(value: PiAgentMessage['timestamp']): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string') {
    const parsed = Date.parse(value)
    return Number.isNaN(parsed) ? 0 : parsed
  }
  return 0
}
