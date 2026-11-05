import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { ROUTE_MANIFEST } from './routeManifest.ts'
import {
  SETTINGS_PARAM,
  configGroupOwners,
  humanizeGroup,
  movedLabelKeys,
  partitionSearchResults,
  routeForConfigGroup,
  settingsHref,
  splitConfigGroups,
} from './configGroups.ts'

// Hardcoded on purpose: dropping a group from a route would silently send its
// fields back to Settings (or nowhere), and deriving this from the manifest
// would let that slip through.
const EXPECTED = {
  '/agent/models': ['model'],
  '/agent/roles': ['roles', 'delegation'],
  '/agent/memory': ['memory', 'rag'],
  '/capabilities/tools': ['tools', 'terminal', 'code_execution', 'tool_loop_guardrails'],
  '/capabilities/skills': ['skills'],
  '/capabilities/mcp': ['mcp'],
  '/capabilities/plugins': ['plugins'],
  '/automation/schedules': ['cron'],
  '/automation/autopilot': ['autopilot'],
  '/automation/channels': ['gateway'],
  '/security/engagement': ['security', 'osint'],
  '/security/proxies': ['proxies'],
  '/studio/creator': ['image_gen', 'video_gen'],
  '/studio/social': ['social'],
}

// Global settings that must stay on the Settings page.
const STAYS = [
  'general',
  'database',
  'server',
  'agent',
  'compression',
  'prompt_caching',
  'session_reset',
  'streaming',
  'display',
  'logging',
]

/** Top-level struct fields of Go's Config: the groups schema.go emits. */
function goSchemaGroups() {
  const here = dirname(fileURLToPath(import.meta.url))
  const src = readFileSync(join(here, '../../../internal/config/config.go'), 'utf8')
  const body = src.slice(src.indexOf('type Config struct {'))
  const block = body.slice(0, body.indexOf('\n}\n'))
  const groups = []
  for (const m of block.matchAll(/^\t\w+\s+(\S+)\s+`yaml:"(\w+)"/gm)) {
    const [, type, tag] = m
    // A root struct names its own group; maps and bare scalars fall under "general".
    if (/^[A-Z]/.test(type)) groups.push(tag)
  }
  return groups
}

describe('configGroups manifest', () => {
  test('each group belongs to at most one route', () => {
    const seen = new Map()
    for (const route of ROUTE_MANIFEST) {
      for (const group of route.configGroups ?? []) {
        expect(seen.get(group)).toBeUndefined()
        seen.set(group, route.path)
      }
    }
    expect(() => configGroupOwners()).not.toThrow()
  })

  test('a duplicate claim throws', () => {
    const dup = [
      { id: 'a', path: '/a', hub: 'system', titleKey: 'nav.chat', configGroups: ['cron'] },
      { id: 'b', path: '/b', hub: 'system', titleKey: 'nav.chat', configGroups: ['cron'] },
    ]
    expect(() => configGroupOwners(dup)).toThrow(/cron/)
  })

  test('every route carries exactly its expected groups', () => {
    for (const [path, groups] of Object.entries(EXPECTED)) {
      const route = ROUTE_MANIFEST.find((r) => r.path === path)
      expect(route).toBeDefined()
      expect(route.configGroups).toEqual(groups)
    }
    const extra = ROUTE_MANIFEST.filter((r) => r.configGroups?.length && !(r.path in EXPECTED))
    expect(extra.map((r) => r.path)).toEqual([])
  })

  test('routes with settings render the standard header', () => {
    for (const route of ROUTE_MANIFEST.filter((r) => r.configGroups?.length)) {
      expect(route.fullBleed).toBeFalsy()
    }
  })

  test('every claimed group exists in the Go schema', () => {
    const real = new Set([...goSchemaGroups(), 'general'])
    expect(real.size).toBeGreaterThan(20)
    for (const route of ROUTE_MANIFEST) {
      for (const group of route.configGroups ?? []) expect(real.has(group)).toBe(true)
    }
    for (const group of STAYS) expect(real.has(group)).toBe(true)
  })

  test('every Go schema group is either moved or listed as staying', () => {
    for (const group of goSchemaGroups()) {
      const moved = routeForConfigGroup(group) !== undefined
      expect(moved || STAYS.includes(group)).toBe(true)
    }
  })

  test('global groups stay in Settings', () => {
    for (const group of STAYS) expect(routeForConfigGroup(group)).toBeUndefined()
  })
})

describe('splitConfigGroups', () => {
  test('keeps schema order and pairs moved groups with their route', () => {
    const { stays, moved } = splitConfigGroups(['model', 'general', 'cron', 'server', 'gateway'])
    expect(stays).toEqual(['general', 'server'])
    expect(moved.map((m) => [m.group, m.route.path])).toEqual([
      ['model', '/agent/models'],
      ['cron', '/automation/schedules'],
      ['gateway', '/automation/channels'],
    ])
  })

  test('an unknown group stays in Settings', () => {
    expect(splitConfigGroups(['brand_new']).stays).toEqual(['brand_new'])
  })
})

describe('settings search mapping', () => {
  const fields = [
    { path: 'cron.enabled', group: 'cron' },
    { path: 'cron.timezone', group: 'cron' },
    { path: 'server.port', group: 'server' },
    { path: 'osint.google_cookie', group: 'osint' },
  ]

  test('moved fields point at the route that edits them', () => {
    const { local, moved } = partitionSearchResults(fields)
    expect(local.map((f) => f.path)).toEqual(['server.port'])
    expect(moved.map((m) => [m.field.path, m.route.path])).toEqual([
      ['cron.enabled', '/automation/schedules'],
      ['cron.timezone', '/automation/schedules'],
      ['osint.google_cookie', '/security/engagement'],
    ])
  })

  test('the link opens the settings sheet', () => {
    const route = routeForConfigGroup('cron')
    expect(settingsHref(route)).toBe(`/automation/schedules?${SETTINGS_PARAM}=1`)
  })

  test('the label names hub and tab', () => {
    expect(movedLabelKeys(routeForConfigGroup('cron'))).toEqual({
      hubKey: 'hub.automation',
      tabKey: 'tab.schedules',
    })
    expect(movedLabelKeys(routeForConfigGroup('model'))).toEqual({
      hubKey: 'hub.agent',
      tabKey: 'nav.models',
    })
  })
})

describe('humanizeGroup', () => {
  test('capitalises and spells out acronyms', () => {
    expect(humanizeGroup('prompt_caching')).toBe('Prompt caching')
    expect(humanizeGroup('rag')).toBe('RAG')
    expect(humanizeGroup('tool_loop_guardrails')).toBe('Tool loop guardrails')
  })
})
