import { describe, expect, test } from 'bun:test'
import {
  DEFAULT_MODE,
  DEFAULT_PALETTE,
  PALETTES,
  PALETTE_SWATCHES,
  PALETTE_THEME_COLORS,
  applyAppearance,
  parseColorMode,
  parsePalette,
  readStoredColorMode,
  readStoredPalette,
} from './theme.ts'

describe('parsePalette', () => {
  test('accepts the shipped palettes and maps the old zinc set', () => {
    expect(PALETTES).toEqual(['claude', 'facebook', 'pinterest', 'supabase'])
    expect(parsePalette('pinterest')).toBe('pinterest')
    expect(parsePalette('violet')).toBe('facebook')
    expect(parsePalette('zinc')).toBe('facebook')
    expect(parsePalette('ocean')).toBe('supabase')
    expect(parsePalette('forest')).toBe('pinterest')
    expect(parsePalette('red')).toBe(DEFAULT_PALETTE)
    expect(parsePalette(undefined)).toBe('facebook')
  })

  test('every palette shares the supabase canvas and keeps its own accent', () => {
    expect(PALETTE_SWATCHES.claude.light).toEqual({ bg: '#ffffff', accent: '#cc785c' })
    expect(PALETTE_SWATCHES.claude.dark).toEqual({ bg: '#1c1c1c', accent: '#cc785c' })
    expect(PALETTE_SWATCHES.facebook.light).toEqual({ bg: '#ffffff', accent: '#0064e0' })
    expect(PALETTE_SWATCHES.facebook.dark).toEqual({ bg: '#1c1c1c', accent: '#1876f2' })
    expect(PALETTE_SWATCHES.pinterest.light).toEqual({ bg: '#ffffff', accent: '#e60023' })
    expect(PALETTE_SWATCHES.pinterest.dark).toEqual({ bg: '#1c1c1c', accent: '#e60023' })
    expect(PALETTE_SWATCHES.supabase.light).toEqual({ bg: '#ffffff', accent: '#3ecf8e' })
    expect(PALETTE_SWATCHES.supabase.dark).toEqual({ bg: '#1c1c1c', accent: '#3ecf8e' })
  })
})

describe('parseColorMode', () => {
  test('accepts light and dark and falls back to dark', () => {
    expect(parseColorMode('light')).toBe('light')
    expect(parseColorMode('dark')).toBe('dark')
    expect(parseColorMode('system')).toBe(DEFAULT_MODE)
  })
})

describe('readStored*', () => {
  test('reads JSON localStorage values and ignores garbage', () => {
    const store = {
      'antares.palette': '"claude"',
      'antares.theme': '"light"',
    }
    const getItem = (k) => store[k] ?? null
    expect(readStoredPalette(getItem)).toBe('claude')
    expect(readStoredColorMode(getItem)).toBe('light')
  })

  test('falls back when JSON is invalid or the palette is unknown', () => {
    expect(readStoredPalette((k) => (k === 'antares.palette' ? 'nope' : null))).toBe('facebook')
    expect(readStoredPalette((k) => (k === 'antares.palette' ? '"red"' : null))).toBe('facebook')
    expect(readStoredColorMode(() => '{')).toBe('dark')
    expect(
      readStoredColorMode(() => {
        throw new Error('blocked')
      }),
    ).toBe('dark')
  })
})

describe('applyAppearance', () => {
  test('sets dark class, data-palette, color-scheme, and theme-color', () => {
    const toggled = []
    const attrs = {}
    const style = { colorScheme: '' }
    const meta = { content: '#000' }
    applyAppearance(
      {
        classList: { toggle: (token, force) => toggled.push([token, force]) },
        setAttribute: (name, value) => {
          attrs[name] = value
        },
        style,
      },
      {
        setAttribute: (name, value) => {
          if (name === 'content') meta.content = value
        },
      },
      'light',
      'claude',
    )
    expect(toggled).toEqual([['dark', false]])
    expect(attrs['data-palette']).toBe('claude')
    expect(style.colorScheme).toBe('light')
    expect(meta.content).toBe(PALETTE_THEME_COLORS.claude.light)
  })
})
