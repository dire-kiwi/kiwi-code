import { classNames } from '@/lib/classNames'
import { formatCost, formatCount, formatTokens } from './agentFormat'
import type { ClaudeSessionStats } from './claudeTypes'
import { piNativeStyles } from './piNativeStyles'

export function ClaudeSessionUsage({ stats }: { stats: ClaudeSessionStats | null }) {
  if (!stats) {
    return (
      <div
        className={classNames(piNativeStyles.sessionUsage, piNativeStyles.sessionUsageLoading)}
        role="group"
        aria-label="Waiting for Claude session token usage and cost"
        data-testid="claude-native-session-usage"
      >
        <span aria-hidden="true">Session usage · waiting for the first run…</span>
      </div>
    )
  }

  const accessibleSummary = [
    `${formatCount(stats.input)} input tokens`,
    `${formatCount(stats.output)} output tokens`,
    `${formatCount(stats.cacheRead)} cache-read tokens`,
    `${formatCount(stats.cacheWrite)} cache-write tokens`,
    `${formatCost(stats.cost)} cost`,
  ].join(', ')

  return (
    <div
      className={piNativeStyles.sessionUsage}
      role="group"
      aria-label={`Claude session usage: ${accessibleSummary}`}
      data-testid="claude-native-session-usage"
    >
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(stats.input)} input tokens`} aria-hidden="true">
        <b>↑</b>{formatTokens(stats.input)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(stats.output)} output tokens`} aria-hidden="true">
        <b>↓</b>{formatTokens(stats.output)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(stats.cacheRead)} cache-read tokens`} aria-hidden="true">
        <b>R</b>{formatTokens(stats.cacheRead)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(stats.cacheWrite)} cache-write tokens`} aria-hidden="true">
        <b>W</b>{formatTokens(stats.cacheWrite)}
      </span>
      <span className={piNativeStyles.sessionUsageCost} title="Cumulative session cost" aria-hidden="true">
        {formatCost(stats.cost)}
      </span>
    </div>
  )
}
