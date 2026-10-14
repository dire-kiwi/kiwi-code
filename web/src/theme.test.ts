import { expect, it } from 'vitest'
import { DEFAULT_THEME, toTerminalTheme } from './theme'

it('converts theme colors to wterm RGB integers in ANSI palette order', () => {
  const colors = toTerminalTheme(DEFAULT_THEME)
  expect(colors.foreground).toBe(0xffffff)
  expect(colors.background).toBe(0x282c34)
  expect(colors.cursor).toBe(0xffffff)
  expect(colors.palette).toHaveLength(16)
  expect(colors.palette[0]).toBe(0x1d1f21)
  expect(colors.palette[1]).toBe(0xcc6666)
  expect(colors.palette[8]).toBe(0x666666)
  expect(colors.palette[15]).toBe(0xeaeaea)
})

it('falls back to the default color when a stored color is not hex', () => {
  const colors = toTerminalTheme({
    ...DEFAULT_THEME,
    colors: { ...DEFAULT_THEME.colors, background: 'transparent', red: '#12345' },
  })
  expect(colors.background).toBe(0x282c34)
  expect(colors.palette[1]).toBe(0xcc6666)
})
