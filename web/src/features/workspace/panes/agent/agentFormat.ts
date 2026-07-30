// Shared by both native panes. These were duplicated byte-for-byte, and both
// copies carried a comment saying they had to stay aligned with the other --
// which is a rule a single definition enforces and a comment does not.
//
// Deliberately NOT shared: addTurnMarkers and contentBlocks, which also look
// duplicated but are not. Claude's addTurnMarkers additionally skips the
// MAX_SAFE_INTEGER live-assistant sentinel, and its contentBlocks wraps a bare
// string into a text block. Unifying either would be a behaviour change.

export function usageValue(value: number | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

// Compact thresholds and precision follow Pi's terminal footer.
export function formatTokens(value: number): string {
  if (value < 1_000) return value.toString()
  if (value < 10_000) return `${(value / 1_000).toFixed(1)}k`
  if (value < 1_000_000) return `${Math.round(value / 1_000)}k`
  if (value < 10_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  return `${Math.round(value / 1_000_000)}M`
}

export function formatCost(value: number): string {
  return `$${usageValue(value).toFixed(3)}`
}

export function formatCount(value: number): string {
  return new Intl.NumberFormat().format(value)
}

export function suggestionID(...parts: string[]): string {
  return parts.join('-').replace(/[^a-z0-9_-]/gi, '-')
}
