import { afterEach, expect, it, vi } from 'vitest'
import { openTerminalLink } from './terminalLinks'

afterEach(() => {
  vi.restoreAllMocks()
})

it('passes web links to window.open so the desktop shell can open them externally', () => {
  const open = vi.spyOn(window, 'open').mockReturnValue(null)

  openTerminalLink('https://github.com/dire-kiwi/kiwi-code/pull/94')

  expect(open).toHaveBeenCalledExactlyOnceWith(
    'https://github.com/dire-kiwi/kiwi-code/pull/94',
    '_blank',
    'noopener,noreferrer',
  )
})

it('ignores malformed and non-web links', () => {
  const open = vi.spyOn(window, 'open').mockReturnValue(null)

  openTerminalLink('not a url')
  openTerminalLink('file:///etc/passwd')
  openTerminalLink('javascript:alert(1)')

  expect(open).not.toHaveBeenCalled()
})
