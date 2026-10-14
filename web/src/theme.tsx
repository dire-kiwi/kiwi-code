import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import type { TerminalThemeColors } from '@wterm/dom'
import { DEFAULT_THEME } from './defaultTheme.generated'
import { isHexColor } from './lib/validation'
import type { ThemeColors, ThemeSettings } from './types'
import { useAppSelector } from '@/store/hooks'
import { selectTheme } from '@/store/slices/settings'

export { DEFAULT_THEME } from './defaultTheme.generated'

export const themeColorKeys = [
  'canvas',
  'sidebar',
  'background',
  'panel',
  'raised',
  'selected',
  'border',
  'foreground',
  'muted',
  'dim',
  'cursor',
  'selectionBackground',
  'selectionForeground',
  'black',
  'red',
  'green',
  'yellow',
  'blue',
  'magenta',
  'cyan',
  'white',
  'brightBlack',
  'brightRed',
  'brightGreen',
  'brightYellow',
  'brightBlue',
  'brightMagenta',
  'brightCyan',
  'brightWhite',
] as const satisfies readonly (keyof ThemeColors)[]

export function themesEqual(left: ThemeSettings, right: ThemeSettings) {
  return left.fontFamily === right.fontFamily
    && left.fontSize === right.fontSize
    && themeColorKeys.every((key) => left.colors[key] === right.colors[key])
}

const terminalPaletteKeys = [
  'black',
  'red',
  'green',
  'yellow',
  'blue',
  'magenta',
  'cyan',
  'white',
  'brightBlack',
  'brightRed',
  'brightGreen',
  'brightYellow',
  'brightBlue',
  'brightMagenta',
  'brightCyan',
  'brightWhite',
] as const satisfies readonly (keyof ThemeColors)[]

export function toTerminalTheme(theme: ThemeSettings): TerminalThemeColors {
  // wterm takes opaque 0xRRGGBB integers and rejects the whole theme when any
  // value is invalid, so fall back per color instead of failing the terminal.
  const color = (key: keyof ThemeColors) => {
    const value = theme.colors[key]
    return Number.parseInt((isHexColor(value) ? value : DEFAULT_THEME.colors[key]).slice(1), 16)
  }
  return {
    foreground: color('foreground'),
    background: color('background'),
    cursor: color('cursor'),
    palette: terminalPaletteKeys.map(color),
  }
}

function cssColorName(name: keyof ThemeColors) {
  return name.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`)
}

function applyTheme(theme: ThemeSettings) {
  const root = document.documentElement
  root.style.setProperty('--theme-font-family', theme.fontFamily)
  for (const key of themeColorKeys) {
    root.style.setProperty(`--theme-color-${cssColorName(key)}`, theme.colors[key])
  }
  document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
    ?.setAttribute('content', theme.colors.canvas)
}

type ThemeContextValue = {
  theme: ThemeSettings
  setTheme: (theme: ThemeSettings) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<ThemeSettings>(DEFAULT_THEME)
  // Local state on top of the stored theme, not instead of it: the appearance
  // editor previews unsaved edits through setTheme, and the saved value arriving
  // in the settings slice overwrites the preview.
  const storedTheme = useAppSelector(selectTheme)

  const setTheme = useCallback((next: ThemeSettings) => {
    setThemeState(next)
  }, [])

  useEffect(() => {
    if (storedTheme) setThemeState(storedTheme)
  }, [storedTheme])

  useLayoutEffect(() => applyTheme(theme), [theme])

  const value = useMemo(() => ({ theme, setTheme }), [setTheme, theme])
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  const context = useContext(ThemeContext)
  if (!context) throw new Error('useTheme must be used within ThemeProvider')
  return context
}
