/** Compact timestamp for list rows: time of day for today, short date otherwise. */
export function formatWhen(iso: string) {
  const value = new Date(iso)
  if (Number.isNaN(value.getTime())) return ''
  if (value.toDateString() === new Date().toDateString()) {
    return value.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  }
  return value.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
}
