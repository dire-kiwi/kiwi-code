import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import type { ITheme } from '@xterm/xterm'
import { getSettings } from './api'
import { DEFAULT_THEME } from './defaultTheme.generated'
import type { ThemeColors, ThemeSettings } from './types'

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

export function toTerminalTheme(theme: ThemeSettings): ITheme {
  const { colors } = theme
  return {
    background: colors.background,
    foreground: colors.foreground,
    cursor: colors.cursor,
    cursorAccent: colors.background,
    selectionBackground: colors.selectionBackground,
    selectionForeground: colors.selectionForeground,
    selectionInactiveBackground: colors.white,
    black: colors.black,
    red: colors.red,
    green: colors.green,
    yellow: colors.yellow,
    blue: colors.blue,
    magenta: colors.magenta,
    cyan: colors.cyan,
    white: colors.white,
    brightBlack: colors.brightBlack,
    brightRed: colors.brightRed,
    brightGreen: colors.brightGreen,
    brightYellow: colors.brightYellow,
    brightBlue: colors.brightBlue,
    brightMagenta: colors.brightMagenta,
    brightCyan: colors.brightCyan,
    brightWhite: colors.brightWhite,
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
  const revisionRef = useRef(0)

  const setTheme = useCallback((next: ThemeSettings) => {
    revisionRef.current += 1
    setThemeState(next)
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    const startingRevision = revisionRef.current
    getSettings(controller.signal)
      .then((settings) => {
        if (revisionRef.current === startingRevision && settings.theme) {
          setThemeState(settings.theme)
        }
      })
      .catch(() => {
        // The bundled defaults keep the UI usable while the backend is unavailable.
      })
    return () => controller.abort()
  }, [])

  useLayoutEffect(() => applyTheme(theme), [theme])

  const value = useMemo(() => ({ theme, setTheme }), [setTheme, theme])
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  const context = useContext(ThemeContext)
  if (!context) throw new Error('useTheme must be used within ThemeProvider')
  return context
}
