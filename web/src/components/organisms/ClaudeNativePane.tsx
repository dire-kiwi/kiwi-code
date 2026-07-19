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
import { uploadPiImage } from '../../api'
import { apiWebSocketUrl } from '../../apiUrl'
import { claudeModelChoices, claudeThinkingLevelIds } from '../../codingAgents'
import { classNames } from '../../lib/classNames'
import { formatDuration } from '../../lib/formatDuration'
import {
  imageFilesFromClipboard,
  isSupportedPiImageType,
  piNativePromptImagePolicy,
} from '../../lib/promptImages'
import { readClaudeNativeDraft, writeClaudeNativeDraft } from '../../lib/promptDrafts'
import { useImageAttachments } from '../../lib/useImageAttachments'
import type { AgentContextStatus, ConnectionStatus } from '../../types'
import { piNativeStyles } from './piNativeStyles'
import {
  PiNativeActivityPanel,
  type PiStatusTone,
} from './PiNativeActivityPanel'
import { PiNativeComposer, type PiNativeComposerSuggestion } from './PiNativeComposer'
import {
  PiNativeTimelineEntry,
  type PiTimelineEntryValue as TimelineEntry,
} from './PiNativeTimeline'

type ClaudeNativePaneProps = {
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

type ClaudeContentBlock = {
  type?: string
  text?: string
  thinking?: string
  id?: string
  name?: string
  input?: unknown
  tool_use_id?: string
  content?: unknown
  is_error?: boolean
  source?: {
    type?: string
    media_type?: string
    data?: string
  }
}

type ClaudeUsage = {
  input_tokens?: number
  output_tokens?: number
  cache_creation_input_tokens?: number
  cache_read_input_tokens?: number
}

type ClaudeApiMessage = {
  id?: string
  role?: string
  content?: string | ClaudeContentBlock[]
  stop_reason?: string | null
  usage?: ClaudeUsage
}

type ClaudeStreamInnerEvent = {
  type?: string
  content_block?: { type?: string; name?: string }
  delta?: { type?: string; text?: string; thinking?: string }
}

type ClaudeEvent = {
  type?: string
  subtype?: string
  uuid?: string
  session_id?: string
  parent_tool_use_id?: string | null
  message?: ClaudeApiMessage
  event?: ClaudeStreamInnerEvent
  model?: string
  slash_commands?: unknown
  result?: string
  is_error?: boolean
  usage?: ClaudeUsage
  total_cost_usd?: number
  num_turns?: number
  // claude_native_* envelope fields from the Dire Mux bridge.
  isStreaming?: boolean
  sessionId?: string
  effort?: string
  events?: Array<{ at?: number; event?: ClaudeEvent }>
}

type ClaudeChatMessage = {
  key: string
  role: 'user' | 'assistant'
  at: number
  blocks: ClaudeContentBlock[]
  pending?: boolean
}

type ClaudeToolResult = {
  output: unknown
  isError: boolean
  at: number
}

type ClaudeRunSummary = {
  key: string
  at: number
  label: string
  text: string
  tone: 'warning' | 'error'
}

type ClaudeSessionStats = {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
  cost: number
  turns: number
}

type ClaudeActivityRecord = {
  id: number
  at: number
  event: string
  summary: string
  repeats: number
}

type ClaudeEventStamp = {
  at: number
  label: string
}

type ComposerSuggestion = PiNativeComposerSuggestion & { completion: string }

const RECONNECT_STABLE_AFTER_MS = 5_000
const CLAUDE_INSPECTION_INTERVAL_MS = 4_000
const CLAUDE_RESPONSE_STALE_AFTER_MS = 12_000
const CLAUDE_ACTIVITY_LOG_LIMIT = 24
const CLAUDE_PENDING_PROMPT_MATCH_MS = 30_000
const CLAUDE_DEFAULT_CONTEXT_WINDOW = 200_000

const NATIVE_SLASH_COMMANDS: Array<{ name: string; description: string }> = [
  {
    name: 'restart',
    description: 'Restart Claude Code and resume this saved conversation',
  },
  {
    name: 'new',
    description: 'Start a new saved Claude session in this thread',
  },
  {
    name: 'model',
    description: 'Switch the model for this Claude session',
  },
  {
    name: 'thinking',
    description: 'Set Claude’s reasoning effort',
  },
  {
    name: 'session',
    description: 'Show token, cache, turn, and cost totals',
  },
]

export function ClaudeNativePane({
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
}: ClaudeNativePaneProps) {
  const [messages, setMessages] = useState<ClaudeChatMessage[]>([])
  const [toolResults, setToolResults] = useState<Map<string, ClaudeToolResult>>(() => new Map())
  const [runSummaries, setRunSummaries] = useState<ClaudeRunSummary[]>([])
  const [liveText, setLiveText] = useState('')
  const [claudeCommands, setClaudeCommands] = useState<string[]>([])
  const [sessionStats, setSessionStats] = useState<ClaudeSessionStats | null>(null)
  const [selectedModel, setSelectedModel] = useState(initialModel ?? '')
  const [reportedModel, setReportedModel] = useState('')
  const [selectedThinking, setSelectedThinking] = useState(initialThinkingLevel ?? '')
  const [draft, setDraft] = useState(() => readClaudeNativeDraft(projectId, threadId))
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
  const [activityLog, setActivityLog] = useState<ClaudeActivityRecord[]>([])
  const [latestEvent, setLatestEvent] = useState<ClaudeEventStamp | null>(null)
  const [latestWorkEvent, setLatestWorkEvent] = useState<ClaudeEventStamp | null>(null)
  const [runPhase, setRunPhase] = useState('Idle')
  const [runStartedAt, setRunStartedAt] = useState<number | null>(null)
  const [runEventCount, setRunEventCount] = useState(0)
  const [connectedAt, setConnectedAt] = useState<number | null>(null)
  const [lastClaudeResponseAt, setLastClaudeResponseAt] = useState<number | null>(null)
  const [lastProbeLatency, setLastProbeLatency] = useState<number | null>(null)
  const [lastProbeSentAt, setLastProbeSentAt] = useState<number | null>(null)
  const [clockNow, setClockNow] = useState(() => Date.now())
  const [connectionAttempt, setConnectionAttempt] = useState(0)
  const socketRef = useRef<WebSocket | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const timelineRef = useRef<HTMLDivElement>(null)
  const activeRef = useRef(active)
  const atBottomRef = useRef(true)
  const reconnectAttemptsRef = useRef(0)
  const activitySequenceRef = useRef(0)
  const entrySequenceRef = useRef(0)
  const seenEventsRef = useRef<Set<string>>(new Set())
  const isStreamingRef = useRef(false)
  const runPhaseRef = useRef('Idle')
  const runStartedAtRef = useRef<number | null>(null)
  const promptSentAtRef = useRef<number | null>(null)
  const probeSentAtRef = useRef<number | null>(null)
  const liveTextRef = useRef('')
  const statsRef = useRef<ClaudeSessionStats>({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0, turns: 0 })
  const lastResultCostRef = useRef(0)
  const reportedModelRef = useRef('')
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
      }, ...current].slice(0, CLAUDE_ACTIVITY_LOG_LIMIT)
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
    liveTextRef.current = ''
    setLiveText('')
    if (wasStreaming && event && summary) appendActivity(event, summary, at)
  }, [appendActivity])

  const markProbeSent = useCallback((at = Date.now()) => {
    probeSentAtRef.current = at
    setLastProbeSentAt(at)
  }, [])

  const publishContextStatus = useCallback((usage: ClaudeUsage | undefined) => {
    const input = usageValue(usage?.input_tokens)
    const cacheRead = usageValue(usage?.cache_read_input_tokens)
    const cacheWrite = usageValue(usage?.cache_creation_input_tokens)
    const tokens = input + cacheRead + cacheWrite
    if (tokens <= 0) return
    const model = reportedModelRef.current
    onContextStatusChangeRef.current({
      source: 'claude-native',
      tokens,
      contextWindow: model.includes('[1m]') ? 1_000_000 : CLAUDE_DEFAULT_CONTEXT_WINDOW,
      percent: (tokens / (model.includes('[1m]') ? 1_000_000 : CLAUDE_DEFAULT_CONTEXT_WINDOW)) * 100,
      ...(model ? { model } : {}),
      updatedAt: new Date().toISOString(),
    })
  }, [])

  const recordResultUsage = useCallback((event: ClaudeEvent) => {
    const usage = event.usage
    if (!usage) return
    const stats = statsRef.current
    stats.input += usageValue(usage.input_tokens)
    stats.output += usageValue(usage.output_tokens)
    stats.cacheRead += usageValue(usage.cache_read_input_tokens)
    stats.cacheWrite += usageValue(usage.cache_creation_input_tokens)
    stats.turns += 1
    const reportedCost = usageValue(event.total_cost_usd)
    if (reportedCost >= lastResultCostRef.current) {
      stats.cost += reportedCost - lastResultCostRef.current
    } else {
      stats.cost += reportedCost
    }
    lastResultCostRef.current = reportedCost
    setSessionStats({ ...stats })
  }, [])

  const ingestConversationEvent = useCallback((event: ClaudeEvent, at: number) => {
    if (event.uuid) {
      if (seenEventsRef.current.has(event.uuid)) return
      seenEventsRef.current.add(event.uuid)
    }
    // Subagent traffic is summarized by its Task tool result instead.
    if (event.parent_tool_use_id) return

    switch (event.type) {
      case 'system': {
        if (event.subtype !== 'init') break
        if (typeof event.model === 'string' && event.model) {
          reportedModelRef.current = event.model
          setReportedModel(event.model)
        }
        setClaudeCommands(normalizeClaudeCommands(event.slash_commands))
        // A new process resets Claude's cumulative cost counter.
        lastResultCostRef.current = 0
        break
      }
      case 'user': {
        const blocks = contentBlocks(event.message?.content)
        const results = blocks.filter((block) => block.type === 'tool_result')
        if (results.length > 0) {
          setToolResults((current) => {
            const next = new Map(current)
            for (const block of results) {
              if (typeof block.tool_use_id !== 'string' || !block.tool_use_id) continue
              next.set(block.tool_use_id, {
                output: block.content,
                isError: Boolean(block.is_error),
                at,
              })
            }
            return next
          })
        }
        const visible = blocks.filter((block) => block.type === 'text' || block.type === 'image')
        if (visible.length === 0) break
        const text = blockText(visible)
        setMessages((current) => {
          const pendingIndex = current.findIndex((candidate) =>
            candidate.pending
            && candidate.role === 'user'
            && blockText(candidate.blocks) === text
            && at - candidate.at < CLAUDE_PENDING_PROMPT_MATCH_MS,
          )
          const message: ClaudeChatMessage = {
            key: event.uuid || `user:${entrySequenceRef.current += 1}`,
            role: 'user',
            at: pendingIndex >= 0 ? (current[pendingIndex]?.at ?? at) : at,
            blocks: visible,
          }
          if (pendingIndex < 0) return [...current, message]
          const next = [...current]
          next[pendingIndex] = message
          return next
        })
        break
      }
      case 'assistant': {
        const blocks = contentBlocks(event.message?.content)
        if (event.message?.usage) publishContextStatus(event.message.usage)
        if (blocks.length === 0) break
        const key = event.message?.id || event.uuid || `assistant:${entrySequenceRef.current += 1}`
        liveTextRef.current = ''
        setLiveText('')
        setMessages((current) => {
          const index = current.findIndex((candidate) => candidate.key === key)
          const message: ClaudeChatMessage = { key, role: 'assistant', at, blocks }
          if (index < 0) return [...current, message]
          const next = [...current]
          next[index] = { ...message, at: current[index]?.at ?? at }
          return next
        })
        break
      }
      case 'result': {
        recordResultUsage(event)
        if (event.is_error) {
          const text = typeof event.result === 'string' && event.result.trim()
            ? event.result.trim()
            : 'Claude could not finish the run.'
          setRunSummaries((current) => [...current, {
            key: event.uuid || `result:${entrySequenceRef.current += 1}`,
            at,
            label: 'Run failed',
            text,
            tone: 'error',
          }])
        }
        break
      }
    }
  }, [publishContextStatus, recordResultUsage])

  const resetConversation = useCallback(() => {
    seenEventsRef.current = new Set()
    statsRef.current = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0, turns: 0 }
    lastResultCostRef.current = 0
    liveTextRef.current = ''
    setMessages([])
    setToolResults(new Map())
    setRunSummaries([])
    setSessionStats(null)
    setLiveText('')
  }, [])

  const handleEvent = useCallback((event: ClaudeEvent, socket: WebSocket) => {
    const receivedAt = Date.now()
    const label = claudeEventLabel(event)
    setLatestEvent({ at: receivedAt, label })
    if (isClaudeWorkEvent(event)) {
      setLatestWorkEvent({ at: receivedAt, label })
      setRunEventCount((current) => current + 1)
    }

    switch (event.type) {
      case 'claude_native_ready': {
        updateConnectionStatus('open')
        setConnectedAt(receivedAt)
        setError('')
        appendActivity('claude_native_ready', 'Connected to the native Claude process.', receivedAt)
        markProbeSent(receivedAt)
        socket.send(JSON.stringify({ type: 'get_state' }))
        const prompt = initialPromptRef.current.trim()
        const imagePaths = initialImagePathsRef.current
        if ((prompt || imagePaths.length > 0) && !initialPromptSentRef.current) {
          promptSentAtRef.current = receivedAt
          beginRun('Sending prompt', receivedAt)
          appendActivity(
            'prompt',
            imagePaths.length > 0
              ? `Initial prompt sent to Claude with ${imagePaths.length} image${imagePaths.length === 1 ? '' : 's'}.`
              : 'Initial prompt sent to Claude.',
            receivedAt,
          )
          socket.send(JSON.stringify({
            type: 'prompt',
            message: prompt,
            ...(imagePaths.length > 0 ? { images: imagePaths.map((path) => ({ path })) } : {}),
          }))
          if (prompt) appendPendingUserMessage(setMessages, entrySequenceRef, prompt, receivedAt)
          initialPromptSentRef.current = true
          onInitialPromptSentRef.current?.()
        }
        if (activeRef.current) textareaRef.current?.focus()
        break
      }
      case 'claude_native_restarting':
        updateConnectionStatus('connecting')
        setError('')
        setNotice(claudeStatusMessage(event) || 'Restarting the Claude session…')
        appendActivity('claude_native_restarting', 'Restarting the native Claude process.', receivedAt)
        break
      case 'claude_native_reloaded':
        updateConnectionStatus('open')
        setConnectedAt(receivedAt)
        setLastClaudeResponseAt(null)
        setError('')
        setNotice(claudeStatusMessage(event) || 'Claude restarted.')
        appendActivity('claude_native_reloaded', 'Claude restarted.', receivedAt)
        markProbeSent(receivedAt)
        socket.send(JSON.stringify({ type: 'get_state' }))
        if (activeRef.current) textareaRef.current?.focus()
        break
      case 'claude_native_error':
        setError(claudeStatusMessage(event) || 'The native Claude session reported an error.')
        appendActivity('claude_native_error', 'Native Claude reported an error.', receivedAt)
        break
      case 'claude_native_fatal':
        setError(claudeStatusMessage(event) || 'The native Claude session cannot start.')
        appendActivity('claude_native_fatal', 'Native Claude reported a non-retryable startup error.', receivedAt)
        break
      case 'claude_native_exit':
        setError(claudeStatusMessage(event) || 'Claude exited.')
        appendActivity('claude_native_exit', 'The native Claude process ended.', receivedAt)
        finishRun(undefined, undefined, receivedAt)
        onContextStatusChangeRef.current(null)
        updateConnectionStatus('closed')
        break
      case 'claude_native_state': {
        const sentAt = probeSentAtRef.current
        setClockNow(receivedAt)
        setLastClaudeResponseAt(receivedAt)
        if (sentAt !== null) setLastProbeLatency(Math.max(0, receivedAt - sentAt))
        probeSentAtRef.current = null
        setLastProbeSentAt(null)
        if (typeof event.model === 'string' && event.model) {
          reportedModelRef.current = event.model
          setReportedModel(event.model)
        }
        if (event.isStreaming === true) {
          promptSentAtRef.current = null
          beginRun(runPhaseRef.current === 'Idle' ? 'Working' : runPhaseRef.current, receivedAt)
        } else if (
          event.isStreaming === false
          && isStreamingRef.current
          && (promptSentAtRef.current === null || receivedAt - promptSentAtRef.current > 2_000)
        ) {
          finishRun('claude_native_state', 'Claude reports that the run is idle.', receivedAt)
        }
        break
      }
      case 'claude_native_history': {
        resetConversation()
        for (const entry of event.events ?? []) {
          if (!entry?.event || typeof entry.event !== 'object') continue
          ingestConversationEvent(entry.event, typeof entry.at === 'number' && entry.at > 0 ? entry.at : receivedAt)
        }
        break
      }
      case 'system':
        if (event.subtype === 'init') {
          appendActivity('system_init', 'Claude session initialized.', receivedAt)
        }
        ingestConversationEvent(event, receivedAt)
        break
      case 'assistant': {
        beginRun('Receiving model output', receivedAt)
        ingestConversationEvent(event, receivedAt)
        break
      }
      case 'user':
        ingestConversationEvent(event, receivedAt)
        break
      case 'result': {
        ingestConversationEvent(event, receivedAt)
        const startedAt = runStartedAtRef.current
        appendActivity(
          'result',
          startedAt === null
            ? 'Claude finished the run.'
            : `Claude finished after ${formatDuration(receivedAt - startedAt)}.`,
          receivedAt,
        )
        if (event.is_error) {
          setError(typeof event.result === 'string' && event.result.trim()
            ? event.result.trim()
            : 'Claude could not finish the run.')
        }
        finishRun(undefined, undefined, receivedAt)
        break
      }
      case 'stream_event': {
        const inner = event.event
        const innerType = inner?.type || ''
        if (innerType === 'message_start') beginRun('Waiting for model', receivedAt)
        if (innerType === 'content_block_start') {
          if (inner?.content_block?.type === 'tool_use') {
            beginRun(`Preparing ${inner.content_block.name || 'tool'} call`, receivedAt)
          } else if (inner?.content_block?.type === 'thinking') {
            beginRun('Thinking', receivedAt)
          }
        }
        if (innerType === 'content_block_delta') {
          const delta = inner?.delta
          if (delta?.type === 'thinking_delta') {
            beginRun('Thinking', receivedAt)
          } else if (delta?.type === 'text_delta' && typeof delta.text === 'string') {
            beginRun('Writing response', receivedAt)
            liveTextRef.current += delta.text
            setLiveText(liveTextRef.current)
          }
        }
        break
      }
    }
  }, [
    appendActivity,
    beginRun,
    finishRun,
    ingestConversationEvent,
    markProbeSent,
    resetConversation,
    updateConnectionStatus,
  ])

  useEffect(() => () => imageUploadControllerRef.current?.abort(), [])

  useEffect(() => {
    writeClaudeNativeDraft(projectId, threadId, draft)
  }, [draft, projectId, threadId])

  useEffect(() => {
    let disposed = false
    let reconnectTimer: ReturnType<typeof window.setTimeout> | undefined
    let stableTimer: ReturnType<typeof window.setTimeout> | undefined
    let inspectionTimer: ReturnType<typeof window.setInterval> | undefined
    let claudeReady = false
    let reconnectAllowed = true
    updateConnectionStatus('connecting')
    onContextStatusChangeRef.current(null)
    setConnectedAt(null)
    setLastClaudeResponseAt(null)
    setLastProbeLatency(null)
    setLastProbeSentAt(null)
    probeSentAtRef.current = null
    setError('')

    const params = new URLSearchParams()
    if (initialModelRef.current) params.set('model', initialModelRef.current)
    if (initialThinkingRef.current) params.set('thinking', initialThinkingRef.current)
    const url = apiWebSocketUrl(
      `/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(threadId)}/claude/native`,
    )
    url.search = params.toString()
    const socket = new WebSocket(url)
    socketRef.current = socket

    socket.addEventListener('open', () => {
      if (disposed) {
        socket.close(1000, 'Native Claude pane closed')
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
          || !claudeReady
          || socket.readyState !== WebSocket.OPEN
          || (pendingSince !== null && now - pendingSince <= CLAUDE_RESPONSE_STALE_AFTER_MS)
        ) return
        markProbeSent(now)
        socket.send(JSON.stringify({ type: 'get_state' }))
      }, CLAUDE_INSPECTION_INTERVAL_MS)
    })

    socket.addEventListener('message', (messageEvent) => {
      if (disposed || typeof messageEvent.data !== 'string') return
      try {
        const event = JSON.parse(messageEvent.data) as ClaudeEvent
        if (event.type === 'claude_native_ready') claudeReady = true
        if (event.type === 'claude_native_fatal') reconnectAllowed = false
        handleEvent(event, socket)
      } catch {
        setError('Claude sent an unreadable conversation update.')
      }
    })

    socket.addEventListener('error', () => {
      if (!disposed) {
        appendActivity('connection_error', 'The native Claude WebSocket reported an error.')
        onContextStatusChangeRef.current(null)
        updateConnectionStatus('error')
      }
    })

    socket.addEventListener('close', () => {
      claudeReady = false
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
      if (socket.readyState < WebSocket.CLOSING) socket.close(1000, 'Native Claude pane closed')
      if (socketRef.current === socket) socketRef.current = null
      onContextStatusChangeRef.current(null)
    }
  }, [appendActivity, connectionAttempt, handleEvent, markProbeSent, projectId, threadId, updateConnectionStatus])

  const timeline = useMemo(
    () => buildTimeline(messages, toolResults, runSummaries, liveText),
    [liveText, messages, runSummaries, toolResults],
  )
  const composerSuggestions = useMemo(
    () => buildComposerSuggestions(draft, claudeCommands),
    [claudeCommands, draft],
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

  function sendSocketCommand(command: Record<string, unknown> & { type: string }): boolean {
    const socket = socketRef.current
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      setError('Claude is still connecting.')
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
    setSelectedSlashIndex(0)
    atBottomRef.current = true
  }

  async function sendPrompt() {
    const message = draft.trim()
    const images = [...draftImages]
    if ((!message && images.length === 0) || promptSubmissionRef.current) return
    const socket = socketRef.current
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      setError('Claude is still connecting.')
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
      })) return

      const sentAt = Date.now()
      if (message) appendPendingUserMessage(setMessages, entrySequenceRef, message, sentAt)
      if (!wasStreaming) {
        promptSentAtRef.current = sentAt
        beginRun('Sending prompt', sentAt)
      }
      appendActivity(
        'prompt',
        `Prompt sent to Claude${uploads.length > 0 ? ` with ${uploads.length} image${uploads.length === 1 ? '' : 's'}` : ''}.`,
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
    const match = message.match(/^\/(restart|new|model|thinking|session)(?:\s+([\s\S]*))?$/)
    if (!match) return false

    const commandName = match[1]
    const argument = (match[2] ?? '').trim()
    if (draftImages.length > 0) {
      setError(`Remove image attachments before running /${commandName}.`)
      return true
    }
    if (isStreaming && commandName !== 'session') {
      setError(`Wait for Claude to finish before running /${commandName}.`)
      return true
    }

    if (commandName === 'restart') {
      if (argument) {
        setError('Use /restart without arguments.')
        return true
      }
      if (!sendSocketCommand({
        type: 'restart',
        ...(selectedModel ? { modelId: selectedModel } : {}),
        ...(claudeThinkingLevelIds.some((level) => level === selectedThinking)
          ? { level: selectedThinking }
          : {}),
      })) return true
      clearSubmittedDraft()
      setNotice('Restarting the Claude session…')
      return true
    }

    if (commandName === 'new') {
      if (argument) {
        setError('Use /new without arguments.')
        return true
      }
      if (!sendSocketCommand({ type: 'new_session' })) return true
      clearSubmittedDraft()
      setNotice('Starting a new Claude session…')
      return true
    }

    if (commandName === 'model') {
      if (!claudeModelChoices.some((choice) => choice.id === argument)) {
        setError(`Use /model <${claudeModelChoices.map((choice) => choice.id).join('|')}>.`)
        return true
      }
      if (!sendSocketCommand({ type: 'set_model', modelId: argument })) return true
      setSelectedModel(argument)
      clearSubmittedDraft()
      setNotice(`Switching Claude to ${argument}…`)
      return true
    }

    if (commandName === 'thinking') {
      if (!claudeThinkingLevelIds.some((level) => level === argument)) {
        setError(`Use /thinking <${claudeThinkingLevelIds.join('|')}>.`)
        return true
      }
      if (!sendSocketCommand({ type: 'set_thinking_level', level: argument })) return true
      setSelectedThinking(argument)
      clearSubmittedDraft()
      setNotice(`Setting Claude reasoning effort to ${argument}…`)
      return true
    }

    if (argument) {
      setError('Use /session without arguments.')
      return true
    }
    clearSubmittedDraft()
    setNotice(formatSessionStats(sessionStats))
    return true
  }

  function submitDraft() {
    const message = draft.trim()
    if ((!message && draftImages.length === 0) || (message && runNativeSlashCommand(message))) return
    void sendPrompt()
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
    if (!claudeModelChoices.some((choice) => choice.id === identifier)) return
    if (!sendSocketCommand({ type: 'set_model', modelId: identifier })) return
    setSelectedModel(identifier)
    setNotice(`Switching Claude to ${identifier}…`)
  }

  function selectThinking(level: string) {
    if (!claudeThinkingLevelIds.some((candidate) => candidate === level)) return
    if (!sendSocketCommand({ type: 'set_thinking_level', level })) return
    setSelectedThinking(level)
    setNotice(`Setting Claude reasoning effort to ${level}…`)
  }

  function applyComposerSuggestion(suggestion: ComposerSuggestion) {
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
  const runElapsed = runStartedAt === null ? 0 : Math.max(0, clockNow - runStartedAt)
  const responseAge = lastClaudeResponseAt === null ? null : Math.max(0, clockNow - lastClaudeResponseAt)
  const workEventAge = latestWorkEvent === null ? null : Math.max(0, clockNow - latestWorkEvent.at)
  const bridgeResponsive = connectionStatus === 'open'
    && responseAge !== null
    && responseAge <= CLAUDE_RESPONSE_STALE_AFTER_MS
  const responseOverdue = connectionStatus === 'open' && (
    responseAge !== null
      ? responseAge > CLAUDE_RESPONSE_STALE_AFTER_MS
      : connectedAt !== null && clockNow - connectedAt > CLAUDE_RESPONSE_STALE_AFTER_MS
  )
  const probePending = lastProbeSentAt !== null
    && clockNow - lastProbeSentAt <= CLAUDE_RESPONSE_STALE_AFTER_MS
  const bridgeTone: PiStatusTone = bridgeResponsive ? 'healthy' : responseOverdue ? 'warning' : 'idle'
  const monitorTone: PiStatusTone = connectionStatus === 'error' || connectionStatus === 'closed'
    ? 'error'
    : connectionStatus !== 'open' || responseOverdue
      ? 'warning'
      : 'healthy'
  const activityToggleLabel = isStreaming
    ? `${runPhase} · ${formatDuration(runElapsed)}`
    : connectionStatus === 'open' && bridgeResponsive
      ? 'Activity - Idle'
      : 'Activity · check status'

  async function copyActivityDiagnostics() {
    const lines = [
      'Claude Native activity diagnostics',
      `Captured: ${new Date().toISOString()}`,
      `Transport: ${connectionStatus}`,
      `Agent: ${isStreaming ? `${runPhase} for ${formatDuration(runElapsed)}` : 'idle'}`,
      `Claude bridge: ${bridgeResponseDescription(responseAge, connectedAt, clockNow)}`,
      `Last probe latency: ${lastProbeLatency === null ? 'unknown' : `${lastProbeLatency}ms`}`,
      `Last event: ${latestEvent ? `${latestEvent.label} (${formatActivityAge(clockNow - latestEvent.at)})` : 'none'}`,
      `Last work event: ${latestWorkEvent ? `${latestWorkEvent.label} (${formatActivityAge(clockNow - latestWorkEvent.at)})` : 'none'}`,
      `Run events observed: ${runEventCount}`,
      '',
      'Recent lifecycle events:',
      ...activityLog.map((entry) => `${new Date(entry.at).toISOString()}  ${entry.event}${entry.repeats > 1 ? ` ×${entry.repeats}` : ''}  ${entry.summary}`),
    ]
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable')
      await navigator.clipboard.writeText(lines.join('\n'))
      setNotice('Copied Claude activity diagnostics.')
    } catch {
      setError('Could not copy Claude activity diagnostics.')
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
          label: 'Claude bridge',
          tone: bridgeTone,
          value: bridgeResponseDescription(responseAge, connectedAt, clockNow),
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
          tone: workEventAge !== null && workEventAge < CLAUDE_RESPONSE_STALE_AFTER_MS
            ? 'working'
            : 'idle',
          value: latestWorkEvent ? <code>{latestWorkEvent.label}</code> : 'No agent events observed yet',
          detail: latestWorkEvent ? formatActivityAge(workEventAge ?? 0) : undefined,
        },
      ]}
      sessionUsage={<ClaudeSessionUsage stats={sessionStats} />}
      activityLog={activityLog.map((entry) => ({
        ...entry,
        clock: formatActivityClock(entry.at),
      }))}
      onInspect={inspectNow}
      onCopy={() => void copyActivityDiagnostics()}
      onHide={() => setActivityExpanded(false)}
    />
  ) : null
  // Show the model reported by Claude when the user has not chosen an alias.
  const composerModelValue = selectedModel || reportedModel
  const composerModelOptions = [
    ...claudeModelChoices.map((choice) => ({ value: choice.id, label: choice.label })),
    ...(composerModelValue && !claudeModelChoices.some((choice) => choice.id === composerModelValue)
      ? [{ value: composerModelValue, label: composerModelValue }]
      : []),
  ]
  const composerHint = isUploadingImages
    ? `Uploading ${draftImages.length} image${draftImages.length === 1 ? '' : 's'}…`
    : connectionStatus !== 'open'
      ? 'Connecting to Claude…'
      : isStreaming
        ? 'Enter to queue for the active run'
        : 'Enter to send · Shift+Enter for newline'

  return (
    <section
      role="tabpanel"
      aria-label={`${threadTitle} native Claude conversation`}
      aria-hidden={!active}
      className={classNames(
        piNativeStyles.pane,
        active ? piNativeStyles.paneActive : piNativeStyles.paneHidden,
      )}
    >
      <div className={piNativeStyles.timeline} ref={timelineRef} onScroll={handleTimelineScroll}>
        <div className={piNativeStyles.conversation} data-testid="claude-native-conversation">
          {timeline.length === 0 ? (
            <div className={piNativeStyles.empty} data-testid="claude-native-empty">
              <span className={piNativeStyles.emptyGlyph} aria-hidden="true"><Bot size={22} /></span>
              <h2 className={piNativeStyles.emptyTitle}>Start a conversation with Claude</h2>
              <p className={piNativeStyles.emptyCopy}>
                Send a prompt below. Claude’s turns and tool activity will appear here.
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
        agentName="Claude"
        readOnly={false}
        monitorTone={monitorTone}
        activityExpanded={activityExpanded}
        activityToggleLabel={activityToggleLabel}
        isStreaming={isStreaming}
        queuedMessages={[]}
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
          setSlashMenuDismissed(false)
          setSelectedSlashIndex(0)
        }}
        onPaste={handleComposerPaste}
        onDrop={handleComposerDrop}
        onKeyDown={handleComposerKeyDown}
        model={composerModelValue}
        modelOptions={composerModelOptions}
        modelDisabled={connectionStatus !== 'open' || isStreaming || isUploadingImages}
        onModelChange={selectModel}
        thinking={selectedThinking}
        thinkingOptions={claudeThinkingLevelIds.map((level) => ({
          value: level,
          label: level === 'ultracode' ? 'Ultracode (workflows)' : level,
        }))}
        thinkingDisabled={connectionStatus !== 'open' || isStreaming || isUploadingImages}
        onThinkingChange={selectThinking}
        onImageInput={handleImageInput}
        hint={composerHint}
        primaryActionIsStop={primaryActionIsStop}
        canSend={canSend}
        onPrimaryAction={() => (primaryActionIsStop ? abortRun() : submitDraft())}
      />
    </section>
  )
}

