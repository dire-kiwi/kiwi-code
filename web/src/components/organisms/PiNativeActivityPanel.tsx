import type { ReactNode } from 'react'
import { Activity, Copy, RefreshCw } from 'lucide-react'
import { classNames } from '../../lib/classNames'
import {
  piNativeStatusDotToneStyles,
  piNativeStyles,
} from './piNativeStyles'

export type PiStatusTone = keyof typeof piNativeStatusDotToneStyles

export type PiActivityMetric = {
  label: string
  tone: PiStatusTone
  value: ReactNode
  detail?: ReactNode
}

export type PiActivityLogItem = {
  id: number
  clock: string
  event: string
  repeats: number
  summary: string
}

type PiNativeActivityPanelProps = {
  probePending: boolean
  probeDisabled: boolean
  metrics: PiActivityMetric[]
  sessionUsage: ReactNode
  activityLog: PiActivityLogItem[]
  onInspect: () => void
  onCopy: () => void
  onHide: () => void
}

export function PiNativeActivityPanel({
  probePending,
  probeDisabled,
  metrics,
  sessionUsage,
  activityLog,
  onInspect,
  onCopy,
  onHide,
}: PiNativeActivityPanelProps) {
  return (
    <section
      className={piNativeStyles.activityPanel}
      aria-label="Pi activity monitor"
      data-testid="pi-native-activity-panel"
    >
      <div className={piNativeStyles.activityHeader}>
        <div>
          <Activity size={14} />
          <span>
            <strong>Pi activity monitor</strong>
            <small>Session usage, transport, process response, and RPC lifecycle events</small>
          </span>
        </div>
        <div className={piNativeStyles.activityActions}>
          <button type="button" disabled={probeDisabled} onClick={onInspect}>
            <RefreshCw size={11} className={probePending ? piNativeStyles.spin : undefined} />
            {probePending ? 'Checking…' : 'Check now'}
          </button>
          <button type="button" onClick={onCopy}>
            <Copy size={11} /> Copy
          </button>
          <button type="button" onClick={onHide}>Hide</button>
        </div>
      </div>

      <dl className={piNativeStyles.activityGrid}>
        {metrics.map((metric) => (
          <div key={metric.label}>
            <dt>{metric.label}</dt>
            <dd>
              <StatusDot tone={metric.tone} />
              {metric.value}
              {metric.detail && <small>{metric.detail}</small>}
            </dd>
          </div>
        ))}
        <div className={piNativeStyles.activityGridUsage}>
          <dt>Session usage</dt>
          <dd>{sessionUsage}</dd>
        </div>
      </dl>

      <div className={piNativeStyles.activityLog}>
        <span>Recent lifecycle</span>
        {activityLog.length === 0 ? (
          <p>No lifecycle events observed yet.</p>
        ) : (
          <ol>
            {activityLog.map((entry) => (
              <li key={entry.id}>
                <time>{entry.clock}</time>
                <code>{entry.event}{entry.repeats > 1 ? ` ×${entry.repeats}` : ''}</code>
                <span>{entry.summary}</span>
              </li>
            ))}
          </ol>
        )}
      </div>
    </section>
  )
}

function StatusDot({ tone }: { tone: PiStatusTone }) {
  return (
    <span
      className={classNames(piNativeStyles.statusDot, piNativeStatusDotToneStyles[tone])}
      aria-hidden="true"
    />
  )
}
