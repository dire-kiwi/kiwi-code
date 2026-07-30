// Token, cost, and context-window presentation for a Pi session.
import type { AgentContextStatus } from '@/types'
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
      `↑${formatPiTokens(usage.input)}`,
      `↓${formatPiTokens(usage.output)}`,
      `R${formatPiTokens(usage.cacheRead)}`,
      `W${formatPiTokens(usage.cacheWrite)}`,
    )
  }
  if (typeof stats.cost === 'number') parts.push(formatPiCost(stats.cost))
  return parts.length > 0 ? `Session · ${parts.join(' · ')}` : 'Pi session totals loaded.'
}

export function piSessionUsage(stats: PiSessionStats): {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
} {
  return {
    input: piUsageValue(stats.tokens?.input),
    output: piUsageValue(stats.tokens?.output),
    cacheRead: piUsageValue(stats.tokens?.cacheRead),
    cacheWrite: piUsageValue(stats.tokens?.cacheWrite),
  }
}

export function piLatestCacheHitRate(messages: PiAgentMessage[]): number | undefined {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index]
    if (message?.role !== 'assistant' || !message.usage) continue
    const input = piUsageValue(message.usage.input)
    const cacheRead = piUsageValue(message.usage.cacheRead)
    const cacheWrite = piUsageValue(message.usage.cacheWrite)
    const promptTokens = input + cacheRead + cacheWrite
    return promptTokens > 0 ? (cacheRead / promptTokens) * 100 : undefined
  }
  return undefined
}

export function piUsageValue(value: number | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

// Keep the compact thresholds and precision aligned with Pi's terminal footer.
export function formatPiTokens(value: number): string {
  if (value < 1_000) return value.toString()
  if (value < 10_000) return `${(value / 1_000).toFixed(1)}k`
  if (value < 1_000_000) return `${Math.round(value / 1_000)}k`
  if (value < 10_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  return `${Math.round(value / 1_000_000)}M`
}

export function formatPiCost(value: number): string {
  return `$${piUsageValue(value).toFixed(3)}`
}

export function formatCount(value: number): string {
  return new Intl.NumberFormat().format(value)
}
