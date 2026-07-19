import { useEffect, useState, type FormEvent } from 'react'
import { AlertTriangle, Check, LoaderCircle } from 'lucide-react'
import { updateThreadLimits } from '../../api'
import {
  formatCompactTokens,
  formatCompactUsd,
  formatTokenCount,
  formatUsd,
  usageDescription,
} from '../../lib/formatUsage'
import type { Thread, ThreadUsageSnapshot, ThreadUsageTotals } from '../../types'
import { Button, GhostButton, PrimaryButton } from '../atoms/Button'
import { TextInput } from '../atoms/Input'

type ThreadUsageLimitsProps = {
  projectId: string
  thread: Thread
  usage?: ThreadUsageSnapshot
  showAllThreads: boolean
  onThreadUpdated: (thread: Thread) => void
}

function UsageTotal({ label, usage, emphasize = false }: {
  label: string
  usage: ThreadUsageTotals
  emphasize?: boolean
}) {
  return (
    <div className={`min-w-0 rounded-lg border px-2.5 py-2 ${
      emphasize
        ? 'border-ghost-green/30 bg-ghost-green/[0.06]'
        : 'border-ghost-border/55 bg-ghost-black/25'
    }`} title={usageDescription(usage)}>
      <p className="font-mono text-[8px] font-semibold uppercase tracking-[0.1em] text-ghost-faint">
        {label}
      </p>
      <p className="mt-1 flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
        <span className="font-mono text-[11px] font-semibold text-ghost-bright-white">
          {formatCompactTokens(usage.totalTokens)}
        </span>
        <span className="text-[8px] text-ghost-dim">tokens</span>
        <span className="font-mono text-[9px] text-ghost-green">{formatCompactUsd(usage.costUsd)}</span>
      </p>
    </div>
  )
}

