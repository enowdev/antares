import type { MessageKey } from './i18n'
import type { ModuleId } from './modules'

/**
 * Command palette logic: item building and fuzzy ranking. Pure (no React, no
 * DOM) so it runs under `bun test`; CommandPalette.tsx only renders what this
 * returns.
 */

export type PaletteKind = 'action' | 'page' | 'session'

export type PaletteActionId = 'new-chat' | 'toggle-theme' | 'models'

export interface PaletteItem {
  /** Unique across every group; also the DOM id suffix of the option. */
  id: string
  kind: PaletteKind
  /** What the row shows, in the active language ("Automation › Schedules"). */
  label: string
  /** Secondary text on the right: a path or a relative time. */
  hint?: string
  /** Extra strings that match but are not shown: English titles, old paths. */
  keywords: string[]
  /** The owning module is switched off. The page still resolves. */
  off?: boolean
  /** Pathname to navigate to. */
  to?: string
  action?: PaletteActionId
  /** Hub and route ids, so the view can pick an icon. */
  hubId?: string
  routeId?: string
}

export interface PaletteGroup {
  kind: PaletteKind
  items: PaletteItem[]
}

/** Translate a message key. The palette takes two: the active language and English. */
export type Translate = (key: MessageKey) => string

/** The slice of a hub (routes.ts HubDef) the palette needs. */
export interface PaletteHubInput {
  id: string
  path: string
  titleKey: MessageKey
  module?: ModuleId
  tabs: {
    id: string
    path: string
    titleKey: MessageKey
    tabKey?: MessageKey
    legacyPaths?: string[]
  }[]
}

export interface PaletteSessionInput {
  id: string
  title: string
  updated_at?: string
}

/** Group order when nothing is typed. */
const KIND_ORDER: PaletteKind[] = ['action', 'page', 'session']

/** Keyword hits rank a little below the same hit on the visible label. */
const KEYWORD_WEIGHT = 0.9

/** fuzzyScore returns at least this for any substring hit, below it for subsequences. */
const SUBSTRING_FLOOR = 400

// ---------------------------------------------------------------- scoring

const SEPARATOR = /[\s\-_/.›>:&,()|]/

/** A word starts at index 0, after a separator, or at a camelCase hump. */
function isWordStart(text: string, i: number): boolean {
  if (i === 0) return true
  const prev = text[i - 1]
  if (SEPARATOR.test(prev)) return true
  const cur = text[i]
  return prev === prev.toLowerCase() && cur !== cur.toLowerCase()
}

/**
 * Score how well `query` matches `text`, case-insensitively. Higher is better;
 * `null` means no match. Tiers, best first: exact, prefix, substring at a word
 * start, any substring, then an in-order subsequence scored by word starts
 * and consecutive runs. Shorter texts win ties within a tier.
 */
export function fuzzyScore(query: string, text: string): number | null {
  const q = query.trim().toLowerCase()
  if (!q) return 0
  const lower = text.toLowerCase()
  if (!lower) return null
  const extra = Math.min(Math.max(lower.length - q.length, 0), 50)

  if (lower === q) return 1000
  if (lower.startsWith(q)) return 900 - extra

  let first = -1
  for (let at = lower.indexOf(q); at !== -1; at = lower.indexOf(q, at + 1)) {
    if (first === -1) first = at
    if (isWordStart(text, at)) return 700 - Math.min(at, 50) - extra / 2
  }
  if (first !== -1) return 500 - Math.min(first, 50) - extra / 2

  let qi = 0
  let prev = -2
  let start = -1
  let bonus = 0
  for (let ti = 0; ti < lower.length && qi < q.length; ti++) {
    if (lower[ti] !== q[qi]) continue
    if (start === -1) start = ti
    bonus += 1
    if (isWordStart(text, ti)) bonus += 8
    if (prev === ti - 1) bonus += 4
    prev = ti
    qi++
  }
  if (qi < q.length) return null
  const gaps = prev - start + 1 - q.length
  return Math.max(1, Math.min(399, 100 + bonus * 4 - gaps * 2 - Math.min(start, 20)))
}

/**
 * Score an item against a (possibly multi-word) query. Every word must match
 * the label or a keyword; the item's score is the sum of each word's best.
 */
export function scoreItem(item: PaletteItem, query: string): number | null {
  const words = query.trim().split(/\s+/).filter(Boolean)
  if (words.length === 0) return 0
  let total = 0
  for (const word of words) {
    let best = fuzzyScore(word, item.label)
    for (const kw of item.keywords) {
      // Hidden keywords must contain the word outright: a scattered
      // subsequence hit on text the user cannot see looks like noise.
      const s = fuzzyScore(word, kw)
      if (s === null || s < SUBSTRING_FLOOR) continue
      if (best === null || s * KEYWORD_WEIGHT > best) best = s * KEYWORD_WEIGHT
    }
    if (best === null) return null
    total += best
  }
  return total
}

/**
 * Filter and rank items into display groups. With no query every item is
 * kept in its original order, groups in action → page → session order. With
 * a query, items are sorted by score and groups by their best item.
 */
