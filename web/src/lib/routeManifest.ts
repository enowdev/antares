import type { MessageKey } from './i18n'
import type { ModuleId } from './modules'

/**
 * Pure-data route manifest. No React, no icons, no lazy components — safe to
 * import from build scripts, smoke tests, or any pre-render tool that just
 * needs to know which paths exist and how the shell should frame them.
 *
 * Runtime routing (icons + lazy components) is composed in {@link ./routes.ts}
 * by mapping this list. Keep both files in sync by editing the manifest here;
 * routes.ts merely attaches the React bits by page id.
 */
export type HubId =
  | 'chat'
  | 'sessions'
  | 'agent'
  | 'capabilities'
  | 'automation'
  | 'security'
  | 'studio'
  | 'system'

/**
 * A sidebar entry. Pages that share a hub render under one header with a tab
 * strip; the tabs are the manifest entries carrying that hub id, in manifest
 * order.
 */
export interface HubManifestEntry {
  id: HubId
  /** Root pathname. Multi-tab hubs redirect it to their first tab. */
  path: string
  titleKey: MessageKey
  /** Sidebar grouping: `system` sits below a separator at the bottom. */
  tier: 'core' | 'module' | 'system'
  /** Optional module that owns this hub. Phase 2 hides hubs whose module is off. */
  module?: ModuleId
}

/** Sidebar order. */
export const HUB_MANIFEST: HubManifestEntry[] = [
  { id: 'chat', path: '/', titleKey: 'nav.chat', tier: 'core' },
  { id: 'sessions', path: '/sessions', titleKey: 'nav.sessions', tier: 'core' },
  { id: 'agent', path: '/agent', titleKey: 'hub.agent', tier: 'core' },
  { id: 'capabilities', path: '/capabilities', titleKey: 'hub.capabilities', tier: 'core' },
  { id: 'automation', path: '/automation', titleKey: 'hub.automation', tier: 'module', module: 'automation' },
  { id: 'security', path: '/security', titleKey: 'hub.security', tier: 'module', module: 'security' },
  { id: 'studio', path: '/studio', titleKey: 'hub.studio', tier: 'module', module: 'studio' },
  { id: 'system', path: '/system', titleKey: 'hub.system', tier: 'system' },
]

export interface RouteManifestEntry {
  /** Stable page id: routes.ts joins on this to attach icon + component. */
  id: string
  /** Primary pathname. */
  path: string
  /** Extra paths that render the same page (e.g. /c/:id for chat). */
  aliases?: string[]
  /**
   * Retired pathnames that redirect here, keeping search and hash. Unlike an
   * alias, the URL changes: old bookmarks land on the canonical path.
   */
  legacyPaths?: string[]
  hub: HubId
  /** Short label for the hub tab strip; hubs with one page have no tabs. */
  tabKey?: MessageKey
  titleKey: MessageKey
  descKey?: MessageKey
  /**
   * Shown in the mobile bottom bar, labelled by its hub. At most one entry per
   * hub; the bar adds a More button for everything else.
   */
  primary?: boolean
  /** Renders without the standard page container and header. */
  fullBleed?: boolean
  /**
   * Opt out of the default fixed-height frame: let this page size to its
   * content and scroll with the document instead of scrolling inside a fixed
   * container. For genuinely short, static pages.
   */
  staticHeight?: boolean
  /**
   * Top-level config.yaml keys (schema groups) edited from this page's
   * settings sheet instead of the Settings page. A group belongs to at most
   * one route; groups no route claims stay in Settings.
   */
  configGroups?: string[]
}