export function ThreadUsageLimits({
  projectId,
  thread,
  usage,
  showAllThreads,
  onThreadUpdated,
}: ThreadUsageLimitsProps) {
  const [editing, setEditing] = useState(false)
  const [tokenLimit, setTokenLimit] = useState(thread.tokenLimit?.toString() ?? '')
  const [costLimit, setCostLimit] = useState(thread.costLimitUsd?.toString() ?? '')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    setTokenLimit(thread.tokenLimit?.toString() ?? '')
    setCostLimit(thread.costLimitUsd?.toString() ?? '')
    setEditing(false)
    setError('')
  }, [thread.costLimitUsd, thread.id, thread.tokenLimit])

  function beginEditing() {
    setTokenLimit(thread.tokenLimit?.toString() ?? '')
    setCostLimit(thread.costLimitUsd?.toString() ?? '')
    setError('')
    setEditing(true)
  }

  function cancelEditing() {
    if (saving) return
    setTokenLimit(thread.tokenLimit?.toString() ?? '')
    setCostLimit(thread.costLimitUsd?.toString() ?? '')
    setError('')
    setEditing(false)
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving) return

    const tokenValue = tokenLimit.trim()
    const costValue = costLimit.trim()
    if (tokenValue && (!/^\d+$/.test(tokenValue) || !Number.isSafeInteger(Number(tokenValue)) || Number(tokenValue) <= 0)) {
      setError('Token limit must be a positive whole number, or left blank.')
      return
    }
    if (costValue && (!Number.isFinite(Number(costValue)) || Number(costValue) <= 0)) {
      setError('Cost limit must be a positive USD amount, or left blank.')
      return
    }

    setSaving(true)
    setError('')
    try {
      const updated = await updateThreadLimits(projectId, thread.id, {
        tokenLimit: tokenValue ? Number(tokenValue) : null,
        costLimitUsd: costValue ? Number(costValue) : null,
      })
      onThreadUpdated(updated)
      setEditing(false)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not save usage limits.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section aria-labelledby="usage-limits-heading">
      <p
        id="usage-limits-heading"
        className="font-mono text-[8px] font-semibold uppercase tracking-[0.16em] text-ghost-faint"
      >
        Usage &amp; limits
      </p>

      {usage ? (
        <>
          <div className={`mt-2.5 grid gap-1.5 ${showAllThreads ? 'grid-cols-2' : 'grid-cols-1'}`}>
            <UsageTotal label="Own" usage={usage.own} />
            {showAllThreads && <UsageTotal label="All threads" usage={usage.total} emphasize />}
          </div>
          <p
            className="mt-2 font-mono text-[8px] leading-4 text-ghost-faint"
            title={`${formatTokenCount(usage.own.inputTokens)} input, ${formatTokenCount(usage.own.outputTokens)} output, ${formatTokenCount(usage.own.cacheReadTokens)} cache read, ${formatTokenCount(usage.own.cacheWriteTokens)} cache write tokens`}
          >
            Own: ↑ {formatCompactTokens(usage.own.inputTokens)} in · ↓ {formatCompactTokens(usage.own.outputTokens)} out
            {(usage.own.cacheReadTokens > 0 || usage.own.cacheWriteTokens > 0) && (
              <> · cache {formatCompactTokens(usage.own.cacheReadTokens)}R/{formatCompactTokens(usage.own.cacheWriteTokens)}W</>
            )}
          </p>
        </>
      ) : (
        <p className="mt-2.5 rounded-lg border border-ghost-border/55 bg-ghost-black/20 px-2.5 py-2 text-[9px] text-ghost-dim">
          Waiting for usage data…
        </p>
      )}

      {usage?.limitReached && (
        <div role="alert" className="mt-2.5 flex gap-2 rounded-lg border border-ghost-bright-red/35 bg-ghost-bright-red/[0.08] px-2.5 py-2 text-ghost-bright-red">
          <AlertTriangle size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
          <p className="text-[9px] leading-4">
            {usage.limitThreadId && usage.limitThreadId !== thread.id
              ? 'An ancestor thread usage limit is reached. Increase or remove that limit to allow more agent work.'
              : 'Usage limit reached. Increase or remove a limit to allow more agent work.'}
          </p>
        </div>
      )}

      {editing ? (
        <form onSubmit={(event) => void handleSubmit(event)} className="mt-3 rounded-xl border border-ghost-border/55 bg-ghost-black/25 p-3">
          <p className="text-[9px] leading-4 text-ghost-dim">Leave either field blank for no limit.</p>
          <div className="mt-2.5 grid grid-cols-2 gap-2">
            <label className="min-w-0">
              <span className="block font-mono text-[8px] font-semibold uppercase tracking-[0.08em] text-ghost-faint">
                Token limit
              </span>
              <TextInput
                type="number"
                inputMode="numeric"
                min="1"
                step="1"
                value={tokenLimit}
                onChange={(event) => {
                  setTokenLimit(event.target.value)
                  setError('')
                }}
                disabled={saving}
                placeholder="No limit"
                aria-label="Token limit"
                autoFocus
                className="mt-1.5 h-8 px-2 font-mono text-[9px]"
              />
            </label>
            <label className="min-w-0">
              <span className="block font-mono text-[8px] font-semibold uppercase tracking-[0.08em] text-ghost-faint">
                Cost limit (USD)
              </span>
              <TextInput
                type="number"
                inputMode="decimal"
                step="any"
                value={costLimit}
                onChange={(event) => {
                  setCostLimit(event.target.value)
                  setError('')
                }}
                disabled={saving}
                placeholder="No limit"
                aria-label="Cost limit in USD"
                className="mt-1.5 h-8 px-2 font-mono text-[9px]"
              />
            </label>
          </div>
          {usage && (thread.tokenLimit || thread.costLimitUsd) && (
            <p className="mt-2 font-mono text-[8px] leading-4 text-ghost-faint">
              Current total: {formatTokenCount(usage.total.totalTokens)} tokens · {formatUsd(usage.total.costUsd)}
            </p>
          )}
          {error && <p role="alert" className="mt-2 text-[9px] leading-4 text-ghost-bright-red">{error}</p>}
          <div className="mt-2.5 flex items-center gap-1.5">
            <PrimaryButton
              type="submit"
              disabled={saving}
              className="flex h-8 flex-1 items-center justify-center gap-1.5 rounded-lg text-[10px]"
            >
              {saving ? <LoaderCircle size={12} className="animate-spin" /> : <Check size={12} />}
              Save limits
            </PrimaryButton>
            <GhostButton type="button" size="sm" onClick={cancelEditing} disabled={saving}>
              Cancel
            </GhostButton>
          </div>
        </form>
      ) : (
        <div className="mt-3 flex items-center justify-between gap-2 rounded-xl border border-dashed border-ghost-border/60 px-3 py-2">
          <p className="min-w-0 font-mono text-[9px] leading-4 text-ghost-dim">
            {thread.costLimitUsd ? (
              <>
                <span className="font-semibold text-ghost-white">{formatUsd(thread.costLimitUsd)}</span> cost limit
              </>
            ) : (
              'No cost limit'
            )}
            <span className="text-ghost-faint"> · </span>
            {thread.tokenLimit ? (
              <>
                <span className="font-semibold text-ghost-white" title={`${formatTokenCount(thread.tokenLimit)} tokens`}>
                  {formatCompactTokens(thread.tokenLimit)}
                </span>{' '}
                token limit
              </>
            ) : (
              'no token limit'
            )}
          </p>
          <Button
            type="button"
            onClick={beginEditing}
            className="shrink-0 font-mono text-[9px] font-semibold text-ghost-cyan transition hover:text-ghost-bright-white"
            aria-label="Edit usage limits"
          >
            Edit
          </Button>
        </div>
      )}
    </section>
  )
}
