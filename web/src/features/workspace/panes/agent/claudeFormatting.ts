// Session-total presentation for a Claude run.
import { formatCost, formatCount, formatTokens } from './agentFormat'
import type { ClaudeSessionStats } from './claudeTypes'

export function formatSessionStats(stats: ClaudeSessionStats | null): string {
  if (!stats) return 'No Claude session totals yet.'
  return [
    'Session',
    `${formatCount(stats.turns)} turn${stats.turns === 1 ? '' : 's'}`,
    `↑${formatTokens(stats.input)}`,
    `↓${formatTokens(stats.output)}`,
    `R${formatTokens(stats.cacheRead)}`,
    `W${formatTokens(stats.cacheWrite)}`,
    formatCost(stats.cost),
  ].join(' · ')
}
