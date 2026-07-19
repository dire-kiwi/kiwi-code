import { Gauge } from 'lucide-react'
import type { AgentContextStatus } from '../../types'

const fullNumber = new Intl.NumberFormat()

function formatTokens(count: number) {
  if (count < 1_000) return String(Math.round(count))
  if (count < 10_000) return `${(count / 1_000).toFixed(1)}k`
  if (count < 1_000_000) return `${Math.round(count / 1_000)}k`
  if (count < 10_000_000) return `${(count / 1_000_000).toFixed(1)}M`
  return `${Math.round(count / 1_000_000)}M`
}

function statusTone(percent: number | null) {
  if (percent === null) {
    return {
      icon: 'text-ghost-faint',
      fill: 'bg-ghost-faint',
      value: 'text-ghost-faint',
    }
  }
  if (percent > 90) {
    return {
      icon: 'text-ghost-bright-red',
      fill: 'bg-ghost-bright-red',
      value: 'text-ghost-bright-red',
    }
  }
  if (percent > 70) {
    return {
      icon: 'text-ghost-yellow',
      fill: 'bg-ghost-yellow',
      value: 'text-ghost-yellow',
    }
  }
  return {
    icon: 'text-ghost-green',
    fill: 'bg-ghost-green',
    value: 'text-ghost-muted',
  }
}

function contextDescription(status: AgentContextStatus) {
  const model = status.model ? `${status.model} · ` : ''
  if (status.tokens === null || status.percent === null) {
    return `${model}Context usage is recalculating after compaction. Window: ${fullNumber.format(status.contextWindow)} tokens.`
  }
  return `${model}${status.percent.toFixed(1)}% of context used · ${fullNumber.format(status.tokens)} of ${fullNumber.format(status.contextWindow)} tokens.`
}

type ContextStatusProps = {
  status: AgentContextStatus
}

export function ContextStatus({ status }: ContextStatusProps) {
  const percent = status.percent === null ? null : Math.max(0, status.percent)
  const progress = percent === null ? 0 : Math.min(100, percent)
  const tone = statusTone(percent)
  const description = contextDescription(status)

  return (
    <span
      role="status"
      aria-live="polite"
      aria-label={description}
      title={description}
      data-testid="context-status"
      className="flex min-w-0 items-center gap-1.5 text-[9px] text-ghost-faint"
    >
      <Gauge size={11} className={`shrink-0 ${tone.icon}`} aria-hidden="true" />
      <span className="hidden sm:inline">Context</span>
      <span className={`font-mono font-medium tabular-nums ${tone.value}`}>
        {percent === null ? '—' : `${Math.round(percent)}%`}
      </span>
      <span className="hidden h-1 w-9 overflow-hidden rounded-full bg-ghost-border/60 md:block" aria-hidden="true">
        <span
          className={`block h-full rounded-full transition-[width] duration-500 ${tone.fill}`}
          style={{ width: `${progress}%` }}
        />
      </span>
      <span className="hidden whitespace-nowrap font-mono tabular-nums text-ghost-faint xl:inline" aria-hidden="true">
        {status.tokens === null ? 'recalculating' : `${formatTokens(status.tokens)} / ${formatTokens(status.contextWindow)}`}
      </span>
    </span>
  )
}
