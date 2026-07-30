// Turns the raw run and connection counters into the ages, tones, and labels
// the activity monitor shows. Pure, so the staleness rules can be reasoned
// about without a socket or a clock.
import { formatDuration } from '@/lib/formatDuration'
import { NATIVE_AGENT_RESPONSE_STALE_AFTER_MS } from '@/lib/nativeAgentDiagnostics'
import type { ConnectionStatus } from '@/types'
import type { PiStatusTone } from './PiNativeActivityPanel'
import type { PiEventStamp } from './piTypes'

export type PiActivitySnapshot = {
  connectionStatus: ConnectionStatus
  isStreaming: boolean
  runPhase: string
  runStartedAt: number | null
  connectedAt: number | null
  lastPiResponseAt: number | null
  lastProbeSentAt: number | null
  latestWorkEvent: PiEventStamp | null
  clockNow: number
}

export type PiActivityView = {
  runElapsed: number
  responseAge: number | null
  workEventAge: number | null
  rpcResponsive: boolean
  responseOverdue: boolean
  probePending: boolean
  rpcTone: PiStatusTone
  monitorTone: PiStatusTone
  activityToggleLabel: string
}

export function derivePiActivity(snapshot: PiActivitySnapshot): PiActivityView {
  const {
    clockNow,
    connectedAt,
    connectionStatus,
    isStreaming,
    lastPiResponseAt,
    lastProbeSentAt,
    latestWorkEvent,
    runPhase,
    runStartedAt,
  } = snapshot

  const runElapsed = runStartedAt === null ? 0 : Math.max(0, clockNow - runStartedAt)
  const responseAge = lastPiResponseAt === null ? null : Math.max(0, clockNow - lastPiResponseAt)
  const workEventAge = latestWorkEvent === null ? null : Math.max(0, clockNow - latestWorkEvent.at)
  const rpcResponsive = connectionStatus === 'open'
    && responseAge !== null
    && responseAge <= NATIVE_AGENT_RESPONSE_STALE_AFTER_MS
  // With no response yet, fall back to how long the transport has been open --
  // a socket that connected and then said nothing is the case worth flagging.
  const responseOverdue = connectionStatus === 'open' && (
    responseAge !== null
      ? responseAge > NATIVE_AGENT_RESPONSE_STALE_AFTER_MS
      : connectedAt !== null && clockNow - connectedAt > NATIVE_AGENT_RESPONSE_STALE_AFTER_MS
  )
  const probePending = lastProbeSentAt !== null
    && clockNow - lastProbeSentAt <= NATIVE_AGENT_RESPONSE_STALE_AFTER_MS

  return {
    runElapsed,
    responseAge,
    workEventAge,
    rpcResponsive,
    responseOverdue,
    probePending,
    rpcTone: rpcResponsive ? 'healthy' : responseOverdue ? 'warning' : 'idle',
    monitorTone: connectionStatus === 'error' || connectionStatus === 'closed'
      ? 'error'
      : connectionStatus !== 'open' || responseOverdue
        ? 'warning'
        : 'healthy',
    activityToggleLabel: isStreaming
      ? `${runPhase} · ${formatDuration(runElapsed)}`
      : connectionStatus === 'open' && rpcResponsive
        ? 'Activity - Idle'
        : 'Activity · check status',
  }
}
