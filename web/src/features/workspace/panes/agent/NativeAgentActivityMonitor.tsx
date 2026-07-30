// Everything behind the composer's activity toggle: the four health metrics,
// the session usage strip, the lifecycle log, and the clipboard dump.
//
// Keeping the metric list and its matching diagnostics text in one file is the
// point -- they restate the same four facts and have to agree, which was easy
// to miss with sixty lines of composer wiring between them.
//
// Shared by both native panes. Pi and Claude had structurally identical copies
// of this; everything that actually differed is in NativeAgentDescriptor.
import type { ReactNode } from 'react'
import { formatDuration } from '@/lib/formatDuration'
import {
  formatNativeActivityAge,
  formatNativeActivityClock,
  nativeConnectionDescription,
  nativeResponseDescription,
  NATIVE_AGENT_RESPONSE_STALE_AFTER_MS,
} from '@/lib/nativeAgentDiagnostics'
import type { NativeActivityRecord } from '@/lib/useNativeActivityLog'
import type { ConnectionStatus } from '@/types'
import type { AgentActivityView, AgentEventStamp } from './agentActivity'
import { formatCount } from './agentFormat'
import { PiNativeActivityPanel } from './PiNativeActivityPanel'

export type NativeAgentDescriptor = {
  /** Heading and clipboard wording: "Pi Native activity diagnostics". */
  name: string
  /** The metric row for the agent's channel: "Pi RPC" or "Claude bridge". */
  channelLabel: string
  /** Subject handed to nativeResponseDescription: "Pi" or "bridge". */
  responseSubject: string
}

export type NativeAgentActivityMonitorProps = {
  agent: NativeAgentDescriptor
  view: AgentActivityView
  connectionStatus: ConnectionStatus
  isStreaming: boolean
  runPhase: string
  runEventCount: number
  connectedAt: number | null
  lastProbeLatency: number | null
  latestEvent: AgentEventStamp | null
  latestWorkEvent: AgentEventStamp | null
  clockNow: number
  activityLog: NativeActivityRecord[]
  sessionUsage: ReactNode
  onInspect: () => void
  onHide: () => void
  onNotice: (message: string) => void
  onError: (message: string) => void
}

export function NativeAgentActivityMonitor({
  agent,
  view,
  connectionStatus,
  isStreaming,
  runPhase,
  runEventCount,
  connectedAt,
  lastProbeLatency,
  latestEvent,
  latestWorkEvent,
  clockNow,
  activityLog,
  sessionUsage,
  onInspect,
  onHide,
  onNotice,
  onError,
}: NativeAgentActivityMonitorProps) {
  const { channelTone, monitorTone, probePending, responseAge, runElapsed, workEventAge } = view
  const channelDescription = nativeResponseDescription(
    agent.responseSubject,
    responseAge,
    connectedAt,
    clockNow,
  )

  async function copyDiagnostics() {
    const lines = [
      `${agent.name} Native activity diagnostics`,
      `Captured: ${new Date().toISOString()}`,
      `Transport: ${connectionStatus}`,
      `Agent: ${isStreaming ? `${runPhase} for ${formatDuration(runElapsed)}` : 'idle'}`,
      `${agent.channelLabel}: ${channelDescription}`,
      `Last probe latency: ${lastProbeLatency === null ? 'unknown' : `${lastProbeLatency}ms`}`,
      `Last event: ${latestEvent ? `${latestEvent.label} (${formatNativeActivityAge(clockNow - latestEvent.at)})` : 'none'}`,
      `Last work event: ${latestWorkEvent ? `${latestWorkEvent.label} (${formatNativeActivityAge(clockNow - latestWorkEvent.at)})` : 'none'}`,
      `Run events observed: ${runEventCount}`,
      '',
      'Recent lifecycle events:',
      ...activityLog.map((entry) => `${new Date(entry.at).toISOString()}  ${entry.event}${entry.repeats > 1 ? ` ×${entry.repeats}` : ''}  ${entry.summary}`),
    ]
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable')
      await navigator.clipboard.writeText(lines.join('\n'))
      onNotice(`Copied ${agent.name} activity diagnostics.`)
    } catch {
      onError(`Could not copy ${agent.name} activity diagnostics.`)
    }
  }

  return (
    <PiNativeActivityPanel
      probePending={probePending}
      probeDisabled={connectionStatus !== 'open' || probePending}
      metrics={[
        {
          label: 'Transport',
          tone: connectionStatus === 'open' ? 'healthy' : monitorTone,
          value: nativeConnectionDescription(connectionStatus),
        },
        {
          label: agent.channelLabel,
          tone: channelTone,
          value: channelDescription,
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
          tone: workEventAge !== null && workEventAge < NATIVE_AGENT_RESPONSE_STALE_AFTER_MS
            ? 'working'
            : 'idle',
          value: latestWorkEvent ? <code>{latestWorkEvent.label}</code> : 'No agent events observed yet',
          detail: latestWorkEvent ? formatNativeActivityAge(workEventAge ?? 0) : undefined,
        },
      ]}
      sessionUsage={sessionUsage}
      activityLog={activityLog.map((entry) => ({
        ...entry,
        clock: formatNativeActivityClock(entry.at),
      }))}
      onInspect={onInspect}
      onCopy={() => void copyDiagnostics()}
      onHide={onHide}
    />
  )
}
