import type { ConnectionStatus } from '@/types'
import { formatDuration } from './formatDuration'

export const NATIVE_AGENT_RESPONSE_STALE_AFTER_MS = 12_000

export function nativeConnectionDescription(status: ConnectionStatus): string {
  switch (status) {
    case 'open': return 'WebSocket connected'
    case 'connecting': return 'WebSocket reconnecting'
    case 'error': return 'WebSocket error'
    case 'closed': return 'WebSocket closed'
  }
}

export function nativeResponseDescription(
  responseName: string,
  responseAge: number | null,
  connectedAt: number | null,
  now: number,
): string {
  if (responseAge !== null) {
    return responseAge <= NATIVE_AGENT_RESPONSE_STALE_AFTER_MS
      ? `Responded ${formatNativeActivityAge(responseAge)}`
      : `Last response ${formatNativeActivityAge(responseAge)}`
  }
  if (connectedAt === null) return 'Waiting for a connection'
  const wait = Math.max(0, now - connectedAt)
  return wait <= NATIVE_AGENT_RESPONSE_STALE_AFTER_MS
    ? `Waiting for the first ${responseName} response`
    : `No ${responseName} response for ${formatDuration(wait)}`
}

export function formatNativeActivityAge(ageMs: number): string {
  const safeAge = Math.max(0, ageMs)
  return safeAge < 1_500 ? 'just now' : `${formatDuration(safeAge)} ago`
}

export function formatNativeActivityClock(timestamp: number): string {
  return new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(timestamp)
}
