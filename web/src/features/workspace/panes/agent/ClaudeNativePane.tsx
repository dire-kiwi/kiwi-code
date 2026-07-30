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
import { claudeModelChoices, claudeThinkingLevelIds } from '@/codingAgents'
import { classNames } from '@/lib/classNames'
import { formatDuration } from '@/lib/formatDuration'
import { imageFilesFromClipboard, piNativePromptImagePolicy } from '@/lib/promptImages'
import { readClaudeNativeDraft, writeClaudeNativeDraft } from '@/lib/promptDrafts'
import { useImageAttachments } from '@/lib/useImageAttachments'
import { useNativeActivityLog } from '@/lib/useNativeActivityLog'
import { useNativeAgentSocket } from '@/lib/useNativeAgentSocket'
import type { AgentContextStatus, ConnectionStatus } from '@/types'
import { ClaudeSessionUsage } from './ClaudeSessionUsage'
import { NativeAgentActivityMonitor, type NativeAgentDescriptor } from './NativeAgentActivityMonitor'
import { PiNativeComposer } from './PiNativeComposer'
import { PiNativeTimelineEntry } from './PiNativeTimeline'
import { deriveAgentActivity } from './agentActivity'
import { piNativeStyles } from './piNativeStyles'
import { usageValue } from './agentFormat'
import {
  buildComposerSuggestions,
  normalizeClaudeCommands,
} from './claudeCommands'
import {
  claudeEventLabel,
  claudeStatusMessage,
  isClaudeWorkEvent,
} from './claudeEvents'
import { runClaudeSlashCommand } from './claudeSlashCommands'
import {
  appendPendingUserMessage,
  blockText,
  buildTimeline,
  contentBlocks,
} from './claudeTimeline'
import {
  CLAUDE_DEFAULT_CONTEXT_WINDOW,
  CLAUDE_PENDING_PROMPT_MATCH_MS,
  type ClaudeChatMessage,
  type ClaudeEvent,
  type ClaudeEventStamp,
  type ClaudeRunSummary,
  type ClaudeSessionStats,
  type ClaudeToolResult,
  type ClaudeUsage,
  type ComposerSuggestion,
} from './claudeTypes'

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

const CLAUDE_AGENT: NativeAgentDescriptor = {
  name: 'Claude',
  channelLabel: 'Claude bridge',
  responseSubject: 'bridge',
}

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
  const { activityLog, appendActivity } = useNativeActivityLog()
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
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const timelineRef = useRef<HTMLDivElement>(null)
  const activeRef = useRef(active)
  const atBottomRef = useRef(true)
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

  const nativeSocketUrl = useMemo(() => {
    const params = new URLSearchParams()
    if (initialModelRef.current) params.set('model', initialModelRef.current)
    if (initialThinkingRef.current) params.set('thinking', initialThinkingRef.current)
    const url = apiWebSocketUrl(
      `/api/projects/${encodeURIComponent(projectId)}/threads/${encodeURIComponent(threadId)}/claude/native`,
    )
    url.search = params.toString()
    return url.toString()
  }, [projectId, threadId])

  const resetConnectionDiagnostics = useCallback(() => {
    setConnectedAt(null)
    setLastClaudeResponseAt(null)
    setLastProbeLatency(null)
    setLastProbeSentAt(null)
    setError('')
  }, [])

  const { send: sendSocketCommand, socketRef } = useNativeAgentSocket<ClaudeEvent>({
    agentName: 'Claude',
    fatalEventType: 'claude_native_fatal',
    onActivity: appendActivity,
    onAttempt: resetConnectionDiagnostics,
    onContextReset: () => onContextStatusChangeRef.current(null),
    onError: setError,
    onEvent: handleEvent,
    onProbeSent: markProbeSent,
    onStatusChange: updateConnectionStatus,
    probeSentAtRef,
    readyEventType: 'claude_native_ready',
    url: nativeSocketUrl,
  })

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

  function handleSlashCommand(message: string): boolean {
    return runClaudeSlashCommand(message, {
      isStreaming,
      hasImageAttachments: draftImages.length > 0,
      selectedModel,
      selectedThinking,
      sessionStats,
      send: sendSocketCommand,
      setSelectedModel,
      setSelectedThinking,
      setError,
      setNotice,
      clearSubmittedDraft,
    })
  }

  function submitDraft() {
    const message = draft.trim()
    if ((!message && draftImages.length === 0) || (message && handleSlashCommand(message))) return
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
  const activity = deriveAgentActivity({
    clockNow,
    connectedAt,
    connectionStatus,
    isStreaming,
    lastResponseAt: lastClaudeResponseAt,
    lastProbeSentAt,
    latestWorkEvent,
    runPhase,
    runStartedAt,
  })
  const { activityToggleLabel, monitorTone } = activity
  const activityPanel = activityExpanded ? (
    <NativeAgentActivityMonitor
      agent={CLAUDE_AGENT}
      view={activity}
      connectionStatus={connectionStatus}
      isStreaming={isStreaming}
      runPhase={runPhase}
      runEventCount={runEventCount}
      connectedAt={connectedAt}
      lastProbeLatency={lastProbeLatency}
      latestEvent={latestEvent}
      latestWorkEvent={latestWorkEvent}
      clockNow={clockNow}
      activityLog={activityLog}
      sessionUsage={<ClaudeSessionUsage stats={sessionStats} />}
      onInspect={inspectNow}
      onHide={() => setActivityExpanded(false)}
      onNotice={setNotice}
      onError={setError}
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
          label: level === 'ultracode' ? 'Ultracode (Claude built-in)' : level,
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
