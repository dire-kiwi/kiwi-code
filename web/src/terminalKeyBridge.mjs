export const TERMINAL_ESCAPE_SEQUENCE = '\x1b'

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
