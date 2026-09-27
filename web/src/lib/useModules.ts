import { useEffect, useSyncExternalStore } from 'react'
import { ApiError, get, post } from './api'
import { type ModuleId, type PresetLabel, presetFor, resolveModules, toModuleList } from './modules'

/**
 * Shared module state for the sidebar, command palette, and Settings. One
 * module-level store so a toggle in Settings updates the sidebar immediately
 * without a reload.
 *
 * Server contract:
 *   GET  /api/ui/modules → { modules: string[] | null, preset: string }
 *   POST /api/ui/modules   { modules: string[], preset: string } → same shape
 * `modules: null` means the key is absent from config.yaml (treated as all
 * modules on).
 */

interface ModulesResponse {
  modules: string[] | null
  preset: string
}

export interface ModulesState {
  active: ReadonlySet<ModuleId>
  preset: PresetLabel
  loaded: boolean
}

let state: ModulesState = { active: resolveModules(null), preset: 'full', loaded: false }
let inflight: Promise<void> | null = null
const listeners = new Set<() => void>()

function publish(next: ModulesState) {
  state = next
  for (const l of listeners) l()
}

function fromResponse(res: ModulesResponse): ModulesState {
  const active = resolveModules(res.modules)
  return { active, preset: presetFor(active), loaded: true }
}

function load(): Promise<void> {
  if (inflight) return inflight
  inflight = get<ModulesResponse>('/api/ui/modules')
    .then((res) => publish(fromResponse(res)))
    .catch((err) => {
      // An older server without the endpoint: keep everything visible.
      if (err instanceof ApiError && err.status === 404) {
        publish({ ...state, loaded: true })
        return
      }
      // Fail open — hiding navigation because a request failed would strand
      // the user. Mark loaded so callers stop waiting.
      // eslint-disable-next-line no-console
      console.error('[modules] failed to load module settings', err)
      publish({ ...state, loaded: true })
    })
    .finally(() => {
      inflight = null
    })
  return inflight
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/** Persist a new module set. Rejects with the server error; state is unchanged on failure. */
export async function saveModules(active: ReadonlySet<ModuleId>): Promise<void> {
  const res = await post<ModulesResponse>('/api/ui/modules', {
    modules: toModuleList(active),
    preset: presetFor(active),
  })
  publish(fromResponse(res))
}

/** Re-read from the server, e.g. after Setup completes. */
export function reloadModules(): Promise<void> {
  return load()
}

export function useModules(): ModulesState {
  const snapshot = useSyncExternalStore(subscribe, () => state)
  useEffect(() => {
    if (!state.loaded) void load()
  }, [])
  return snapshot
}

/** Test hook: reset the store between cases. */
export function __resetModulesForTest(next?: Partial<ModulesState>) {
  state = { active: resolveModules(null), preset: 'full', loaded: false, ...next }
  inflight = null
}
