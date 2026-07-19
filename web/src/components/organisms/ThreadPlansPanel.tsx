import { CircleAlert, Download, Eye, FileText } from 'lucide-react'
import { threadPlanDownloadUrl } from '../../api'
import type { ThreadPlan } from '../../types'

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
      <p className="mt-2 text-[9px] leading-4 text-ghost-dim">
        Forked planning agents publish implementation briefs here for later download or execution.
      </p>

      {error && (
        <p className="mt-2 flex items-start gap-1.5 text-[9px] leading-4 text-ghost-bright-red" role="alert">
          <CircleAlert size={11} className="mt-0.5 shrink-0" />
          {error}
        </p>
      )}
      {!error && plans.length === 0 && (
        <p className="mt-2.5 rounded-lg border border-dashed border-ghost-border/55 px-3 py-2.5 text-[9px] leading-4 text-ghost-faint">
          No saved plans in this thread yet.
        </p>
      )}

      {plans.length > 0 && (
        <ul className="mt-2.5 space-y-1.5" aria-label="Saved thread plans">
          {plans.map((plan) => (
            <li key={plan.id} className="rounded-xl border border-ghost-border/55 bg-ghost-black/20 px-2.5 py-2.5">
              <div className="flex items-start gap-2.5">
                <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-ghost-cyan/10 text-ghost-cyan">
                  <FileText size={13} aria-hidden="true" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block break-words text-[10px] font-medium leading-4 text-ghost-white">
                    {plan.title}
                  </span>
                  <span className="mt-1 block font-mono text-[7px] leading-3 text-ghost-faint">
                    {createdLabel(plan.createdAt)} · {formatBytes(plan.sizeBytes)}
                  </span>
                  <span className="mt-0.5 block truncate font-mono text-[7px] text-ghost-dim" title={plan.id}>
                    {plan.id}
                  </span>
                </span>
              </div>
              <button
                type="button"
                onClick={() => onViewPlan(plan)}
                className="mt-2 flex h-7 w-full items-center justify-center gap-1.5 rounded-md border border-ghost-cyan/30 bg-ghost-cyan/[0.06] text-[9px] font-medium text-ghost-cyan transition hover:border-ghost-cyan/50 hover:bg-ghost-cyan/[0.11]"
                aria-label={`View plan ${plan.title} in the main pane`}
              >
                <Eye size={10} />
                View Plan
              </button>
              <a
                href={threadPlanDownloadUrl(projectId, plan.threadId, plan.id)}
                download
                className="mt-1.5 flex h-7 w-full items-center justify-center gap-1.5 rounded-md border border-ghost-border/70 text-[9px] font-medium text-ghost-muted transition hover:border-ghost-cyan/35 hover:bg-ghost-cyan/[0.07] hover:text-ghost-cyan"
                aria-label={`Download plan ${plan.title}`}
              >
                <Download size={10} />
                Download Markdown
              </a>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
