import type { CellData, TerminalCore, TerminalThemeColors } from '@wterm/dom'

const FLAG_REVERSE = 0x20
const FLAG_INVISIBLE = 0x40
const CACHE_LIMIT = 4096

function channel(value: number) {
  const normalized = value / 255
  return normalized <= 0.03928 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4
}

function luminance(rgb: number) {
  return 0.2126 * channel((rgb >> 16) & 0xff)
    + 0.7152 * channel((rgb >> 8) & 0xff)
    + 0.0722 * channel(rgb & 0xff)
}

function contrast(first: number, second: number) {
  return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05)
}

function shiftLuminance(foreground: number, backgroundLuminance: number, ratio: number, lighten: boolean) {
  let red = (foreground >> 16) & 0xff
  let green = (foreground >> 8) & 0xff
  let blue = foreground & 0xff
  let rgb = foreground
  while (contrast(luminance(rgb), backgroundLuminance) < ratio) {
    if (lighten) {
      if (red === 0xff && green === 0xff && blue === 0xff) break
      red = Math.min(0xff, red + Math.ceil((0xff - red) * 0.1))
      green = Math.min(0xff, green + Math.ceil((0xff - green) * 0.1))
      blue = Math.min(0xff, blue + Math.ceil((0xff - blue) * 0.1))
    } else {
      if (red === 0 && green === 0 && blue === 0) break
      red -= Math.ceil(red * 0.1)
      green -= Math.ceil(green * 0.1)
      blue -= Math.ceil(blue * 0.1)
    }
    rgb = (red << 16) | (green << 8) | blue
  }
  return rgb
}

/**
 * Returns a foreground adjusted to meet the contrast ratio against the
 * background, or undefined when the original already does. This follows
 * xterm's minimumContrastRatio: move away from the background's luminance
 * first, and try the other direction only if that cannot reach the ratio.
 */
export function ensureContrastRatio(foreground: number, background: number, ratio: number) {
  const foregroundLuminance = luminance(foreground)
  const backgroundLuminance = luminance(background)
  if (contrast(foregroundLuminance, backgroundLuminance) >= ratio) return undefined

  const lighten = foregroundLuminance >= backgroundLuminance
  const preferred = shiftLuminance(foreground, backgroundLuminance, ratio, lighten)
  const preferredRatio = contrast(luminance(preferred), backgroundLuminance)
  if (preferredRatio >= ratio) return preferred
  const fallback = shiftLuminance(foreground, backgroundLuminance, ratio, !lighten)
  return contrast(luminance(fallback), backgroundLuminance) > preferredRatio ? fallback : preferred
}

/**
 * Wraps a terminal core so rendered cells keep a minimum contrast between text
 * and its background. wterm has no equivalent of xterm's minimumContrastRatio,
 * and agents emit explicit colors (including RGB black) that the configured
 * theme cannot anticipate. The ANSI palette itself is left unchanged.
 */
export function withMinimumContrast<Core extends TerminalCore>(
  core: Core,
  ratio: number,
  colors: TerminalThemeColors,
): Core {
  let defaultForeground = colors.foreground
  let defaultBackground = colors.background
  const adjustments = new Map<number, number | undefined>()

  function adjust(cell: CellData): CellData {
    if ((cell.fgRgb === undefined && cell.bgRgb === undefined) || cell.flags & FLAG_INVISIBLE) return cell

    const reverse = (cell.flags & FLAG_REVERSE) !== 0
    const cellForeground = cell.fgRgb ?? defaultForeground
    const cellBackground = cell.bgRgb ?? defaultBackground
    const foreground = reverse ? cellBackground : cellForeground
    const background = reverse ? cellForeground : cellBackground
    const key = foreground * 0x1000000 + background
    let adjusted = adjustments.get(key)
    if (adjusted === undefined && !adjustments.has(key)) {
      if (adjustments.size >= CACHE_LIMIT) adjustments.clear()
      adjusted = ensureContrastRatio(foreground, background, ratio)
      adjustments.set(key, adjusted)
    }
    if (adjusted === undefined) return cell
    // Cores can share cell objects, such as a blank cell, so never mutate one.
    return reverse ? { ...cell, bgRgb: adjusted } : { ...cell, fgRgb: adjusted }
  }

  const overrides: Partial<Record<keyof TerminalCore, unknown>> = {
    getCell: (row: number, col: number) => adjust(core.getCell(row, col)),
    getScrollbackCell: (offset: number, col: number) => adjust(core.getScrollbackCell(offset, col)),
  }
  if (core.setThemeColors) {
    overrides.setThemeColors = (next: TerminalThemeColors) => {
      defaultForeground = next.foreground
      defaultBackground = next.background
      core.setThemeColors?.(next)
    }
  }

  const bound = new Map<PropertyKey, unknown>()
  return new Proxy(core, {
    get(target, property) {
      if (Object.hasOwn(overrides, property)) return overrides[property as keyof TerminalCore]
      const value: unknown = Reflect.get(target, property, target)
      if (typeof value !== 'function') return value
      let method = bound.get(property)
      if (!method) {
        method = value.bind(target)
        bound.set(property, method)
      }
      return method
    },
  })
}
