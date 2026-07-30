// Turns the raw run and connection counters into the ages, tones, and labels
// the activity monitor shows. Pure, so the staleness rules can be reasoned
// about without a socket or a clock.
//
// Shared by both native panes: the Pi and Claude copies of this were identical
// expression for expression, differing only in whether the channel was called
// the RPC or the bridge.
import { formatDuration } from '@/lib/formatDuration'
import { NATIVE_AGENT_RESPONSE_STALE_AFTER_MS } from '@/lib/nativeAgentDiagnostics'
import type { ConnectionStatus } from '@/types'
import type { PiStatusTone } from './PiNativeActivityPanel'

/** Both panes stamp their events the same way. */
export type AgentEventStamp = {
  at: number
  label: string
}

export type AgentActivitySnapshot = {
  connectionStatus: ConnectionStatus
  isStreaming: boolean
  runPhase: string
  runStartedAt: number | null
  connectedAt: number | null
  lastResponseAt: number | null
  lastProbeSentAt: number | null
  latestWorkEvent: AgentEventStamp | null
  clockNow: number
}

export type AgentActivityView = {
  runElapsed: number
  responseAge: number | null
  workEventAge: number | null
  channelResponsive: boolean
  responseOverdue: boolean
  probePending: boolean
  channelTone: PiStatusTone
  monitorTone: PiStatusTone
  activityToggleLabel: string
}

export function deriveAgentActivity(snapshot: AgentActivitySnapshot): AgentActivityView {
  const {
    clockNow,
    connectedAt,
    connectionStatus,
    isStreaming,
    lastResponseAt,
    lastProbeSentAt,
    latestWorkEvent,
    runPhase,
    runStartedAt,
  } = snapshot

  const runElapsed = runStartedAt === null ? 0 : Math.max(0, clockNow - runStartedAt)
  const responseAge = lastResponseAt === null ? null : Math.max(0, clockNow - lastResponseAt)
  const workEventAge = latestWorkEvent === null ? null : Math.max(0, clockNow - latestWorkEvent.at)
  const channelResponsive = connectionStatus === 'open'
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
    channelResponsive,
    responseOverdue,
    probePending,
    channelTone: channelResponsive ? 'healthy' : responseOverdue ? 'warning' : 'idle',
    monitorTone: connectionStatus === 'error' || connectionStatus === 'closed'
      ? 'error'
      : connectionStatus !== 'open' || responseOverdue
        ? 'warning'
        : 'healthy',
    activityToggleLabel: isStreaming
      ? `${runPhase} · ${formatDuration(runElapsed)}`
      : connectionStatus === 'open' && channelResponsive
        ? 'Activity - Idle'
        : 'Activity · check status',
  }
}
