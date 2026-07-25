type TerminalKeyboardEvent = Pick<
  KeyboardEvent,
  'key' | 'code' | 'keyCode' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'
>

export const TERMINAL_ESCAPE_SEQUENCE: '\x1b'
export function isTerminalEscapeKey(event: TerminalKeyboardEvent): boolean
export function terminalControlSequence(event: TerminalKeyboardEvent): string | null
export function shouldBridgeTerminalControl(
  data: string,
  terminalHasFocus: boolean,
  pageHasNeutralFocus: boolean,
): boolean
export function shouldForwardTerminalBlurAsEscape(options: {
  active: boolean
  isPiTerminal: boolean
  pageStillFocused: boolean
  pageHasNeutralFocus: boolean
  recentPointerDown: boolean
  recentEscapeEvent: boolean
}): boolean
