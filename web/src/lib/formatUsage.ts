import type { ThreadUsageTotals } from '../types'

const compactNumber = new Intl.NumberFormat('en-US', {
  notation: 'compact',
  maximumFractionDigits: 1,
})

const wholeNumber = new Intl.NumberFormat('en-US', {
  maximumFractionDigits: 0,
})

export function formatTokenCount(value: number) {
  return wholeNumber.format(Math.max(0, value))
}

export function formatCompactTokens(value: number) {
  return compactNumber.format(Math.max(0, value)).replace('K', 'k')
}

export function formatUsd(value: number) {
  const amount = Math.max(0, value)
  if (amount > 0 && amount < 0.0001) return '<$0.0001'
  const maximumFractionDigits = amount > 0 && amount < 0.01 ? 4 : 2
  return amount.toLocaleString('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 2,
    maximumFractionDigits,
  })
}

export function formatCompactUsd(value: number) {
  const amount = Math.max(0, value)
  if (amount >= 1_000) return `$${compactNumber.format(amount).replace('K', 'k')}`
  if (amount > 0 && amount < 0.0001) return '<$.0001'
  if (amount > 0 && amount < 0.01) return `$${amount.toFixed(4).replace(/^0/, '')}`
  return `$${amount.toFixed(amount < 10 ? 2 : 1)}`
}

export function usageDescription(usage: ThreadUsageTotals) {
  return `${formatTokenCount(usage.totalTokens)} tokens, ${formatUsd(usage.costUsd)}`
}
