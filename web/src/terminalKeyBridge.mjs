export const TERMINAL_ESCAPE_SEQUENCE = '\x1b'

export function terminalClipboardAction(event, hasSelection) {
  if (event.altKey) return null
  const key = event.key.toLowerCase()
  if (key === 'insert' && !event.metaKey) {
    if (event.shiftKey && !event.ctrlKey) return 'paste'
    if (event.ctrlKey && !event.shiftKey && hasSelection) return 'copy'
  }
  if (!event.ctrlKey && !event.metaKey) return null
  if (key === 'v') return 'paste'
  // With no selection, preserve terminal interrupt (Ctrl-C) and Ctrl-X.
  if (hasSelection && (key === 'c' || key === 'x')) {
    return key === 'c' ? 'copy' : 'cut'
  }
  return null
}

export function isTerminalEscapeKey(event) {
  return event.key === 'Escape'
    || event.key === 'Esc'
    || event.code === 'Escape'
    || event.keyCode === 27
}

export function terminalControlSequence(event) {
  if (isTerminalEscapeKey(event)) return TERMINAL_ESCAPE_SEQUENCE

  const isWordErase = (event.ctrlKey || event.metaKey)
    && !event.altKey
    && !event.shiftKey
    && (event.key.toLowerCase() === 'w' || event.code === 'KeyW')
  return isWordErase ? '\x17' : null
}

export function shouldBridgeTerminalControl(data, terminalHasFocus, pageHasNeutralFocus) {
  return terminalHasFocus
    || (data === TERMINAL_ESCAPE_SEQUENCE && pageHasNeutralFocus)
}

export function shouldForwardTerminalBlurAsEscape({
  active,
  isPiTerminal,
  pageStillFocused,
  pageHasNeutralFocus,
  recentPointerDown,
  recentEscapeEvent,
}) {
  return active
    && isPiTerminal
    && pageStillFocused
    && pageHasNeutralFocus
    && !recentPointerDown
    && !recentEscapeEvent
}
