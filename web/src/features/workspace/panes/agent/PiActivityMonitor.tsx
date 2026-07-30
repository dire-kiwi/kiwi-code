// Everything behind the composer's activity toggle: the four health metrics,
// the session usage strip, the lifecycle log, and the clipboard dump. Keeping
// the metric list and its matching diagnostics text in one file is the point --
// they have to agree, and they drifted easily when the pane owned both.
import {
  formatNativeActivityAge,
  formatNativeActivityClock,
  nativeConnectionDescription,
  nativeResponseDescription,
  NATIVE_AGENT_RESPONSE_STALE_AFTER_MS,
} from '@/lib/nativeAgentDiagnostics'
import { formatDuration } from '@/lib/formatDuration'
import type { NativeActivityRecord } from '@/lib/useNativeActivityLog'
import type { ConnectionStatus } from '@/types'
import { formatCount } from './piFormatting'
import { PiNativeActivityPanel } from './PiNativeActivityPanel'
import { PiSessionUsage } from './PiSessionUsage'
import type { PiActivityView } from './piActivity'
import type { PiEventStamp, PiSessionStats } from './piTypes'

export type PiActivityMonitorProps = {
  view: PiActivityView
  connectionStatus: ConnectionStatus
  isStreaming: boolean
  runPhase: string
  runEventCount: number
  connectedAt: number | null
  lastProbeLatency: number | null
  latestRpcEvent: PiEventStamp | null
  latestWorkEvent: PiEventStamp | null
  clockNow: number
  activityLog: NativeActivityRecord[]
  sessionStats: PiSessionStats | undefined
  latestCacheHitRate: number | undefined
  onInspect: () => void
  onHide: () => void
  onNotice: (message: string) => void
  onError: (message: string) => void
}

export function PiActivityMonitor({
  view,
  connectionStatus,
  isStreaming,
  runPhase,
  runEventCount,
  connectedAt,
  lastProbeLatency,
  latestRpcEvent,
  latestWorkEvent,
  clockNow,
  activityLog,
  sessionStats,
  latestCacheHitRate,
  onInspect,
  onHide,
  onNotice,
  onError,
}: PiActivityMonitorProps) {
  const { monitorTone, probePending, responseAge, rpcTone, runElapsed, workEventAge } = view

  async function copyDiagnostics() {
    const lines = [
      'Pi Native activity diagnostics',
      `Captured: ${new Date().toISOString()}`,
      `Transport: ${connectionStatus}`,
      `Agent: ${isStreaming ? `${runPhase} for ${formatDuration(runElapsed)}` : 'idle'}`,
      `Pi RPC: ${nativeResponseDescription('Pi', responseAge, connectedAt, clockNow)}`,
      `Last probe latency: ${lastProbeLatency === null ? 'unknown' : `${lastProbeLatency}ms`}`,
      `Last RPC event: ${latestRpcEvent ? `${latestRpcEvent.label} (${formatNativeActivityAge(clockNow - latestRpcEvent.at)})` : 'none'}`,
      `Last work event: ${latestWorkEvent ? `${latestWorkEvent.label} (${formatNativeActivityAge(clockNow - latestWorkEvent.at)})` : 'none'}`,
      `Run events observed: ${runEventCount}`,
      '',
      'Recent lifecycle events:',
      ...activityLog.map((entry) => `${new Date(entry.at).toISOString()}  ${entry.event}${entry.repeats > 1 ? ` ×${entry.repeats}` : ''}  ${entry.summary}`),
    ]
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable')
      await navigator.clipboard.writeText(lines.join('\n'))
      onNotice('Copied Pi activity diagnostics.')
    } catch {
      onError('Could not copy Pi activity diagnostics.')
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
          label: 'Pi RPC',
          tone: rpcTone,
          value: nativeResponseDescription('Pi', responseAge, connectedAt, clockNow),
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
      sessionUsage={<PiSessionUsage stats={sessionStats} latestCacheHitRate={latestCacheHitRate} />}
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
