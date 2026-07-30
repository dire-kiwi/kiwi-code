// Reading intent out of a raw RPC event: what to call it in the activity log,
// whether it counts as work, and which run phase it implies.
import { contentBlocks } from './piTimeline'
import type { PiAgentMessage, PiRpcEvent } from './piTypes'

export function piRpcEventLabel(event: PiRpcEvent): string {
  const type = event.type || 'unknown'
  if (type === 'response') return `${type} · ${event.command || 'unknown'}`
  if (type === 'message_update' && event.assistantMessageEvent?.type) {
    return `${type} · ${event.assistantMessageEvent.type}`
  }
  if (type.startsWith('tool_execution_') && event.toolName) return `${type} · ${event.toolName}`
  return type
}

export function isPiWorkEvent(event: PiRpcEvent): boolean {
  const type = event.type
  return Boolean(
    type
    && type !== 'response'
    && type !== 'extension_ui_request'
    && !type.startsWith('pi_native_'),
  )
}

export function assistantMessagePhase(message: PiAgentMessage): string | null {
  const blocks = contentBlocks(message.content)
  if (blocks.some((block) => block.type === 'text' && Boolean(block.text?.trim()))) return 'Writing response'
  if (blocks.some((block) => block.type === 'toolCall')) return 'Preparing tool call'
  if (blocks.some((block) => block.type === 'thinking')) return 'Thinking'
  return message.role === 'assistant' ? 'Receiving model output' : null
}

export function assistantUpdatePhase(event: PiRpcEvent): string | null {
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
