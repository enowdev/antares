import { describe, expect, test } from 'bun:test'
import { hubsOfModule, hubsOfModules, offModuleOf, visibleHubs, withModule } from './moduleNav.ts'
import { HUB_MANIFEST } from './routeManifest.ts'
import { MODULE_IDS, PRESET_IDS, PRESET_MODULES } from './modules.ts'

const ids = (hubs) => hubs.map((h) => h.id)
const coreAndSystem = HUB_MANIFEST.filter((h) => !h.module).map((h) => h.id)

describe('visibleHubs', () => {
  test('hides hubs whose module is off', () => {
    const shown = ids(visibleHubs(HUB_MANIFEST, new Set(['automation']), true))
    expect(shown).toContain('automation')
    expect(shown).not.toContain('security')
    expect(shown).not.toContain('studio')
  })

  test('shows everything before the module set has loaded', () => {
    expect(ids(visibleHubs(HUB_MANIFEST, new Set(), false))).toEqual(ids(HUB_MANIFEST))
  })

  test('keeps the current hub visible even when its module is off', () => {
    const shown = ids(visibleHubs(HUB_MANIFEST, new Set(), true, 'security'))
    expect(shown).toContain('security')
    expect(shown).not.toContain('automation')
    expect(shown).not.toContain('studio')
  })

  test('never hides core or system hubs', () => {
    expect(coreAndSystem).toContain('chat')
    expect(coreAndSystem).toContain('system')
    for (const preset of PRESET_IDS) {
      const shown = ids(visibleHubs(HUB_MANIFEST, new Set(PRESET_MODULES[preset]), true))
      for (const id of coreAndSystem) expect(shown).toContain(id)
    }
  })

  test('sidebar sizes match the design table for each preset', () => {
    const expected = { general: 5, coding: 6, security: 7, creator: 7, full: 8 }
    for (const preset of PRESET_IDS) {
      expect(visibleHubs(HUB_MANIFEST, new Set(PRESET_MODULES[preset]), true).length).toBe(
        expected[preset],
      )
    }
  })

  test('keeps manifest order', () => {
    const shown = ids(visibleHubs(HUB_MANIFEST, new Set(MODULE_IDS), true))
    expect(shown).toEqual(ids(HUB_MANIFEST))
  })
})

describe('offModuleOf', () => {
  const security = HUB_MANIFEST.find((h) => h.id === 'security')
  const chat = HUB_MANIFEST.find((h) => h.id === 'chat')

  test('reports the module of a hub that is off', () => {
    expect(offModuleOf(security, new Set(['automation']), true)).toBe('security')
  })

  test('is silent when the module is on, the hub has none, or nothing has loaded', () => {
    expect(offModuleOf(security, new Set(['security']), true)).toBeUndefined()
    expect(offModuleOf(chat, new Set(), true)).toBeUndefined()
    expect(offModuleOf(security, new Set(), false)).toBeUndefined()
    expect(offModuleOf(undefined, new Set(), true)).toBeUndefined()
  })
})

describe('module helpers', () => {
  test('every module owns at least one hub', () => {
    for (const m of MODULE_IDS) expect(hubsOfModule(HUB_MANIFEST, m).length).toBeGreaterThan(0)
  })

  test('a preset adds exactly the hubs of its modules', () => {
    expect(ids(hubsOfModules(HUB_MANIFEST, PRESET_MODULES.general))).toEqual([])
    expect(ids(hubsOfModules(HUB_MANIFEST, PRESET_MODULES.creator))).toEqual(['automation', 'studio'])
  })

  test('withModule returns a new set', () => {
    const base = new Set(['automation'])
    const on = withModule(base, 'security', true)
    expect([...on].sort()).toEqual(['automation', 'security'])
    expect([...withModule(on, 'automation', false)]).toEqual(['security'])
    expect([...base]).toEqual(['automation'])
  })
})
