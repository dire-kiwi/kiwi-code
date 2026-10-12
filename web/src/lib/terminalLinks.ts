export function openTerminalLink(uri: string) {
  let target: URL
  try {
    target = new URL(uri)
  } catch {
    return
  }
  if (target.protocol !== 'http:' && target.protocol !== 'https:') return

  // xterm's default handler opens a blank window and navigates it afterward.
  // The desktop shell only sees that blank URL, denies it, and the link never
  // reaches the system browser, so pass the target to window.open directly.
  window.open(target.toString(), '_blank', 'noopener,noreferrer')
}
