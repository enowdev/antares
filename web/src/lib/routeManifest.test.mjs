import { describe, expect, test } from 'bun:test'
import {
  HUB_MANIFEST,
  ROUTE_MANIFEST,
  allRedirects,
  entryFor,
  hubFor,
  hubRootRedirects,
  legacyRedirects,
  smokeRedirects,
  tabsOf,
} from './routeManifest.ts'
import { MODULE_IDS } from './modules.ts'

// Every pathname the dashboard served before hubs existed. Hardcoded on
// purpose: an edit that drops one of these breaks someone's bookmark, and
// deriving the list from the manifest would let that slip through.
const PRE_HUB_PATHS = [
  '/',
  '/c/:sessionId',
  '/sessions',
  '/providers',
  '/models',
  '/tools',
  '/memory',
  '/roles',
  '/soul',
  '/skills',
  '/cron',
  '/channels',
  '/autopilot',
  '/engagement',
  '/board',
  '/intercept',
  '/mcp',
  '/plugins',
  '/proxies',
  '/vps',
  '/analytics',
  '/files',
  '/logs',
  '/config',
  '/system',
  '/content-creator',
  '/social-media',
]

const hubIds = new Set(HUB_MANIFEST.map((h) => h.id))
const canonical = new Set(ROUTE_MANIFEST.map((r) => r.path))

describe('hubs', () => {
  test('hub ids are unique', () => {
    expect(hubIds.size).toBe(HUB_MANIFEST.length)
  })

  test('every route belongs to a known hub', () => {
    for (const r of ROUTE_MANIFEST) expect(hubIds).toContain(r.hub)
  })

  test('every hub has at least one route', () => {
    for (const h of HUB_MANIFEST) expect(tabsOf(h.id).length).toBeGreaterThan(0)
  })

  test('route paths nest under their hub', () => {
    for (const r of ROUTE_MANIFEST) {
      const hub = HUB_MANIFEST.find((h) => h.id === r.hub)
      if (tabsOf(r.hub).length === 1) {
        // Single-page hubs (chat, sessions) are the page itself.
        expect(r.path).toBe(hub.path)
      } else {
        expect(r.path.startsWith(hub.path + '/')).toBe(true)
      }
    }
  })

  test('multi-tab hubs label every tab', () => {
    for (const h of HUB_MANIFEST) {
      const tabs = tabsOf(h.id)
      if (tabs.length > 1) for (const r of tabs) expect(r.tabKey).toBeDefined()
    }
  })

  test('hub modules are known module ids', () => {
    for (const h of HUB_MANIFEST) {
      if (h.module !== undefined) expect(MODULE_IDS).toContain(h.module)
    }
  })

  test('the mobile bar gets at most one primary route per hub', () => {
    for (const h of HUB_MANIFEST) {
      expect(tabsOf(h.id).filter((r) => r.primary).length).toBeLessThanOrEqual(1)
    }
  })
})

describe('paths', () => {
  test('paths, aliases, and legacy paths are globally unique', () => {
    const all = ROUTE_MANIFEST.flatMap((r) => [r.path, ...(r.aliases ?? []), ...(r.legacyPaths ?? [])])
    const dupes = all.filter((p, i) => all.indexOf(p) !== i)
    expect(dupes).toEqual([])
  })

  test('no redirect source shadows a live route', () => {
    const live = new Set(ROUTE_MANIFEST.flatMap((r) => [r.path, ...(r.aliases ?? [])]))
    for (const { from } of allRedirects()) expect(live.has(from)).toBe(false)
  })

  test('every pre-hub path still resolves', () => {
    const known = new Set([
      ...ROUTE_MANIFEST.flatMap((r) => [r.path, ...(r.aliases ?? [])]),
      ...allRedirects().map((r) => r.from),
    ])
    const missing = PRE_HUB_PATHS.filter((p) => !known.has(p))
    expect(missing).toEqual([])
  })
})

describe('redirects', () => {
  test('legacy redirects land on canonical paths', () => {
    for (const { to } of legacyRedirects()) expect(canonical.has(to)).toBe(true)
  })

  test('a bare multi-tab hub root lands on its first tab', () => {
    expect(hubRootRedirects()).toContainEqual({ from: '/agent', to: '/agent/models' })
    expect(hubRootRedirects()).toContainEqual({ from: '/system', to: '/system/status' })
    // Single-page hubs are their own page and need no redirect.
    expect(hubRootRedirects().some((r) => r.from === '/' || r.from === '/sessions')).toBe(false)
  })

  test('merged redirects list each source once', () => {
    const froms = allRedirects().map((r) => r.from)
    expect(new Set(froms).size).toBe(froms.length)
  })

  test('smoke redirect destinations are all canonical paths', () => {
    const redirects = smokeRedirects()
    expect(redirects.length).toBeGreaterThan(0)
    for (const { to } of redirects) expect(canonical.has(to)).toBe(true)
  })

  test('design mapping holds for representative old paths', () => {
    const map = Object.fromEntries(allRedirects().map((r) => [r.from, r.to]))
    expect(map['/providers']).toBe('/agent/models')
    expect(map['/models']).toBe('/agent/models')
    expect(map['/cron']).toBe('/automation/schedules')
    expect(map['/config']).toBe('/system/settings')
    expect(map['/content-creator']).toBe('/studio/creator')
    expect(map['/social-media']).toBe('/studio/social')
  })
})

describe('lookup', () => {
  test('hubFor resolves tabs, aliases, and bare hub roots', () => {
    expect(hubFor('/')).toBe('chat')
    expect(hubFor('/c/abc')).toBe('chat')
    expect(hubFor('/sessions')).toBe('sessions')
    expect(hubFor('/agent/soul')).toBe('agent')
    expect(hubFor('/agent')).toBe('agent')
    expect(hubFor('/system/settings')).toBe('system')
    expect(hubFor('/nope')).toBeUndefined()
  })

  test('entryFor does not match legacy paths', () => {
    expect(entryFor('/cron')).toBeUndefined()
    expect(entryFor('/automation/schedules')?.id).toBe('cron')
  })
})
