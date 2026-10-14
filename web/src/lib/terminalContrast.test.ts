import type { CellData, TerminalCore } from '@wterm/dom'
import { describe, expect, it, vi } from 'vitest'
import { ensureContrastRatio, withMinimumContrast } from './terminalContrast'

function ratio(first: number, second: number) {
  const channel = (value: number) => {
    const normalized = value / 255
    return normalized <= 0.03928 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4
  }
  const luminance = (rgb: number) => 0.2126 * channel((rgb >> 16) & 0xff)
    + 0.7152 * channel((rgb >> 8) & 0xff)
    + 0.0722 * channel(rgb & 0xff)
  const [high, low] = [luminance(first), luminance(second)].sort((a, b) => b - a)
  return (high + 0.05) / (low + 0.05)
}

const background = 0x282c34

describe('ensureContrastRatio', () => {
  it('leaves readable colors unchanged', () => {
    expect(ensureContrastRatio(0xffffff, background, 4.5)).toBeUndefined()
  })

  it('lightens black text on a dark background until it is readable', () => {
    const adjusted = ensureContrastRatio(0x000000, background, 4.5)
    expect(adjusted).toBeDefined()
    expect(ratio(adjusted!, background)).toBeGreaterThanOrEqual(4.5)
  })

  it('darkens pale text on a light background', () => {
    const adjusted = ensureContrastRatio(0xeeeeee, 0xffffff, 4.5)
    expect(adjusted).toBeDefined()
    expect(adjusted!).toBeLessThan(0xeeeeee)
    expect(ratio(adjusted!, 0xffffff)).toBeGreaterThanOrEqual(4.5)
  })
})

function fakeCore(cells: Record<string, CellData>) {
  const blank: CellData = { char: 32, fg: 256, bg: 256, flags: 0 }
  return {
    getCell: vi.fn((row: number, col: number) => cells[`${row}:${col}`] ?? blank),
    getScrollbackCell: vi.fn((offset: number, col: number) => cells[`sb${offset}:${col}`] ?? blank),
    setThemeColors: vi.fn(),
    getCols: () => 80,
    blank,
  }
}

const theme = { foreground: 0xffffff, background, cursor: 0xffffff, palette: Array(16).fill(0) }

describe('withMinimumContrast', () => {
  it('adjusts explicit foregrounds against the default background', () => {
    const raw = fakeCore({ '0:0': { char: 65, fg: 256, bg: 256, flags: 0, fgRgb: 0x000000 } })
    const core = withMinimumContrast(raw as unknown as TerminalCore, 4.5, theme)
    const cell = core.getCell(0, 0)
    expect(ratio(cell.fgRgb!, background)).toBeGreaterThanOrEqual(4.5)
    expect(raw.getCell(0, 0).fgRgb).toBe(0x000000)
  })

  it('adjusts default text on an explicit background, including scrollback', () => {
    const raw = fakeCore({ 'sb2:1': { char: 65, fg: 256, bg: 256, flags: 0, bgRgb: 0xfafafa } })
    const core = withMinimumContrast(raw as unknown as TerminalCore, 4.5, theme)
    const cell = core.getScrollbackCell(2, 1)
    expect(ratio(cell.fgRgb!, 0xfafafa)).toBeGreaterThanOrEqual(4.5)
  })

  it('adjusts the displayed foreground of reversed cells', () => {
    // Reverse video draws the cell background as text over the foreground.
    const raw = fakeCore({ '0:0': { char: 65, fg: 256, bg: 256, flags: 0x20, fgRgb: background, bgRgb: 0x000000 } })
    const core = withMinimumContrast(raw as unknown as TerminalCore, 4.5, theme)
    const cell = core.getCell(0, 0)
    expect(cell.fgRgb).toBe(background)
    expect(ratio(cell.bgRgb!, background)).toBeGreaterThanOrEqual(4.5)
  })

  it('leaves default-colored and invisible cells untouched', () => {
    const invisible = { char: 65, fg: 256, bg: 256, flags: 0x40, fgRgb: 0x000000 }
    const raw = fakeCore({ '0:1': invisible })
    const core = withMinimumContrast(raw as unknown as TerminalCore, 4.5, theme)
    expect(core.getCell(0, 0)).toBe(raw.blank)
    expect(core.getCell(0, 1)).toBe(invisible)
  })

  it('follows theme defaults and delegates other members to the core', () => {
    const raw = fakeCore({ '0:0': { char: 65, fg: 256, bg: 256, flags: 0, fgRgb: 0xdddddd } })
    const core = withMinimumContrast(raw as unknown as TerminalCore, 4.5, theme)
    expect(core.getCell(0, 0).fgRgb).toBe(0xdddddd)

    const light = { ...theme, background: 0xffffff }
    core.setThemeColors?.(light)
    expect(raw.setThemeColors).toHaveBeenCalledWith(light)
    expect(core.getCell(0, 0).fgRgb).not.toBe(0xdddddd)
    expect(core.getCols()).toBe(80)
  })
})
