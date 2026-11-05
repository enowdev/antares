import { describe, expect, test } from 'bun:test'
import {
  buildActionItems,
  buildPageItems,
  buildSessionItems,
  flattenGroups,
  fuzzyScore,
  rankItems,
  scoreItem,
} from './palette.ts'
import { HUB_MANIFEST, tabsOf } from './routeManifest.ts'

// A small fake UI language, so labels and English keywords differ and the
// tests prove matching works on both.
const FAKE = {
  'hub.chat': 'Obrolan',
  'hub.automation': 'Otomasi',
  'hub.studio': 'Studio',
  'hub.system': 'Sistem',
  'tab.schedules': 'Jadwal',
  'tab.board': 'Papan',
  'tab.creator': 'Kreator',
  'tab.settings': 'Pengaturan',
  'tab.status': 'Status',
  'page.chat': 'Obrolan',
  'page.cron': 'Tugas terjadwal',
  'page.board': 'Papan',
  'page.creator': 'Kreator konten',
  'page.config': 'Pengaturan',
  'page.system': 'Status sistem',
  'palette.newChat': 'Obrolan baru',
  'palette.themeLight': 'Tema terang',
  'palette.themeDark': 'Tema gelap',
  'palette.goModels': 'Pengaturan model',
}
const EN = {
  'hub.chat': 'Chat',
  'hub.automation': 'Automation',
  'hub.studio': 'Studio',
  'hub.system': 'System',
  'tab.schedules': 'Schedules',
  'tab.board': 'Board',
  'tab.creator': 'Creator',
  'tab.settings': 'Settings',
  'tab.status': 'Status',
  'page.chat': 'Chat',
  'page.cron': 'Scheduled jobs',
  'page.board': 'Board',
  'page.creator': 'Content Creator',
  'page.config': 'Settings',
  'page.system': 'System status',
  'palette.newChat': 'New chat',
  'palette.themeLight': 'Switch to light theme',
  'palette.themeDark': 'Switch to dark theme',
  'palette.goModels': 'Go to model settings',
  'theme.toggle': 'Toggle theme',
}
const t = (k) => FAKE[k] ?? k
const en = (k) => EN[k] ?? k

const HUBS = [
  {
    id: 'chat',
    path: '/',
    titleKey: 'hub.chat',
    tabs: [{ id: 'chat', path: '/', titleKey: 'page.chat' }],
  },
  {
    id: 'automation',
    path: '/automation',
    titleKey: 'hub.automation',
    module: 'automation',
    tabs: [
      { id: 'cron', path: '/automation/schedules', titleKey: 'page.cron', tabKey: 'tab.schedules', legacyPaths: ['/cron'] },
      { id: 'board', path: '/automation/board', titleKey: 'page.board', tabKey: 'tab.board', legacyPaths: ['/board'] },
    ],
  },
  {
    id: 'studio',
    path: '/studio',
    titleKey: 'hub.studio',
    module: 'studio',
    tabs: [
      {
        id: 'content-creator',
        path: '/studio/creator',
        titleKey: 'page.creator',
        tabKey: 'tab.creator',
        legacyPaths: ['/content-creator'],
      },
    ],
  },
  {
    id: 'system',
    path: '/system',
    titleKey: 'hub.system',
    tabs: [
      { id: 'system', path: '/system/status', titleKey: 'page.system', tabKey: 'tab.status', legacyPaths: ['/system'] },
      { id: 'config', path: '/system/settings', titleKey: 'page.config', tabKey: 'tab.settings', legacyPaths: ['/config'] },
    ],
  },
]

const ALL_ON = new Set(['automation', 'security', 'studio'])
const pages = (active = ALL_ON) => buildPageItems(HUBS, { t, en, activeModules: active })
const topLabel = (items, q) => flattenGroups(rankItems(items, q))[0]?.label

