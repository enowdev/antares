import type { MessageKey } from './i18n'
import { HUB_MANIFEST, ROUTE_MANIFEST, type RouteManifestEntry } from './routeManifest'

/**
 * Where each config group is edited. Pure data derived from the route
 * manifest, so tests and the Settings page agree on which groups stay in
 * Settings and which moved to a page's settings sheet.
 */

const ACRONYMS: Record<string, string> = {
  rag: 'RAG',
  mcp: 'MCP',
  yaml: 'YAML',
  dsn: 'DSN',
  api: 'API',
}

/** "prompt_caching" → "Prompt caching", "rag" → "RAG" */
export function humanizeGroup(name: string): string {
  return name
    .split('_')
    .map((w, i) => ACRONYMS[w] ?? (i === 0 ? w.charAt(0).toUpperCase() + w.slice(1) : w))
    .join(' ')
}

/** Query parameter that opens a page's settings sheet (`?settings=1`). */
export const SETTINGS_PARAM = 'settings'

/** Group → the route that owns it. Throws if two routes claim one group. */
export function configGroupOwners(
  manifest: RouteManifestEntry[] = ROUTE_MANIFEST,
): Map<string, RouteManifestEntry> {
  const owners = new Map<string, RouteManifestEntry>()
  for (const route of manifest) {
    for (const group of route.configGroups ?? []) {
      const prev = owners.get(group)
      if (prev) {
        throw new Error(`configGroups: "${group}" is claimed by ${prev.path} and ${route.path}`)
      }
      owners.set(group, route)
    }
  }
  return owners
}

const OWNERS = configGroupOwners()

/** The route whose settings sheet edits `group`, or undefined if it stays in Settings. */
export function routeForConfigGroup(group: string): RouteManifestEntry | undefined {
  return OWNERS.get(group)
}

export interface MovedGroup {
  group: string
  route: RouteManifestEntry
}

/** Split schema groups (in schema order) into those Settings keeps and those that moved. */
export function splitConfigGroups(groups: string[]): { stays: string[]; moved: MovedGroup[] } {
  const stays: string[] = []
  const moved: MovedGroup[] = []
  for (const group of groups) {
    const route = OWNERS.get(group)
    if (route) moved.push({ group, route })
    else stays.push(group)
  }
  return { stays, moved }
}

export interface MovedField<F> {
  field: F
  route: RouteManifestEntry
}

/**
 * Partition settings-search matches: fields Settings still edits, and fields
 * whose group moved, each paired with the route that now edits it.
 */
export function partitionSearchResults<F extends { group: string }>(
  fields: F[],
): { local: F[]; moved: MovedField<F>[] } {
  const local: F[] = []
  const moved: MovedField<F>[] = []
  for (const field of fields) {
    const route = OWNERS.get(field.group)
    if (route) moved.push({ field, route })
    else local.push(field)
  }
  return { local, moved }
}

/** Link that lands on the route with its settings sheet open. */
export function settingsHref(route: RouteManifestEntry): string {
  return `${route.path}?${SETTINGS_PARAM}=1`
}

/** Message keys for the hub and tab a moved group lives under ("Moved to Hub › Tab"). */
export function movedLabelKeys(route: RouteManifestEntry): { hubKey: MessageKey; tabKey: MessageKey } {
  const hub = HUB_MANIFEST.find((h) => h.id === route.hub)
  return { hubKey: hub?.titleKey ?? route.titleKey, tabKey: route.tabKey ?? route.titleKey }
}