export const ROUTE_MANIFEST: RouteManifestEntry[] = [
  {
    id: 'chat',
    path: '/',
    aliases: ['/c/:sessionId'],
    hub: 'chat',
    titleKey: 'nav.chat',
    primary: true,
    fullBleed: true,
  },
  {
    id: 'sessions',
    path: '/sessions',
    hub: 'sessions',
    titleKey: 'sessions.title',
    descKey: 'sessions.desc',
    primary: true,
  },

  // Agent
  {
    id: 'providers',
    path: '/agent/models',
    legacyPaths: ['/providers', '/models'],
    hub: 'agent',
    tabKey: 'nav.models',
    titleKey: 'providers.title',
    descKey: 'providers.desc',
    configGroups: ['model'],
    primary: true,
  },
  {
    id: 'roles',
    path: '/agent/roles',
    legacyPaths: ['/roles'],
    hub: 'agent',
    tabKey: 'nav.roles',
    titleKey: 'roles.title',
    descKey: 'roles.desc',
    configGroups: ['roles', 'delegation'],
  },
  {
    id: 'soul',
    path: '/agent/soul',
    legacyPaths: ['/soul'],
    hub: 'agent',
    tabKey: 'nav.soul',
    titleKey: 'soul.title',
    descKey: 'persona.desc',
    staticHeight: true,
  },
  {
    id: 'memory',
    path: '/agent/memory',
    legacyPaths: ['/memory'],
    hub: 'agent',
    tabKey: 'nav.memory',
    titleKey: 'memory.title',
    descKey: 'memory.desc',
    configGroups: ['memory', 'rag'],
  },

  // Capabilities
  {
    id: 'tools',
    path: '/capabilities/tools',
    legacyPaths: ['/tools'],
    hub: 'capabilities',
    tabKey: 'nav.tools',
    titleKey: 'tools.title',
    descKey: 'tools.desc',
    configGroups: ['tools', 'terminal', 'code_execution', 'tool_loop_guardrails'],
  },
  {
    id: 'skills',
    path: '/capabilities/skills',
    legacyPaths: ['/skills'],
    hub: 'capabilities',
    tabKey: 'nav.skills',
    titleKey: 'skills.title',
    descKey: 'skills.desc',
    configGroups: ['skills'],
  },
  {
    id: 'mcp',
    path: '/capabilities/mcp',
    legacyPaths: ['/mcp'],
    hub: 'capabilities',
    tabKey: 'nav.mcp',
    titleKey: 'mcp.title',
    descKey: 'mcp.desc',
    configGroups: ['mcp'],
  },
  {
    id: 'plugins',
    path: '/capabilities/plugins',
    legacyPaths: ['/plugins'],
    hub: 'capabilities',
    tabKey: 'nav.plugins',
    titleKey: 'plugins.title',
    descKey: 'plugins.desc',
    configGroups: ['plugins'],
  },

  // Automation
  {
    id: 'cron',
    path: '/automation/schedules',
    legacyPaths: ['/cron'],
    hub: 'automation',
    tabKey: 'tab.schedules',
    titleKey: 'cron.title',
    descKey: 'cron.desc',
    configGroups: ['cron'],
  },
  {
    id: 'autopilot',
    path: '/automation/autopilot',
    legacyPaths: ['/autopilot'],
    hub: 'automation',
    tabKey: 'nav.autopilot',
    titleKey: 'autopilot.title',
    descKey: 'autopilot.desc',
    configGroups: ['autopilot'],
  },
  {
    id: 'board',
    path: '/automation/board',
    legacyPaths: ['/board'],
    hub: 'automation',
    tabKey: 'nav.board',
    titleKey: 'board.title',
    descKey: 'board.desc',
  },
  {
    id: 'channels',
    path: '/automation/channels',
    legacyPaths: ['/channels'],
    hub: 'automation',
    tabKey: 'nav.channels',
    titleKey: 'channels.title',
    descKey: 'channels.desc',
    configGroups: ['gateway'],
  },

  // Security
  {
    id: 'engagement',
    path: '/security/engagement',
    legacyPaths: ['/engagement'],
    hub: 'security',
    tabKey: 'nav.engagement',
    titleKey: 'engagement.title',
    descKey: 'engagement.desc',
    configGroups: ['security', 'osint'],
  },
  {
    id: 'intercept',
    path: '/security/intercept',
    legacyPaths: ['/intercept'],
    hub: 'security',
    tabKey: 'nav.intercept',
    titleKey: 'intercept.title',
    descKey: 'intercept.desc',
  },
  {
    id: 'proxies',
    path: '/security/proxies',
    legacyPaths: ['/proxies'],
    hub: 'security',
    tabKey: 'nav.proxies',
    titleKey: 'proxies.title',
    descKey: 'proxies.desc',
    configGroups: ['proxies'],
  },
  {
    id: 'vps',
    path: '/security/vps',
    legacyPaths: ['/vps'],
    hub: 'security',
    tabKey: 'nav.vps',
    titleKey: 'vps.title',
    descKey: 'vps.desc',
  },

  // Studio
  {
    id: 'content-creator',
    path: '/studio/creator',
    legacyPaths: ['/content-creator'],
    hub: 'studio',
    tabKey: 'tab.creator',
    titleKey: 'creator.title',
    descKey: 'creator.desc',
    configGroups: ['image_gen', 'video_gen'],
  },
  {
    id: 'social-media',
    path: '/studio/social',
    legacyPaths: ['/social-media'],
    hub: 'studio',
    tabKey: 'tab.social',
    titleKey: 'social.title',
    descKey: 'social.desc',
    configGroups: ['social'],
  },

  // System
  {
    id: 'system',
    path: '/system/status',
    legacyPaths: ['/system'],
    hub: 'system',
    tabKey: 'tab.status',
    titleKey: 'system.title',
    descKey: 'system.desc',
  },
  {
    id: 'files',
    path: '/system/files',
    legacyPaths: ['/files'],
    hub: 'system',
    tabKey: 'nav.files',
    titleKey: 'files.title',
    descKey: 'files.desc',
  },
  {
    id: 'logs',
    path: '/system/logs',
    legacyPaths: ['/logs'],
    hub: 'system',
    tabKey: 'nav.logs',
    titleKey: 'logs.title',
    descKey: 'logs.desc',
  },
  {
    id: 'analytics',
    path: '/system/analytics',
    legacyPaths: ['/analytics'],
    hub: 'system',
    tabKey: 'nav.analytics',
    titleKey: 'analytics.title',
    descKey: 'analytics.desc',
  },
  {
    id: 'config',
    path: '/system/settings',
    legacyPaths: ['/config'],
    hub: 'system',
    tabKey: 'tab.settings',
    titleKey: 'config.title',
    descKey: 'config.desc',
  },
]

