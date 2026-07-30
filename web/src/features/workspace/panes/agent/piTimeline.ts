// Turns the saved conversation plus whatever is streaming right now into the
// flat entry list the timeline renders. Pure: no React, no socket.
import { isSupportedPiImageType } from '@/lib/promptImages'
import type { PiTimelineEntryValue as TimelineEntry } from './PiNativeTimeline'
import type {
  PiAgentMessage,
  PiContentBlock,
  PiRenderedImage,
  PiRunDiagnostic,
  PiToolState,
} from './piTypes'

export function upsertAgentMessage(
  messages: PiAgentMessage[],
  message: PiAgentMessage,
): PiAgentMessage[] {
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

export function buildTimeline(
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

export function addTurnMarkers(entries: TimelineEntry[]): TimelineEntry[] {
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

export function contentBlocks(content: PiAgentMessage['content']): PiContentBlock[] {
  return Array.isArray(content) ? content : []
}

export function contentImages(content: PiAgentMessage['content']): PiRenderedImage[] {
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

export function contentText(content: PiAgentMessage['content'], includeThinking = true): string {
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return ''
  return content.flatMap((block) => {
    if (block.type === 'text' && typeof block.text === 'string') return [block.text]
    if (includeThinking && block.type === 'thinking' && typeof block.thinking === 'string') return [block.thinking]
    return []
  }).join('\n\n')
}

export function assistantRunDiagnostic(message: PiAgentMessage | null): PiRunDiagnostic | null {
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

export function normalizedTimestamp(value: PiAgentMessage['timestamp']): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string') {
    const parsed = Date.parse(value)
    return Number.isNaN(parsed) ? 0 : parsed
  }
  return 0
}
