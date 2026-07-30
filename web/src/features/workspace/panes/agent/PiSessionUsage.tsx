import { classNames } from '@/lib/classNames'
import {
  formatCount,
  formatPiCost,
  formatPiTokens,
  piSessionUsage,
  piUsageValue,
} from './piFormatting'
import { piNativeStyles } from './piNativeStyles'
import type { PiSessionStats } from './piTypes'

export function PiSessionUsage({
  stats,
  latestCacheHitRate,
}: {
  stats: PiSessionStats | undefined
  latestCacheHitRate: number | undefined
}) {
  if (!stats?.tokens) {
    return (
      <div
        className={classNames(piNativeStyles.sessionUsage, piNativeStyles.sessionUsageLoading)}
        role="group"
        aria-label="Loading Pi session token usage and cost"
        data-testid="pi-native-session-usage"
      >
        <span aria-hidden="true">Session usage · loading…</span>
      </div>
    )
  }

  const usage = piSessionUsage(stats)
  const showCacheHitRate = (usage.cacheRead > 0 || usage.cacheWrite > 0)
    && latestCacheHitRate !== undefined
  const cost = piUsageValue(stats.cost)
  const accessibleSummary = [
    `${formatCount(usage.input)} input tokens`,
    `${formatCount(usage.output)} output tokens`,
    `${formatCount(usage.cacheRead)} cache-read tokens`,
    `${formatCount(usage.cacheWrite)} cache-write tokens`,
    ...(showCacheHitRate ? [`${latestCacheHitRate.toFixed(1)} percent latest cache hit rate`] : []),
    `${formatPiCost(cost)} cost`,
  ].join(', ')

  return (
    <div
      className={piNativeStyles.sessionUsage}
      role="group"
      aria-label={`Pi session usage: ${accessibleSummary}`}
      data-testid="pi-native-session-usage"
    >
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(usage.input)} input tokens`} aria-hidden="true">
        <b>↑</b>{formatPiTokens(usage.input)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(usage.output)} output tokens`} aria-hidden="true">
        <b>↓</b>{formatPiTokens(usage.output)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(usage.cacheRead)} cache-read tokens`} aria-hidden="true">
        <b>R</b>{formatPiTokens(usage.cacheRead)}
      </span>
      <span className={piNativeStyles.sessionUsageMetric} title={`${formatCount(usage.cacheWrite)} cache-write tokens`} aria-hidden="true">
        <b>W</b>{formatPiTokens(usage.cacheWrite)}
      </span>
      {showCacheHitRate && (
        <span className={piNativeStyles.sessionUsageMetric} title="Latest cache hit rate" aria-hidden="true">
          <b>CH</b>{latestCacheHitRate.toFixed(1)}%
        </span>
      )}
      <span className={piNativeStyles.sessionUsageCost} title="Cumulative session cost" aria-hidden="true">
        {formatPiCost(cost)}
      </span>
    </div>
  )
}
