/**
 * Client side of the migrate contract (docs/plans/2026-10-03-migrate-contract.md,
 * internal/migrate/types.go): JSON shapes and the pure helpers the migrate UI
 * uses to group a plan, track the user's choices and build the apply request.
 */

export type MigrateCategory =
  | 'provider'
  | 'model'
  | 'soul'
  | 'agents_md'
  | 'user_md'
  | 'memory'
  | 'knowledge'
  | 'skill'
  | 'mcp'
  | 'cron'
  | 'channel'
  | 'role'

export type MigrateStatus = 'ready' | 'conflict' | 'needs_input' | 'unsupported'
export type Resolution = 'skip' | 'replace' | 'rename' | 'append'

export interface Detection {
  source: string
  name: string
  root: string
  version?: string
  profile?: string
  running: boolean
  summary: string
}

export interface MigrateSource {
  id: string
  name: string
  detected: Detection[]
}

export interface MigrateItem {
  id: string
  category: MigrateCategory
  title: string
  detail?: string
  status: MigrateStatus
  reason?: string
  input?: string
  secret?: boolean
  selected: boolean
}

export interface MigratePlan {
  plan_id: string
  detection: Detection
  items: MigrateItem[]
  warnings?: string[]
}

export interface Choice {
  id: string
  resolution?: Resolution
  input?: string
}

export interface MigrateReport {
  source: string
  backup: string
  applied: string[]
  skipped: string[]
  failed?: { id: string; error: string }[]
  needs_restart: boolean
}

/** Section order in the plan view. */
export const CATEGORY_ORDER: MigrateCategory[] = [
  'provider',
  'model',
  'soul',
  'agents_md',
  'user_md',
  'memory',
  'knowledge',
  'skill',
  'mcp',
  'cron',
  'channel',
  'role',
]

const RENAMES: MigrateCategory[] = ['provider', 'skill', 'mcp', 'role']
const APPENDS: MigrateCategory[] = ['soul', 'agents_md', 'user_md']

/** Conflict resolutions a category accepts, in the order the select lists them. */
export function allowedResolutions(category: MigrateCategory): Resolution[] {
  const out: Resolution[] = ['skip', 'replace']
  if (RENAMES.includes(category)) out.push('rename')
  if (APPENDS.includes(category)) out.push('append')
  return out
}

/**
 * The resolution picked when the user ticks a conflict that is still on skip:
 * the least destructive one that actually brings the item over.
 */
export function defaultKeepResolution(category: MigrateCategory): Resolution {
  const allowed = allowedResolutions(category)
  if (allowed.includes('rename')) return 'rename'
  if (allowed.includes('append')) return 'append'
  return 'replace'
}

export interface ItemState {
  selected: boolean
  resolution: Resolution
  input: string
}

export type Selection = Record<string, ItemState>

/** Whether an item can be ticked at all. */
export function selectable(item: MigrateItem): boolean {
  return item.status !== 'unsupported'
}

/**
 * Initial per-item state from the plan's defaults, keeping what the user
 * already chose for an item id that is still present (a re-plan after the
 * cached plan expired keeps the same ids).
 */
export function initialSelection(items: MigrateItem[], previous?: Selection): Selection {
  const out: Selection = {}
  for (const item of items) {
    if (!selectable(item)) continue
    const prev = previous?.[item.id]
    out[item.id] = prev ?? { selected: item.selected, resolution: 'skip', input: '' }
  }
  return out
}

export interface CategoryGroup {
  category: MigrateCategory
  items: MigrateItem[]
  /** Items that can be ticked (not unsupported). */
  selectable: number
  selected: number
}

/** Items grouped by category in {@link CATEGORY_ORDER}; unknown categories last. */
export function groupPlan(items: MigrateItem[], selection: Selection): CategoryGroup[] {
  const byCat = new Map<MigrateCategory, MigrateItem[]>()
  for (const item of items) {
    const list = byCat.get(item.category) ?? []
    list.push(item)
    byCat.set(item.category, list)
  }
  const cats = [
    ...CATEGORY_ORDER.filter((c) => byCat.has(c)),
    ...[...byCat.keys()].filter((c) => !CATEGORY_ORDER.includes(c)),
  ]
  return cats.map((category) => {
    const list = byCat.get(category) ?? []
    return {
      category,
      items: list,
      selectable: list.filter(selectable).length,
      selected: list.filter((i) => selection[i.id]?.selected).length,
    }
  })
}

/**
 * The apply request's items: every ticked, supported item, with the conflict
 * resolution and the needs_input value where they apply. A ticked conflict
 * left on skip is still sent (the server records it as skipped).
 */
export function buildChoices(items: MigrateItem[], selection: Selection): Choice[] {
  const out: Choice[] = []
  for (const item of items) {
    const s = selection[item.id]
    if (!s?.selected || !selectable(item)) continue
    const choice: Choice = { id: item.id }
    if (item.status === 'conflict') choice.resolution = s.resolution
    if (item.status === 'needs_input' && s.input.trim()) choice.input = s.input.trim()
    out.push(choice)
  }
  return out
}

/** Ticked needs_input items still missing their value; the server skips them. */
export function missingInputs(items: MigrateItem[], selection: Selection): MigrateItem[] {
  return items.filter(
    (i) => i.status === 'needs_input' && selection[i.id]?.selected && !selection[i.id]?.input.trim(),
  )
}

/** The backup directory's own name, which is what the undo endpoint takes. */
export function backupName(backup: string): string {
  const parts = backup.split(/[\\/]+/).filter(Boolean)
  return parts[parts.length - 1] ?? backup
}

/** Detected installs across every source, flattened for the picker. */
export function detectedInstalls(sources: MigrateSource[]): Detection[] {
  return sources.flatMap((s) => s.detected ?? [])
}
