import type { ModuleId } from './modules'

/**
 * How active modules shape navigation. Pure so the sidebar rules can be tested
 * without React; the shell passes its HubDef list, tests pass plain objects.
 */
export interface ModuleHub {
  id: string
  module?: ModuleId
}

/**
 * Hubs the sidebar shows. A hub without a module (core and system tiers) is
 * always shown. Before the module set has loaded everything is shown, so an
 * existing install never sees entries vanish and reappear. The hub the user
 * is on stays visible even when its module is off, so a deep link into a
 * hidden page still shows where they are.
 */
export function visibleHubs<H extends ModuleHub>(
  hubs: readonly H[],
  active: ReadonlySet<ModuleId>,
  loaded: boolean,
  currentHubId?: string,
): H[] {
  if (!loaded) return [...hubs]
  return hubs.filter((h) => !h.module || active.has(h.module) || h.id === currentHubId)
}

/**
 * The module to offer turning on for the hub being viewed, or undefined when
 * the hub has no module, the module is on, or the set has not loaded yet.
 */
export function offModuleOf(
  hub: ModuleHub | undefined,
  active: ReadonlySet<ModuleId>,
  loaded: boolean,
): ModuleId | undefined {
  if (!loaded || !hub?.module) return undefined
  return active.has(hub.module) ? undefined : hub.module
}

/** Hubs owned by a module, in the order given. */
export function hubsOfModule<H extends ModuleHub>(hubs: readonly H[], module: ModuleId): H[] {
  return hubs.filter((h) => h.module === module)
}

/** Hubs a set of modules adds to the sidebar, e.g. for a preset card. */
export function hubsOfModules<H extends ModuleHub>(
  hubs: readonly H[],
  modules: readonly ModuleId[],
): H[] {
  return hubs.filter((h) => h.module !== undefined && modules.includes(h.module))
}

/** `active` with `module` switched on or off; the input set is not modified. */
export function withModule(
  active: ReadonlySet<ModuleId>,
  module: ModuleId,
  on: boolean,
): Set<ModuleId> {
  const next = new Set(active)
  if (on) next.add(module)
  else next.delete(module)
  return next
}
