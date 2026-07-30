// Reading intent out of a raw Claude stream event.
import type { ClaudeEvent } from './claudeTypes'

export function claudeEventLabel(event: ClaudeEvent): string {
  const type = event.type || 'unknown'
  if (type === 'system' && event.subtype) return `${type} · ${event.subtype}`
  if (type === 'stream_event' && event.event?.type) return `${type} · ${event.event.type}`
  return type
}

export function isClaudeWorkEvent(event: ClaudeEvent): boolean {
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

export function claudeStatusMessage(event: ClaudeEvent): string {
  const message = (event as { message?: unknown }).message
  return typeof message === 'string' ? message : ''
}