describe('fuzzyScore', () => {
  test('is case-insensitive and rejects characters out of order', () => {
    expect(fuzzyScore('SCHED', 'Schedules')).not.toBeNull()
    expect(fuzzyScore('hdls', 'Schedules')).not.toBeNull()
    expect(fuzzyScore('xyz', 'Schedules')).toBeNull()
    expect(fuzzyScore('sd', 'ds')).toBeNull()
  })

  test('ranks exact > prefix > word start > inner substring > subsequence', () => {
    const exact = fuzzyScore('logs', 'Logs')
    const prefix = fuzzyScore('log', 'Logs')
    const wordStart = fuzzyScore('log', 'System › Logs')
    const inner = fuzzyScore('log', 'Catalogue')
    const subseq = fuzzyScore('lgs', 'Logs')
    expect(exact).toBeGreaterThan(prefix)
    expect(prefix).toBeGreaterThan(wordStart)
    expect(wordStart).toBeGreaterThan(inner)
    expect(inner).toBeGreaterThan(subseq)
    expect(subseq).toBeGreaterThan(0)
  })

  test('a subsequence hitting word starts beats one scattered mid-word', () => {
    expect(fuzzyScore('as', 'Automation › Schedules')).toBeGreaterThan(fuzzyScore('as', 'Parties'))
  })

  test('shorter text wins within the same tier', () => {
    expect(fuzzyScore('auto', 'Automation')).toBeGreaterThan(fuzzyScore('auto', 'Automation › Autopilot'))
  })

  test('an empty query matches everything with a neutral score', () => {
    expect(fuzzyScore('', 'anything')).toBe(0)
  })
})

describe('buildPageItems', () => {
  test('lists every hub and every tab, labelled "Hub › Tab" in the UI language', () => {
    const items = pages()
    expect(items.map((i) => i.label)).toEqual([
      'Obrolan',
      'Otomasi',
      'Otomasi › Jadwal',
      'Otomasi › Papan',
      'Studio',
      'Sistem',
      'Sistem › Status',
      'Sistem › Pengaturan',
    ])
  })

  test('a multi-tab hub item opens its first tab; single-page hubs are one item', () => {
    const items = pages()
    expect(items.find((i) => i.id === 'hub:automation')?.to).toBe('/automation/schedules')
    expect(items.find((i) => i.id === 'page:content-creator')?.to).toBe('/studio/creator')
    expect(items.some((i) => i.id === 'hub:studio')).toBe(false)
  })

  test('hubs whose module is off are still listed, flagged, and still navigate', () => {
    const items = pages(new Set(['automation']))
    const studio = items.find((i) => i.hubId === 'studio')
    expect(studio?.off).toBe(true)
    expect(studio?.to).toBe('/studio/creator')
    expect(items.filter((i) => i.hubId === 'automation').every((i) => !i.off)).toBe(true)
    // Hubs without a module are never off.
    expect(items.filter((i) => i.hubId === 'system').every((i) => !i.off)).toBe(true)
    // Off items still match a search.
    expect(flattenGroups(rankItems(items, 'creator')).map((i) => i.id)).toContain('page:content-creator')
  })
})

describe('legacy and English matching', () => {
  test('"cron" finds Schedules through its old path', () => {
    expect(topLabel(pages(), 'cron')).toBe('Otomasi › Jadwal')
  })

  test('"config" finds Settings through its old path', () => {
    expect(topLabel(pages(), 'config')).toBe('Sistem › Pengaturan')
  })

  test('English titles match when the UI is in another language', () => {
    expect(topLabel(pages(), 'schedules')).toBe('Otomasi › Jadwal')
    expect(topLabel(pages(), 'content creator')).toBe('Studio')
  })

  test('a keyword hit ranks below the same hit on the visible label', () => {
    const onLabel = { id: 'a', kind: 'page', label: 'Board', keywords: [] }
    const onKeyword = { id: 'b', kind: 'page', label: 'Papan', keywords: ['Board'] }
    expect(scoreItem(onLabel, 'board')).toBeGreaterThan(scoreItem(onKeyword, 'board'))
    expect(topLabel([onKeyword, onLabel], 'board')).toBe('Board')
  })

  test('every word of a multi-word query must match', () => {
    const hits = flattenGroups(rankItems(pages(), 'otomasi papan')).map((i) => i.id)
    expect(hits[0]).toBe('page:board')
    expect(hits).not.toContain('page:cron')
  })
})