export function rankItems(items: PaletteItem[], query: string): PaletteGroup[] {
  const q = query.trim()
  if (!q) {
    return KIND_ORDER.map((kind) => ({ kind, items: items.filter((i) => i.kind === kind) })).filter(
      (g) => g.items.length > 0,
    )
  }
  const scored: { item: PaletteItem; score: number; index: number }[] = []
  items.forEach((item, index) => {
    const score = scoreItem(item, q)
    if (score !== null) scored.push({ item, score, index })
  })
  scored.sort((a, b) => b.score - a.score || a.index - b.index)

  const groups = new Map<PaletteKind, PaletteItem[]>()
  for (const { item } of scored) {
    const list = groups.get(item.kind)
    if (list) list.push(item)
    else groups.set(item.kind, [item])
  }
  // Map insertion order follows the first (best) item of each kind.
  return [...groups].map(([kind, list]) => ({ kind, items: list }))
}

/** Groups flattened in display order: the keyboard walks this list. */
export function flattenGroups(groups: PaletteGroup[]): PaletteItem[] {
  return groups.flatMap((g) => g.items)
}

// ---------------------------------------------------------------- building

function unique(values: string[]): string[] {
  const out: string[] = []
  for (const v of values) {
    if (v && !out.includes(v)) out.push(v)
  }
  return out
}

/** "/content-creator" → ["/content-creator", "content-creator", "content creator"]. */
function pathKeywords(path: string): string[] {
  const bare = path.replace(/^\/+/, '')
  return [path, bare, bare.replace(/[-/]/g, ' ')]
}

export interface BuildPagesOptions {
  t: Translate
  /** English, so "cron" or "config" finds pages whatever the UI language. */
  en: Translate
  /** Modules that are on. Hubs owned by any other module are flagged off. */
  activeModules: ReadonlySet<ModuleId>
}

/**
 * One item per hub and one per tab, labelled "Hub › Tab". A single-page hub
 * (Chat, Sessions) is one item under the hub's name. Hubs whose module is off
 * are still listed, flagged, because their routes still resolve.
 */
export function buildPageItems(hubs: PaletteHubInput[], opts: BuildPagesOptions): PaletteItem[] {
  const { t, en, activeModules } = opts
  const out: PaletteItem[] = []
  for (const hub of hubs) {
    const off = hub.module !== undefined && !activeModules.has(hub.module)
    const hubTitle = t(hub.titleKey)
    const hubTitleEn = en(hub.titleKey)

    if (hub.tabs.length === 1) {
      const page = hub.tabs[0]
      out.push({
        id: `page:${page.id}`,
        kind: 'page',
        label: hubTitle,
        hint: page.path,
        keywords: unique([
          hubTitleEn,
          t(page.titleKey),
          en(page.titleKey),
          page.id,
          ...pathKeywords(page.path),
          ...(page.legacyPaths ?? []).flatMap(pathKeywords),
        ]),
        off,
        to: page.path,
        hubId: hub.id,
        routeId: page.id,
      })
      continue
    }

    const first = hub.tabs[0]
    out.push({
      id: `hub:${hub.id}`,
      kind: 'page',
      label: hubTitle,
      hint: hub.path,
      keywords: unique([hubTitleEn, ...pathKeywords(hub.path)]),
      off,
      to: first.path,
      hubId: hub.id,
    })
    for (const tab of hub.tabs) {
      const tabKey = tab.tabKey ?? tab.titleKey
      out.push({
        id: `page:${tab.id}`,
        kind: 'page',
        label: `${hubTitle} › ${t(tabKey)}`,
        hint: tab.path,
        keywords: unique([
          `${hubTitleEn} › ${en(tabKey)}`,
          en(tabKey),
          t(tab.titleKey),
          en(tab.titleKey),
          tab.id,
          ...pathKeywords(tab.path),
          ...(tab.legacyPaths ?? []).flatMap(pathKeywords),
        ]),
        off,
        to: tab.path,
        hubId: hub.id,
        routeId: tab.id,
      })
    }
  }
  return out
}

export interface BuildActionsOptions {
  t: Translate
  en: Translate
  theme: 'dark' | 'light'
}

/** The short action list: new chat, theme toggle, model settings. */
export function buildActionItems({ t, en, theme }: BuildActionsOptions): PaletteItem[] {
  const themeKey: MessageKey = theme === 'dark' ? 'palette.themeLight' : 'palette.themeDark'
  return [
    {
      id: 'action:new-chat',
      kind: 'action',
      label: t('palette.newChat'),
      keywords: unique([en('palette.newChat'), 'conversation']),
      action: 'new-chat',
    },
    {
      id: 'action:toggle-theme',
      kind: 'action',
      label: t(themeKey),
      keywords: unique([en(themeKey), en('theme.toggle'), 'theme', 'dark', 'light']),
      action: 'toggle-theme',
    },
    {
      id: 'action:models',
      kind: 'action',
      label: t('palette.goModels'),
      hint: '/agent/models',
      keywords: unique([en('palette.goModels'), 'switch model', 'provider']),
      action: 'models',
      to: '/agent/models',
    },
  ]
}

/** Most recent sessions first, capped; each opens its chat. */
export function buildSessionItems(
  sessions: PaletteSessionInput[],
  opts: { untitled: string; limit?: number; timeAgo?: (iso: string) => string },
): PaletteItem[] {
  const limit = opts.limit ?? 8
  const sorted = [...sessions].sort((a, b) => (b.updated_at ?? '').localeCompare(a.updated_at ?? ''))
  return sorted.slice(0, limit).map((s) => ({
    id: `session:${s.id}`,
    kind: 'session',
    label: s.title?.trim() || opts.untitled,
    hint: s.updated_at && opts.timeAgo ? opts.timeAgo(s.updated_at) : undefined,
    keywords: [],
    to: `/c/${s.id}`,
  }))
}
