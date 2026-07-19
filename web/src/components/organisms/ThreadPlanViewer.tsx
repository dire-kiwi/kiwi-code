import { useEffect, useState } from 'react'
import { CircleAlert, Download, FileText, LoaderCircle, X } from 'lucide-react'
import { getThreadPlanMarkdown, threadPlanDownloadUrl } from '../../api'
import type { ThreadPlan } from '../../types'
import { AgentMarkdown } from '../molecules/AgentMarkdown'
import './thread-plan-viewer.css'

type ThreadPlanViewerProps = {
  projectId: string
  plan: ThreadPlan
  onClose: () => void
}

function createdLabel(createdAt: string) {
  const value = new Date(createdAt)
  return Number.isNaN(value.getTime()) ? 'Saved plan' : `Saved ${value.toLocaleString()}`
}

export function ThreadPlanViewer({ projectId, plan, onClose }: ThreadPlanViewerProps) {
  const [markdown, setMarkdown] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const controller = new AbortController()
    setMarkdown('')
    setError('')
    setLoading(true)
    void getThreadPlanMarkdown(projectId, plan.threadId, plan.id, controller.signal)
      .then(setMarkdown)
      .catch((reason: unknown) => {
        if (controller.signal.aborted) return
        setError(reason instanceof Error ? reason.message : 'Could not load the plan.')
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [plan.id, plan.threadId, projectId])

  return (
    <section
      className="absolute inset-0 z-10 flex min-h-0 flex-col bg-ghost-background"
      aria-label={`Plan: ${plan.title}`}
    >
      <header className="flex min-h-14 shrink-0 items-center gap-3 border-b border-ghost-border/70 bg-ghost-panel/75 px-4 lg:px-6">
        <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-ghost-cyan/10 text-ghost-cyan">
          <FileText size={15} aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-xs font-semibold text-ghost-bright-white" title={plan.title}>{plan.title}</h1>
          <p className="mt-0.5 truncate font-mono text-[8px] text-ghost-faint">
            {createdLabel(plan.createdAt)} · implementation plan
          </p>
        </div>
        <a
          href={threadPlanDownloadUrl(projectId, plan.threadId, plan.id)}
          download
          className="flex h-8 shrink-0 items-center gap-1.5 rounded-lg border border-ghost-border/70 px-2.5 text-[9px] font-medium text-ghost-muted transition hover:border-ghost-cyan/40 hover:bg-ghost-cyan/[0.07] hover:text-ghost-cyan"
          aria-label={`Download plan ${plan.title}`}
          title="Download Markdown"
        >
          <Download size={12} />
          <span className="hidden sm:inline">Download</span>
        </a>
        <button
          type="button"
          onClick={onClose}
          className="grid size-8 shrink-0 place-items-center rounded-lg text-ghost-dim transition hover:bg-ghost-raised hover:text-ghost-white"
          aria-label="Close plan viewer"
          title="Close plan"
        >
          <X size={15} />
        </button>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {loading && (
          <div className="grid h-full place-items-center px-6 text-center" role="status">
            <div>
              <LoaderCircle size={20} className="mx-auto animate-spin text-ghost-cyan" />
              <p className="mt-3 text-[11px] text-ghost-muted">Loading plan…</p>
            </div>
          </div>
        )}
        {!loading && error && (
          <div className="grid h-full place-items-center px-6 text-center" role="alert">
            <div className="max-w-sm">
              <CircleAlert size={22} className="mx-auto text-ghost-bright-red" />
              <p className="mt-3 text-xs font-medium text-ghost-bright-white">Could not open this plan</p>
              <p className="mt-1.5 text-[10px] leading-5 text-ghost-muted">{error}</p>
            </div>
          </div>
        )}
        {!loading && !error && (
          <article className="plan-markdown mx-auto w-full max-w-4xl px-6 py-8 text-[12px] leading-[1.75] text-ghost-white md:px-10 md:py-10">
            <AgentMarkdown text={markdown} />
          </article>
        )}
      </div>
    </section>
  )
}
