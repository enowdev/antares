import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react'
import { useLocalStorage } from './hooks'
import {
  DEFAULT_MODE,
  DEFAULT_PALETTE,
  PALETTE_KEY,
  THEME_KEY,
  applyAppearance,
  parseColorMode,
  parsePalette,
  type ColorMode,
  type Palette,
} from './theme'

export type { ColorMode, Palette }

interface ThemeValue {
  theme: ColorMode
  setTheme: (v: ColorMode) => void
  toggleTheme: () => void
  palette: Palette
  setPalette: (v: Palette) => void
}

const ThemeContext = createContext<ThemeValue | null>(null)

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [storedTheme, setThemeRaw] = useLocalStorage<ColorMode>(THEME_KEY, DEFAULT_MODE)
  const [storedPalette, setPaletteRaw] = useLocalStorage<Palette>(PALETTE_KEY, DEFAULT_PALETTE)
  const theme = parseColorMode(storedTheme)
  const palette = parsePalette(storedPalette)

  useEffect(() => {
    applyAppearance(
      document.documentElement,
      document.querySelector('meta[name="theme-color"]'),
      theme,
      palette,
    )
  }, [theme, palette])

  const value = useMemo(
    () => ({
      theme,
      setTheme: (v: ColorMode) => setThemeRaw(parseColorMode(v)),
      toggleTheme: () => setThemeRaw(theme === 'dark' ? 'light' : 'dark'),
      palette,
      setPalette: (v: Palette) => setPaletteRaw(parsePalette(v)),
    }),
    [theme, palette, setThemeRaw, setPaletteRaw],
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme(): ThemeValue {
  const ctx = useContext(ThemeContext)
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider')
  return ctx
}
