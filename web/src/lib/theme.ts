export const PALETTES = ['claude', 'facebook', 'pinterest', 'supabase'] as const
export type Palette = (typeof PALETTES)[number]
export type ColorMode = 'dark' | 'light'

export const PALETTE_KEY = 'antares.palette'
export const THEME_KEY = 'antares.theme'

export const DEFAULT_PALETTE: Palette = 'facebook'
export const DEFAULT_MODE: ColorMode = 'dark'

/** Stored ids from the previous zinc/ocean/forest/violet set. */
const LEGACY_PALETTES: Record<string, Palette> = {
  zinc: 'facebook',
  violet: 'facebook',
  ocean: 'supabase',
  forest: 'pinterest',
}

export const PALETTE_SWATCHES: Record<Palette, Record<ColorMode, { bg: string; accent: string }>> = {
  claude: {
    light: { bg: '#ffffff', accent: '#cc785c' },
    dark: { bg: '#1c1c1c', accent: '#cc785c' },
  },
  facebook: {
    light: { bg: '#ffffff', accent: '#0064e0' },
    dark: { bg: '#1c1c1c', accent: '#1876f2' },
  },
  pinterest: {
    light: { bg: '#ffffff', accent: '#e60023' },
    dark: { bg: '#1c1c1c', accent: '#e60023' },
  },
  supabase: {
    light: { bg: '#ffffff', accent: '#3ecf8e' },
    dark: { bg: '#1c1c1c', accent: '#3ecf8e' },
  },
}

export const PALETTE_THEME_COLORS: Record<Palette, Record<ColorMode, string>> = {
  claude: { light: PALETTE_SWATCHES.claude.light.bg, dark: PALETTE_SWATCHES.claude.dark.bg },
  facebook: { light: PALETTE_SWATCHES.facebook.light.bg, dark: PALETTE_SWATCHES.facebook.dark.bg },
  pinterest: { light: PALETTE_SWATCHES.pinterest.light.bg, dark: PALETTE_SWATCHES.pinterest.dark.bg },
  supabase: { light: PALETTE_SWATCHES.supabase.light.bg, dark: PALETTE_SWATCHES.supabase.dark.bg },
}

export interface AppearanceRoot {
  classList: { toggle(token: string, force?: boolean): void }
  setAttribute(name: string, value: string): void
  style: { colorScheme: string }
}

export interface AppearanceMeta {
  setAttribute(name: string, value: string): void
}

export function parsePalette(raw: unknown): Palette {
  if (typeof raw === 'string' && PALETTES.includes(raw as Palette)) return raw as Palette
  if (typeof raw === 'string' && raw in LEGACY_PALETTES) return LEGACY_PALETTES[raw]
  return DEFAULT_PALETTE
}

export function parseColorMode(raw: unknown): ColorMode {
  return raw === 'light' || raw === 'dark' ? raw : DEFAULT_MODE
}

function readStoredJson(getItem: (key: string) => string | null, key: string): unknown {
  try {
    const raw = getItem(key)
    return raw == null ? undefined : JSON.parse(raw)
  } catch {
    return undefined
  }
}

export function readStoredPalette(getItem: (key: string) => string | null): Palette {
  return parsePalette(readStoredJson(getItem, PALETTE_KEY))
}

export function readStoredColorMode(getItem: (key: string) => string | null): ColorMode {
  return parseColorMode(readStoredJson(getItem, THEME_KEY))
}

export function applyAppearance(
  root: AppearanceRoot,
  meta: AppearanceMeta | null,
  mode: ColorMode,
  palette: Palette,
): void {
  root.classList.toggle('dark', mode === 'dark')
  root.setAttribute('data-palette', palette)
  root.style.colorScheme = mode
  meta?.setAttribute('content', PALETTE_THEME_COLORS[palette][mode])
}
