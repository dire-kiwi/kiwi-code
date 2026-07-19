import { CircleAlert, Download, FileText } from 'lucide-react'
import { threadPlanDownloadUrl } from '../../api'
import { formatWhen } from '../../lib/formatWhen'
import type { ThreadPlan } from '../../types'
import { Button } from '../atoms/Button'

const byteFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 })

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes < 0) return ''
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${byteFormatter.format(bytes / 1024)} KB`
  return `${byteFormatter.format(bytes / (1024 * 1024))} MB`
}

function createdLabel(createdAt: string) {
  const value = new Date(createdAt)
  return Number.isNaN(value.getTime()) ? 'Saved plan' : value.toLocaleString()
}

type ThreadPlansPanelProps = {
  projectId: string
  plans: ThreadPlan[]
  error: string
  onViewPlan: (plan: ThreadPlan) => void
}

export function ThreadPlansPanel({ projectId, plans, error, onViewPlan }: ThreadPlansPanelProps) {
  return (
    <section aria-labelledby="thread-plans-heading">
      <div className="flex items-center justify-between gap-2">
        <p
          id="thread-plans-heading"
          className="flex items-center gap-1.5 font-mono text-[8px] font-semibold uppercase tracking-[0.16em] text-ghost-faint"
          title="Forked planning agents publish implementation briefs here for later download or execution."
        >
          <FileText size={10} />
          Plans
        </p>
        {plans.length > 0 && (
          <span className="rounded-full border border-ghost-border/65 px-1.5 py-0.5 font-mono text-[8px] text-ghost-faint">
            {plans.length}
          </span>
        )}
      </div>

      {error && (
        <p className="mt-2 flex items-start gap-1.5 text-[9px] leading-4 text-ghost-bright-red" role="alert">
          <CircleAlert size={11} className="mt-0.5 shrink-0" />
          {error}
        </p>
      )}
      {!error && plans.length === 0 && (
        <p className="mt-2 px-2 text-[9px] leading-4 text-ghost-faint">
          No saved plans in this thread yet.
        </p>
      )}

      {plans.length > 0 && (
        <ul className="mt-1.5 space-y-0.5" aria-label="Saved thread plans">
          {plans.map((plan) => (
            <li key={plan.id} className="flex items-center gap-0.5">
              <Button
                type="button"
                onClick={() => onViewPlan(plan)}
                className="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1.5 text-left transition hover:bg-ghost-raised/55"
                aria-label={`View plan ${plan.title} in the main pane`}
                title={`${plan.title}\n${createdLabel(plan.createdAt)} · ${formatBytes(plan.sizeBytes)}`}
              >
                <FileText size={12} className="shrink-0 text-ghost-cyan" />
                <span className="min-w-0 flex-1 truncate text-[10px] font-medium text-ghost-white">{plan.title}</span>
                <span className="shrink-0 font-mono text-[8px] text-ghost-faint">{formatWhen(plan.createdAt)}</span>
              </Button>
              <a
                href={threadPlanDownloadUrl(projectId, plan.threadId, plan.id)}
                download
                className="grid size-6 shrink-0 place-items-center rounded-md text-ghost-faint transition hover:bg-ghost-raised hover:text-ghost-cyan"
                aria-label={`Download plan ${plan.title}`}
                title="Download Markdown"
              >
                <Download size={11} />
              </a>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