describe('rankItems', () => {
  const actions = buildActionItems({ t, en, theme: 'dark' })
  const sessions = buildSessionItems(
    [
      { id: 'a', title: 'Old chat', updated_at: '2026-09-01T10:00:00Z' },
      { id: 'b', title: '', updated_at: '2026-09-27T10:00:00Z' },
    ],
    { untitled: 'Untitled' },
  )

  test('an empty query keeps actions, then pages, then sessions, in order', () => {
    const groups = rankItems([...sessions, ...pages(), ...actions], '')
    expect(groups.map((g) => g.kind)).toEqual(['action', 'page', 'session'])
    expect(groups[1].items[0].label).toBe('Obrolan')
  })

  test('with a query, groups follow their best match', () => {
    const groups = rankItems([...actions, ...pages(), ...sessions], 'old chat')
    expect(groups[0].kind).toBe('session')
    expect(groups[0].items[0].to).toBe('/c/a')
  })

  test('hidden keywords need a substring hit, not a scattered subsequence', () => {
    // "cron" is a subsequence of the action's "conversation" keyword.
    const hits = flattenGroups(rankItems([...actions, ...pages()], 'cron')).map((i) => i.id)
    expect(hits).toEqual(['page:cron'])
  })

  test('no match returns no groups', () => {
    expect(rankItems([...actions, ...pages()], 'zzzz')).toEqual([])
  })
})

describe('actions and sessions', () => {
  test('the theme action names the theme it switches to', () => {
    expect(buildActionItems({ t, en, theme: 'dark' })[1].label).toBe('Tema terang')
    expect(buildActionItems({ t, en, theme: 'light' })[1].label).toBe('Tema gelap')
  })

  test('model settings go to /agent/models', () => {
    const models = buildActionItems({ t, en, theme: 'dark' }).find((a) => a.action === 'models')
    expect(models?.to).toBe('/agent/models')
    expect(topLabel([models], 'switch model')).toBe('Pengaturan model')
  })

  test('sessions: newest first, capped, untitled fallback, open the chat', () => {
    const many = Array.from({ length: 12 }, (_, i) => ({
      id: `s${i}`,
      title: `Chat ${i}`,
      updated_at: `2026-09-${String(i + 10).padStart(2, '0')}T00:00:00Z`,
    }))
    many.push({ id: 'blank', title: '  ', updated_at: '2026-09-30T00:00:00Z' })
    const items = buildSessionItems(many, { untitled: 'Untitled', timeAgo: () => 'now' })
    expect(items).toHaveLength(8)
    expect(items[0]).toMatchObject({ label: 'Untitled', to: '/c/blank', hint: 'now', kind: 'session' })
    expect(items[1].label).toBe('Chat 11')
  })
})

describe('real manifest', () => {
  test('every hub and tab becomes an item, and cron/config resolve', () => {
    const hubs = HUB_MANIFEST.map((h) => ({ ...h, tabs: tabsOf(h.id) }))
    const id = (k) => k
    const items = buildPageItems(hubs, { t: id, en: id, activeModules: new Set() })
    const tabs = hubs.reduce((n, h) => n + h.tabs.length, 0)
    const multi = hubs.filter((h) => h.tabs.length > 1).length
    expect(items).toHaveLength(tabs + multi)
    expect(new Set(items.map((i) => i.id)).size).toBe(items.length)
    expect(flattenGroups(rankItems(items, 'cron'))[0].to).toBe('/automation/schedules')
    expect(flattenGroups(rankItems(items, 'config'))[0].to).toBe('/system/settings')
    // No modules on: every module hub is flagged, core hubs are not.
    expect(items.filter((i) => i.off).every((i) => ['automation', 'security', 'studio'].includes(i.hubId))).toBe(true)
    expect(items.some((i) => i.hubId === 'security' && i.off)).toBe(true)
  })
})
