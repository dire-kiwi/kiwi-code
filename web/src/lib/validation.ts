export const MAX_CLEANUP_RETENTION_DAYS = 3650
export const MAX_SUB_AGENT_NESTING_DEPTH = 4

const hexColorPattern = /^#[0-9a-f]{6}$/i

export function isHexColor(value: string): boolean {
  return hexColorPattern.test(value)
}
