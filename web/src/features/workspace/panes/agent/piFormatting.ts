// Token, cost, and context-window presentation for a Pi session.
import type { AgentContextStatus } from '@/types'
import { formatCost, formatCount, formatTokens, usageValue } from './agentFormat'
import type { PiAgentMessage, PiContextUsage, PiSessionStats } from './piTypes'

export function nativeContextStatus(
  usage: PiContextUsage | undefined,
  model: string,
): AgentContextStatus | null {
  if (!usage || !Number.isFinite(usage.contextWindow) || usage.contextWindow <= 0) return null
  const hasKnownUsage = Number.isFinite(usage.tokens) && Number.isFinite(usage.percent)
  return {
    source: 'pi-native',
    tokens: hasKnownUsage ? usage.tokens : null,
    contextWindow: usage.contextWindow,
    percent: hasKnownUsage ? usage.percent : null,
    ...(model ? { model } : {}),
    updatedAt: new Date().toISOString(),
  }
}

export function formatSessionStats(stats: PiSessionStats | undefined): string {
  if (!stats) return 'Pi session totals loaded.'
  const parts: string[] = []
  if (typeof stats.totalMessages === 'number') parts.push(`${formatCount(stats.totalMessages)} messages`)
  if (typeof stats.toolCalls === 'number') parts.push(`${formatCount(stats.toolCalls)} tool calls`)
  if (stats.tokens) {
    const usage = piSessionUsage(stats)
    parts.push(
      `↑${formatTokens(usage.input)}`,
      `↓${formatTokens(usage.output)}`,
      `R${formatTokens(usage.cacheRead)}`,
      `W${formatTokens(usage.cacheWrite)}`,
    )
  }
  if (typeof stats.cost === 'number') parts.push(formatCost(stats.cost))
  return parts.length > 0 ? `Session · ${parts.join(' · ')}` : 'Pi session totals loaded.'
}

export function piSessionUsage(stats: PiSessionStats): {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
} {
  return {
    input: usageValue(stats.tokens?.input),
    output: usageValue(stats.tokens?.output),
    cacheRead: usageValue(stats.tokens?.cacheRead),
    cacheWrite: usageValue(stats.tokens?.cacheWrite),
  }
}

export function piLatestCacheHitRate(messages: PiAgentMessage[]): number | undefined {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index]
    if (message?.role !== 'assistant' || !message.usage) continue
    const input = usageValue(message.usage.input)
    const cacheRead = usageValue(message.usage.cacheRead)
    const cacheWrite = usageValue(message.usage.cacheWrite)
    const promptTokens = input + cacheRead + cacheWrite
    return promptTokens > 0 ? (cacheRead / promptTokens) * 100 : undefined
  }
  return undefined
}