function appendPendingUserMessage(
  setMessages: (updater: (current: ClaudeChatMessage[]) => ClaudeChatMessage[]) => void,
  sequence: { current: number },
  text: string,
  at: number,
) {
  setMessages((current) => [...current, {
    key: `pending:${sequence.current += 1}`,
    role: 'user',
    at,
    blocks: [{ type: 'text', text }],
    pending: true,
  }])
}

function connectionDescription(status: ConnectionStatus): string {
  switch (status) {
    case 'open': return 'WebSocket connected'
    case 'connecting': return 'WebSocket reconnecting'
    case 'error': return 'WebSocket error'
    case 'closed': return 'WebSocket closed'
  }
}

function bridgeResponseDescription(responseAge: number | null, connectedAt: number | null, now: number): string {
  if (responseAge !== null) {
    return responseAge <= CLAUDE_RESPONSE_STALE_AFTER_MS
      ? `Responded ${formatActivityAge(responseAge)}`
      : `Last response ${formatActivityAge(responseAge)}`
  }
  if (connectedAt === null) return 'Waiting for a connection'
  const wait = Math.max(0, now - connectedAt)
  return wait <= CLAUDE_RESPONSE_STALE_AFTER_MS
    ? 'Waiting for the first bridge response'
    : `No bridge response for ${formatDuration(wait)}`
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

function claudeEventLabel(event: ClaudeEvent): string {
  const type = event.type || 'unknown'
  if (type === 'system' && event.subtype) return `${type} · ${event.subtype}`
  if (type === 'stream_event' && event.event?.type) return `${type} · ${event.event.type}`
  return type
}

function isClaudeWorkEvent(event: ClaudeEvent): boolean {
  switch (event.type) {
    case 'assistant':
    case 'user':
    case 'result':
    case 'stream_event':
      return true
    default:
      return false
  }
}

function claudeStatusMessage(event: ClaudeEvent): string {
  const message = (event as { message?: unknown }).message
  return typeof message === 'string' ? message : ''
}

function normalizeClaudeCommands(commands: unknown): string[] {
  if (!Array.isArray(commands)) return []
  const seen = new Set<string>()
  const normalized: string[] = []
  for (const command of commands) {
    const name = typeof command === 'string'
      ? command.trim()
      : typeof (command as { name?: unknown })?.name === 'string'
        ? ((command as { name: string }).name).trim()
        : ''
    if (!name || name.includes('/') || /\s/.test(name) || seen.has(name)) continue
    if (NATIVE_SLASH_COMMANDS.some((native) => native.name === name)) continue
    seen.add(name)
    normalized.push(name)
  }
  return normalized
}

function buildComposerSuggestions(draft: string, claudeCommands: string[]): ComposerSuggestion[] {
  const commandMatch = draft.match(/^\/([^\s]*)$/)
  if (commandMatch) {
    const query = (commandMatch[1] ?? '').toLowerCase()
    const native = NATIVE_SLASH_COMMANDS
      .filter((command) => command.name.toLowerCase().startsWith(query))
      .map((command, index) => ({
        id: suggestionID('native', command.name, String(index)),
        label: `/${command.name}`,
        description: command.description,
        source: 'native' as const,
        completion: command.name === 'model' || command.name === 'thinking'
          ? `/${command.name} `
          : `/${command.name}`,
      }))
    const claude = claudeCommands
      .filter((name) => name.toLowerCase().startsWith(query))
      .map((name, index) => ({
        id: suggestionID('claude', name, String(index)),
        label: `/${name}`,
        description: 'Claude Code command',
        source: 'skill' as const,
        completion: `/${name}`,
      }))
    return [...native, ...claude].slice(0, 12)
  }

  const modelMatch = draft.match(/^\/model\s+([^\s]*)$/)
  if (modelMatch) {
    const query = (modelMatch[1] ?? '').toLowerCase()
    return claudeModelChoices
      .filter((choice) => choice.id.toLowerCase().includes(query)
        || choice.label.toLowerCase().includes(query))
      .map((choice, index) => ({
        id: suggestionID('model', choice.id, String(index)),
        label: `/model ${choice.id}`,
        description: choice.label,
        source: 'model' as const,
        completion: `/model ${choice.id}`,
      }))
  }

  const thinkingMatch = draft.match(/^\/thinking\s+([^\s]*)$/)
  if (thinkingMatch) {
    const query = (thinkingMatch[1] ?? '').toLowerCase()
    return claudeThinkingLevelIds
      .filter((level) => level.startsWith(query))
      .map((level, index) => ({
        id: suggestionID('level', level, String(index)),
        label: `/thinking ${level}`,
        description: `Use ${level} reasoning effort`,
        source: 'level' as const,
        completion: `/thinking ${level}`,
      }))
  }

  return []
}

function suggestionID(...parts: string[]): string {
  return parts.join('-').replace(/[^a-z0-9_-]/gi, '-')
}

function formatSessionStats(stats: ClaudeSessionStats | null): string {
  if (!stats) return 'No Claude session totals yet.'
  return [
    'Session',
    `${formatCount(stats.turns)} turn${stats.turns === 1 ? '' : 's'}`,
    `↑${formatTokens(stats.input)}`,
    `↓${formatTokens(stats.output)}`,
    `R${formatTokens(stats.cacheRead)}`,
    `W${formatTokens(stats.cacheWrite)}`,
    formatCost(stats.cost),
  ].join(' · ')
}

function ClaudeSessionUsage({ stats }: { stats: ClaudeSessionStats | null }) {
  if (!stats) {
    return (
      <div
        className={classNames(piNativeStyles.sessionUsage, piNativeStyles.sessionUsageLoading)}
        role="group"
        aria-label="Waiting for Claude session token usage and cost"
        data-testid="claude-native-session-usage"
      >
        <span aria-hidden="true">Session usage · waiting for the first run…</span>
      </div>
    )
  }

  const accessibleSummary = [
    `${formatCount(stats.input)} input tokens`,
    `${formatCount(stats.output)} output tokens`,
    `${formatCount(stats.cacheRead)} cache-read tokens`,
    `${formatCount(stats.cacheWrite)} cache-write tokens`,
    `${formatCost(stats.cost)} cost`,
  ].join(', ')

  return (
    <div
      className={piNativeStyles.sessionUsage}
      role="group"
      aria-label={`Claude session usage: ${accessibleSummary}`}
      data-testid="claude-native-session-usage"
    >
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(stats.input)} input tokens`} aria-hidden="true">
        <b>↑</b>{formatTokens(stats.input)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(stats.output)} output tokens`} aria-hidden="true">
        <b>↓</b>{formatTokens(stats.output)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(stats.cacheRead)} cache-read tokens`} aria-hidden="true">
        <b>R</b>{formatTokens(stats.cacheRead)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(stats.cacheWrite)} cache-write tokens`} aria-hidden="true">
        <b>W</b>{formatTokens(stats.cacheWrite)}
      </span>
      <span className={piNativeStyles.sessionUsageCost} title="Cumulative session cost" aria-hidden="true">
        {formatCost(stats.cost)}
      </span>
    </div>
  )
}