/**
 * The manifest entry that renders `pathname`: an exact canonical path, or an
 * alias matched by its static prefix (e.g. /c/<id>). Legacy paths do not
 * match; they only redirect.
 */
export function entryFor(pathname: string): RouteManifestEntry | undefined {
  const exact = ROUTE_MANIFEST.find((r) => r.path === pathname)
  if (exact) return exact
  return ROUTE_MANIFEST.find((r) =>
    r.aliases?.some((a) => {
      const prefix = a.split('/:')[0]
      return prefix !== '' && pathname.startsWith(prefix + '/')
    }),
  )
}

/**
 * The hub a pathname belongs to. Falls back to the hub whose root prefixes the
 * path, so a bare hub root (/agent) highlights its sidebar entry while it
 * redirects. "/" is never used as a prefix; it would match everything.
 */
export function hubFor(pathname: string): HubId | undefined {
  const entry = entryFor(pathname)
  if (entry) return entry.hub
  return HUB_MANIFEST.find(
    (h) => h.path !== '/' && (pathname === h.path || pathname.startsWith(h.path + '/')),
  )?.id
}

/** A hub's pages in tab order. */
export function tabsOf(hubId: HubId): RouteManifestEntry[] {
  return ROUTE_MANIFEST.filter((r) => r.hub === hubId)
}

export interface Redirect {
  from: string
  to: string
}

/** Every retired pathname and the canonical path it now lives at. */
export function legacyRedirects(): Redirect[] {
  return ROUTE_MANIFEST.flatMap((r) => (r.legacyPaths ?? []).map((from) => ({ from, to: r.path })))
}

/**
 * Bare roots of multi-tab hubs (/agent) and where they land: the first tab.
 * Single-page hubs are skipped because their root is the page itself.
 */
export function hubRootRedirects(): Redirect[] {
  const out: Redirect[] = []
  for (const hub of HUB_MANIFEST) {
    const tabs = tabsOf(hub.id)
    if (tabs.length > 1 && tabs[0].path !== hub.path) out.push({ from: hub.path, to: tabs[0].path })
  }
  return out
}

/**
 * Legacy and hub-root redirects merged, one per source path. A hub root can
 * also be a legacy path (/system was the status page and is now the System
 * hub); both agree on the destination, so it is listed once. Disagreement is
 * a manifest bug and throws.
 */
export function allRedirects(): Redirect[] {
  const byFrom = new Map<string, string>()
  for (const r of [...legacyRedirects(), ...hubRootRedirects()]) {
    const prev = byFrom.get(r.from)
    if (prev !== undefined && prev !== r.to) {
      throw new Error(`routeManifest: ${r.from} redirects to both ${prev} and ${r.to}`)
    }
    byFrom.set(r.from, r.to)
  }
  return [...byFrom].map(([from, to]) => ({ from, to }))
}

/**
 * Fixture segment substituted for alias parameters when producing smoke
 * targets. Stable so recorded runs diff cleanly across invocations.
 */
export const SMOKE_FIXTURE_SESSION_ID = 'smoke-session'

const SMOKE_FIXTURES: Record<string, string> = {
  sessionId: SMOKE_FIXTURE_SESSION_ID,
}

function materializePath(pattern: string): string {
  return pattern.replace(/:([A-Za-z0-9_]+)/g, (_, name: string) => {
    const value = SMOKE_FIXTURES[name]
    // Unknown parameter → fall back to the fixture id rather than leaving a
    // literal ":param" segment behind; smoke callers can override by extending
    // SMOKE_FIXTURES if they add a new param.
    return value ?? SMOKE_FIXTURE_SESSION_ID
  })
}

/**
 * Concrete pathnames a headless smoke run should visit: every canonical route,
 * every alias with parameters filled from fixtures, plus the standalone
 * onboarding surfaces that live outside the manifest.
 */
export function smokeTargets(): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  const push = (p: string) => {
    if (seen.has(p)) return
    seen.add(p)
    out.push(p)
  }
  push('/setup')
  push('/login')
  for (const entry of ROUTE_MANIFEST) {
    push(materializePath(entry.path))
    for (const alias of entry.aliases ?? []) push(materializePath(alias))
  }
  return out
}

/**
 * Paths a smoke run should request and the path it must land on: every legacy
 * path and every multi-tab hub root.
 */
export function smokeRedirects(): Redirect[] {
  return allRedirects()
}
