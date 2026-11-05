import { lazy, type ComponentType, type LazyExoticComponent } from 'react'
import {
  ChartLineUp,
  ChatCircleDots,
  ClockCounterClockwise,
  Database,
  FileText,
  Fingerprint,
  FilmStrip,
  Gear,
  GlobeHemisphereWest,
  HardDrives,
  Plugs,
  PlugsConnected,
  PuzzlePiece,
  ShareNetwork,
  UsersThree,
  Broadcast,
  Kanban,
  Robot,
  ShieldCheck,
  Sparkle,
  Terminal,
  Toolbox,
} from '@phosphor-icons/react'
import {
  HUB_MANIFEST,
  ROUTE_MANIFEST,
  entryFor,
  type HubId,
  type HubManifestEntry,
  type RouteManifestEntry,
} from './routeManifest'

export type IconComponent = ComponentType<{
  className?: string
  weight?: 'regular' | 'fill' | 'bold' | 'duotone'
}>

export interface RouteDef extends RouteManifestEntry {
  icon: IconComponent
  component: LazyExoticComponent<ComponentType>
}

/**
 * React bits keyed by manifest id. Kept alongside the manifest so a new page
 * is two edits in this repo (manifest entry + this table); the shell reads
 * everything through the composed ROUTES list below.
 */
interface RouteRuntime {
  icon: IconComponent
  component: LazyExoticComponent<ComponentType>
}

const RUNTIME_BY_ID: Record<string, RouteRuntime> = {
  chat: {
    icon: ChatCircleDots,
    component: lazy(() => import('@/pages/ChatPage')),
  },
  sessions: {
    icon: ClockCounterClockwise,
    component: lazy(() => import('@/pages/SessionsPage')),
  },
  providers: {
    icon: Plugs,
    component: lazy(() => import('@/pages/ProvidersPage')),
  },
  tools: {
    icon: Toolbox,
    component: lazy(() => import('@/pages/ToolsPage')),
  },
  memory: {
    icon: Database,
    component: lazy(() => import('@/pages/MemoryPage')),
  },
  roles: {
    icon: UsersThree,
    component: lazy(() => import('@/pages/RolesPage')),
  },
  soul: {
    icon: Fingerprint,
    component: lazy(() => import('@/pages/SoulPage')),
  },
  skills: {
    icon: Sparkle,
    component: lazy(() => import('@/pages/SkillsPage')),
  },
  cron: {
    icon: ClockCounterClockwise,
    component: lazy(() => import('@/pages/CronPage')),
  },
  channels: {
    icon: Plugs,
    component: lazy(() => import('@/pages/ChannelsPage')),
  },
  autopilot: {
    icon: Robot,
    component: lazy(() => import('@/pages/AutopilotPage')),
  },
  engagement: {
    icon: ShieldCheck,
    component: lazy(() => import('@/pages/EngagementPage')),
  },
  board: {
    icon: Kanban,
    component: lazy(() => import('@/pages/BoardPage')),
  },
  intercept: {
    icon: Broadcast,
    component: lazy(() => import('@/pages/InterceptPage')),
  },
  mcp: {
    icon: PlugsConnected,
    component: lazy(() => import('@/pages/McpPage')),
  },
  plugins: {
    icon: PuzzlePiece,
    component: lazy(() => import('@/pages/PluginsPage')),
  },
  proxies: {
    icon: GlobeHemisphereWest,
    component: lazy(() => import('@/pages/ProxiesPage')),
  },
  vps: {
    icon: HardDrives,
    component: lazy(() => import('@/pages/VPSPage')),
  },
  analytics: {
    icon: ChartLineUp,
    component: lazy(() => import('@/pages/AnalyticsPage')),
  },
  files: {
    icon: FileText,
    component: lazy(() => import('@/pages/FilesPage')),
  },
  logs: {
    icon: Terminal,
    component: lazy(() => import('@/pages/LogsPage')),
  },
  config: {
    icon: Gear,
    component: lazy(() => import('@/pages/ConfigPage')),
  },
  system: {
    icon: Robot,
    component: lazy(() => import('@/pages/SystemPage')),
  },
  'content-creator': {
    icon: FilmStrip,
    component: lazy(() => import('@/pages/ContentCreatorPage')),
  },
  'social-media': {
    icon: ShareNetwork,
    component: lazy(() => import('@/pages/SocialMediaPage')),
  },
}

/**
 * The single source of truth for navigation, routing, and page chrome.
 * Pages render content only — the shell owns the container and header so every
 * screen shares identical spacing. Composed from {@link ROUTE_MANIFEST} so
 * build-time consumers (smoke, prerender) read the same list as the runtime.
 */
export const ROUTES: RouteDef[] = ROUTE_MANIFEST.map((entry) => {
  const runtime = RUNTIME_BY_ID[entry.id]
  if (!runtime) {
    throw new Error(`routes.ts: no runtime registered for manifest id "${entry.id}"`)
  }
  return { ...entry, icon: runtime.icon, component: runtime.component }
})

export interface HubDef extends HubManifestEntry {
  icon: IconComponent
  /** Pages in tab order; the first is where the sidebar entry links. */
  tabs: RouteDef[]
}

/** Single-page hubs reuse their page's icon; multi-tab hubs get their own. */
const HUB_ICONS: Partial<Record<HubId, IconComponent>> = {
  agent: Robot,
  capabilities: Toolbox,
  automation: Kanban,
  security: ShieldCheck,
  studio: FilmStrip,
  system: Gear,
}

/**
 * Sidebar entries, composed from {@link HUB_MANIFEST} like ROUTES is from the
 * route manifest. A hub with no pages or no icon throws at startup.
 */
export const HUBS: HubDef[] = HUB_MANIFEST.map((hub) => {
  const tabs = ROUTES.filter((r) => r.hub === hub.id)
  if (tabs.length === 0) {
    throw new Error(`routes.ts: hub "${hub.id}" has no routes`)
  }
  const icon = HUB_ICONS[hub.id] ?? (tabs.length === 1 ? tabs[0].icon : undefined)
  if (!icon) {
    throw new Error(`routes.ts: no icon registered for hub "${hub.id}"`)
  }
  return { ...hub, icon, tabs }
})

const HUB_BY_ID = new Map(HUBS.map((h) => [h.id, h]))

export function hubById(id: HubId): HubDef | undefined {
  return HUB_BY_ID.get(id)
}

/** Resolve the route definition for a pathname. */
export function routeFor(pathname: string): RouteDef | undefined {
  const entry = entryFor(pathname)
  return entry ? ROUTES.find((r) => r.id === entry.id) : undefined
}