function usageValue(value: number | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

// Keep the compact thresholds and precision aligned with the Pi Native pane.
function formatTokens(value: number): string {
  if (value < 1_000) return value.toString()
  if (value < 10_000) return `${(value / 1_000).toFixed(1)}k`
  if (value < 1_000_000) return `${Math.round(value / 1_000)}k`
  if (value < 10_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  return `${Math.round(value / 1_000_000)}M`
}

function formatCost(value: number): string {
  return `$${usageValue(value).toFixed(3)}`
}

function formatCount(value: number): string {
  return new Intl.NumberFormat().format(value)
}

function contentBlocks(content: ClaudeApiMessage['content']): ClaudeContentBlock[] {
  if (typeof content === 'string') {
    return content ? [{ type: 'text', text: content }] : []
  }
  return Array.isArray(content) ? content : []
}

function blockText(blocks: ClaudeContentBlock[]): string {
  return blocks
    .flatMap((block) => (block.type === 'text' && typeof block.text === 'string' ? [block.text] : []))
    .join('\n\n')
}

function blockImages(blocks: ClaudeContentBlock[]): Array<{ mimeType: string; data: string }> {
  return blocks.flatMap((block) => (
    block.type === 'image'
      && block.source?.type === 'base64'
      && typeof block.source.data === 'string'
      && block.source.data.length > 0
      && typeof block.source.media_type === 'string'
      && isSupportedPiImageType(block.source.media_type)
      ? [{ data: block.source.data, mimeType: block.source.media_type }]
      : []
  ))
}

function buildTimeline(
  messages: ClaudeChatMessage[],
  toolResults: Map<string, ClaudeToolResult>,
  runSummaries: ClaudeRunSummary[],
  liveText: string,
): TimelineEntry[] {
  const entries: TimelineEntry[] = []

  messages.forEach((message, messageIndex) => {
    const keyBase = `${message.key}:${messageIndex}`
    if (message.role === 'user') {
      entries.push({
        kind: 'user',
        key: keyBase,
        text: blockText(message.blocks),
        images: blockImages(message.blocks),
        timestamp: message.at,
      })
      return
    }
    const text = blockText(message.blocks)
    if (text.trim()) {
      entries.push({ kind: 'assistant', key: `${keyBase}:text`, text, timestamp: message.at })
    }
    for (const block of message.blocks) {
      if (block.type !== 'tool_use' || typeof block.id !== 'string' || !block.id) continue
      const result = toolResults.get(block.id)
      entries.push({
        kind: 'tool',
        key: `${keyBase}:tool:${block.id}`,
        callId: block.id,
        name: block.name || 'tool',
        args: block.input,
        output: result?.output,
        status: result ? (result.isError ? 'error' : 'success') : 'running',
        timestamp: result?.at ?? message.at,
      })
    }
  })

  for (const summary of runSummaries) {
    entries.push({
      kind: 'summary',
      key: summary.key,
      label: summary.label,
      text: summary.text,
      timestamp: summary.at,
      tone: summary.tone,
    })
  }

  entries.sort((left, right) => left.timestamp - right.timestamp)

  if (liveText.trim()) {
    entries.push({
      kind: 'assistant',
      key: 'live-assistant',
      text: liveText,
      timestamp: Number.MAX_SAFE_INTEGER,
    })
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
      if (!candidate || candidate.kind === 'turn-marker' || candidate.timestamp === Number.MAX_SAFE_INTEGER) continue
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
