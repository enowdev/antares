/**
 * Optional dashboard modules and the use-case presets that switch them on.
 *
 * Pure data — no React — so tests and the Go parity check can read it. The Go
 * side keeps the same ids in internal/config (KnownModules); a test on each
 * side fails if the two lists drift.
 *
 * Visibility rule: a module that is off is only hidden from the sidebar. Its
 * routes still resolve and the command palette still lists them.
 */

export const MODULE_IDS = ['automation', 'security', 'studio'] as const
export type ModuleId = (typeof MODULE_IDS)[number]

export const PRESET_IDS = ['general', 'coding', 'security', 'creator', 'full'] as const
export type PresetId = (typeof PRESET_IDS)[number]

/** Preset shown when the active module set matches no preset exactly. */
export const CUSTOM_PRESET = 'custom'
export type PresetLabel = PresetId | typeof CUSTOM_PRESET

/** Pre-selected in Setup for a new install. */
export const DEFAULT_PRESET: PresetId = 'general'

export const PRESET_MODULES: Record<PresetId, readonly ModuleId[]> = {
  general: [],
  coding: ['automation'],
  security: ['automation', 'security'],
  creator: ['automation', 'studio'],
  full: ['automation', 'security', 'studio'],
}

export function isModuleId(value: unknown): value is ModuleId {
  return typeof value === 'string' && (MODULE_IDS as readonly string[]).includes(value)
}

/**
 * Turn the stored value into the active set. `null`/`undefined` means the key
 * is absent from config.yaml — an install from before modules existed — and
 * resolves to every module so an upgrade hides nothing. An empty array is a
 * deliberate choice (the General preset) and stays empty. Unknown ids are
 * dropped rather than trusted.
 */
export function resolveModules(stored: unknown): Set<ModuleId> {
  if (stored === null || stored === undefined) return new Set(MODULE_IDS)
  if (!Array.isArray(stored)) return new Set(MODULE_IDS)
  return new Set(stored.filter(isModuleId))
}

/** The preset whose module set equals `active`, or "custom". */
export function presetFor(active: ReadonlySet<ModuleId>): PresetLabel {
  for (const id of PRESET_IDS) {
    const mods = PRESET_MODULES[id]
    if (mods.length === active.size && mods.every((m) => active.has(m))) return id
  }
  return CUSTOM_PRESET
}

/** Stable, ordered list for persisting. */
export function toModuleList(active: ReadonlySet<ModuleId>): ModuleId[] {
  return MODULE_IDS.filter((m) => active.has(m))
}
