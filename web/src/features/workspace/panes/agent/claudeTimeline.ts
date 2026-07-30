// Turns Claude's chat messages, tool results, and run summaries into the flat
// entry list the shared timeline renders. Pure: no React, no socket.
import { isSupportedPiImageType } from '@/lib/promptImages'
import type { PiTimelineEntryValue as TimelineEntry } from './PiNativeTimeline'
import type {
  ClaudeApiMessage,
  ClaudeChatMessage,
  ClaudeContentBlock,
  ClaudeRunSummary,
  ClaudeToolResult,
} from './claudeTypes'

export function appendPendingUserMessage(
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

export function contentBlocks(content: ClaudeApiMessage['content']): ClaudeContentBlock[] {
  if (typeof content === 'string') {
    return content ? [{ type: 'text', text: content }] : []
  }
  return Array.isArray(content) ? content : []
}

export function blockText(blocks: ClaudeContentBlock[]): string {
  return blocks
    .flatMap((block) => (block.type === 'text' && typeof block.text === 'string' ? [block.text] : []))
    .join('\n\n')
}

export function blockImages(blocks: ClaudeContentBlock[]): Array<{ mimeType: string; data: string }> {
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

export function buildTimeline(
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
