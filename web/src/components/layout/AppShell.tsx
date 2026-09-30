import { Suspense, useEffect, useRef, useState } from 'react'
import { Link, Outlet, useLocation } from 'react-router-dom'
import { CaretRight, DotsThreeOutline, House, List, Moon, SidebarSimple, Sun, X } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { useLocalStorage, useMediaQuery } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { useTheme } from '@/lib/theme'
import { HUBS, hubById, routeFor, type HubDef } from '@/lib/routes'
import { hubFor } from '@/lib/routeManifest'
import { offModuleOf, visibleHubs, withModule } from '@/lib/moduleNav'
import { saveModules, useModules } from '@/lib/useModules'
import { Button } from '@/components/ui/button'
import { Separator, Tooltip, TooltipProvider } from '@/components/ui/primitives'
import { HubTabs } from '@/components/layout/HubTabs'
import { StatusPill } from '@/components/layout/StatusPill'
import { UpdateBanner } from '@/components/layout/UpdateBanner'
import { PageChromeProvider, usePageChrome } from '@/components/layout/PageChrome'
import { ErrorBoundary } from '@/components/layout/ErrorBoundary'
import { PageSettingsSheet } from '@/components/settings/PageSettingsSheet'
import { SkeletonList, SkeletonStats } from '@/components/ui/skeleton'
import { CommandPalette, CommandPaletteTrigger } from '@/components/CommandPalette'
import { BrandMark } from '@/components/brand/BrandMark'
import { useReveal } from '@/lib/motion'

function AntaresMark({ className }: { className?: string }) {
  return <BrandMark size={26} className={className} />
}

function HubLink({
  hub,
  active,
  collapsed,
  onNavigate,
}: {
  hub: HubDef
  active: boolean
  collapsed?: boolean
  onNavigate?: () => void
}) {
  const { t } = useI18n()
  const Icon = hub.icon
  const title = t(hub.titleKey)
  const link = (
    <Link
      to={hub.tabs[0].path}
      onClick={onNavigate}
      aria-current={active ? 'page' : undefined}
      aria-label={collapsed ? title : undefined}
      className={cn(
        'flex min-h-10 items-center gap-2.5 rounded-full px-3 py-2 text-[13px] transition-[background-color,color] duration-200',
        collapsed && 'justify-center px-0',
        active ? 'bg-nav-active text-foreground' : 'text-muted-foreground hover:bg-raised hover:text-foreground',
      )}
    >
      <Icon className="size-[18px] shrink-0" />
      {collapsed ? null : <span className="truncate">{title}</span>}
    </Link>
  )
  return collapsed ? (
    <Tooltip side="right" label={title}>
      {link}
    </Tooltip>
  ) : (
    link
  )
}

/**
 * One entry per hub. A hub stays highlighted on any of its tabs (and Chat on a
 * resumed /c/:id), so activeness comes from hubFor() rather than NavLink's own
 * path match. The system tier sits apart at the bottom. Hubs of modules that
 * are off are left out (see visibleHubs for the exceptions).
 */
function NavItems({ onNavigate, collapsed }: { onNavigate?: () => void; collapsed?: boolean }) {
  const location = useLocation()
  const { active, loaded } = useModules()
  const activeHub = hubFor(location.pathname)
  const hubs = visibleHubs(HUBS, active, loaded, activeHub)
  const main = hubs.filter((h) => h.tier !== 'system')
  const system = hubs.filter((h) => h.tier === 'system')
  const { t } = useI18n()
  return (
    <nav className="flex min-h-full flex-col gap-0.5">
      {collapsed ? null : <p className="eyebrow mb-2 pl-3 text-[11px] text-dim">{t('nav.groupWorkspace')}</p>}
      {main.map((hub) => (
        <HubLink key={hub.id} hub={hub} active={hub.id === activeHub} collapsed={collapsed} onNavigate={onNavigate} />
      ))}
      {system.length ? (
        <div className="mt-auto flex flex-col gap-0.5 pt-8">
          {collapsed ? <Separator className="mb-2" /> : <p className="eyebrow mb-2 pl-3 text-[11px] text-dim">{t('nav.groupSystem')}</p>}
          {system.map((hub) => (
            <HubLink key={hub.id} hub={hub} active={hub.id === activeHub} collapsed={collapsed} onNavigate={onNavigate} />
          ))}
        </div>
      ) : null}
    </nav>
  )
}

/** Language and theme live in Settings › Appearance; the footer shows status only. */
function SidebarFooter({ collapsed }: { collapsed?: boolean }) {
  return (
    <div className={cn('space-y-1.5 px-3.5 pb-2', collapsed && 'px-2')}>
      <UpdateBanner compact={collapsed} />
      <StatusPill compact={collapsed} />
    </div>
  )
}

/** Collapses the desktop sidebar to an icon rail, or expands it back. */
function RailToggle({ collapsed, onToggle }: { collapsed: boolean; onToggle: () => void }) {
  const { t } = useI18n()
  const label = collapsed ? t('sidebar.expand') : t('sidebar.collapse')
  const button = (
    <Button
      variant="ghost"
      size="sm"
      onClick={onToggle}
      aria-label={label}
      aria-expanded={!collapsed}
      className={cn('h-9 w-full justify-start gap-2 rounded-full px-3 font-sans text-[13px] normal-case tracking-normal text-muted-foreground', collapsed && 'justify-center px-0')}
    >
      <SidebarSimple className={cn('size-4', collapsed && '-scale-x-100')} />
      {collapsed ? null : label}
    </Button>
  )
  return collapsed ? (
    <Tooltip side="right" label={label}>
      {button}
    </Tooltip>
  ) : (
    button
  )
}

/**
 * One line under the hub header when the hub's module is off: the page still
 * works, but the sidebar no longer lists it. Turning the module back on saves
 * immediately and the sidebar entry returns.
 */
function ModuleOffNotice({ hub, className }: { hub: HubDef; className?: string }) {
  const { t } = useI18n()
  const { active, loaded } = useModules()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const off = offModuleOf(hub, active, loaded)

  useEffect(() => setError(undefined), [hub.id])

  if (!off) return null

  const turnOn = async () => {
    setPending(true)
    setError(undefined)
    try {
      await saveModules(withModule(active, off, true))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setPending(false)
    }
  }

  return (
    <div
      role="status"
      className={cn(
        'mb-5 flex flex-wrap items-center gap-x-2 gap-y-1 border border-border bg-card px-3.5 py-2.5 text-xs text-muted-foreground',
        className,
      )}
    >
      <span>{t('modules.offNotice', { hub: t(hub.titleKey) })}</span>
      <span aria-hidden>·</span>
      <button
        type="button"
        onClick={turnOn}
        disabled={pending}
        aria-busy={pending || undefined}
        className="font-medium text-foreground underline decoration-line underline-offset-4 hover:decoration-foreground disabled:cursor-wait disabled:opacity-60"
      >
        {pending ? `${t('modules.turnOn')}…` : t('modules.turnOn')}
      </button>
      {error ? <span className="basis-full text-[var(--destructive)]">{error}</span> : null}
    </div>
  )
}

/** Route-level loading state; every page also has its own inner skeletons. */
function RouteFallback() {
  return (
    <div className="space-y-6">
      <SkeletonStats count={3} />
      <SkeletonList count={4} />
    </div>
  )
}

/**
 * Standard page frame: identical container width, padding, and header for every
 * route. Pages render content only.
 */
function PageFrame() {
  const location = useLocation()
  const { t } = useI18n()
  const { actions } = usePageChrome()
  const route = routeFor(location.pathname)
  const hub = route ? hubById(route.hub) : undefined
  // Hubs with several pages get the compact header: hub title, tab strip, and
  // actions on one row. The page description moves to the tab tooltip.
  const tabbed = hub && hub.tabs.length > 1 ? hub : undefined
  // Pages whose config groups moved out of Settings get a gear for them.
  const settingsRoute = route?.configGroups?.length ? route : undefined

  const labels = {
    title: t('error.pageTitle'),
    description: t('error.pageDesc'),
    retry: t('error.retry'),
  }

  if (route?.fullBleed) {
    return (
      <ErrorBoundary resetKey={location.pathname} labels={labels}>
        <Suspense
          fallback={
            <div className="p-6">
              <RouteFallback />
            </div>
          }
        >
          <Outlet />
        </Suspense>
      </ErrorBoundary>
    )
  }

  // Content pages own their scrolling on desktop: the frame stops growing and
  // the page scrolls inside it (via PageLayout), so the header and any footer
  // stay put. This is the default now; a route sets staticHeight to opt out
  // (a genuinely short page that should just size to its content).
  const fill = !route?.staticHeight
  const actionsNode =
    actions || settingsRoute ? (
      <div className="flex shrink-0 flex-wrap items-center gap-2.5">
        {actions}
        {settingsRoute ? <PageSettingsSheet route={settingsRoute} /> : null}
      </div>
    ) : null

  // The header plays its entrance when the hub changes (its tabs stay mounted
  // between the hub's pages, so the nav mark can slide); the body plays it on
  // every page.
  return (
    <div
      className={cn(
        'mx-auto w-full max-w-[1400px] px-4 py-6 sm:px-6 sm:py-8 lg:px-10',
        fill && 'lg:flex lg:h-[calc(100dvh-4rem)] lg:flex-col lg:overflow-hidden',
      )}
    >
      {route ? (
        <header
          key={hub?.id ?? route.path}
          className={cn(
            'm-page-head m-rule mb-7 flex flex-col gap-4 pb-6 lg:flex-row lg:items-end lg:justify-between lg:gap-6',
            fill && 'lg:shrink-0',
          )}
        >
          <div className="min-w-0">
            <p className="eyebrow mb-2">{hub ? t(hub.titleKey) : 'Antares'}</p>
            <h1 className="text-[clamp(22px,2.2vw,28px)] font-medium leading-tight tracking-[-0.6px] text-balance">
              {tabbed ? t(tabbed.titleKey) : t(route.titleKey)}
            </h1>
            {!tabbed && route.descKey ? (
              <p className="mt-2 max-w-2xl text-sm leading-relaxed text-muted-foreground">{t(route.descKey)}</p>
            ) : null}
          </div>
          {tabbed ? (
            <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center lg:justify-end">
              <HubTabs hub={tabbed} className="min-w-0" />
              {actionsNode}
            </div>
          ) : (
            actionsNode
          )}
        </header>
      ) : null}

      {hub ? <ModuleOffNotice hub={hub} className={cn(fill && 'lg:shrink-0')} /> : null}

      <ErrorBoundary resetKey={location.pathname} labels={labels}>
        <Suspense fallback={<RouteFallback />}>
          <div key={location.pathname} className={cn('m-page-body', fill && 'lg:flex lg:min-h-0 lg:flex-1 lg:flex-col')}>
            <Outlet />
          </div>
        </Suspense>
      </ErrorBoundary>
    </div>
  )
}

/**
 * The strip over every page: where you are (home › hub › page) on the left,
 * search and the theme on the right. The site's dashboard topbar.
 */
function Topbar() {
  const location = useLocation()
  const { t } = useI18n()
  const { theme, toggleTheme } = useTheme()
  const route = routeFor(location.pathname)
  const hub = route ? hubById(route.hub) : undefined
  const page = route && hub && hub.tabs.length > 1 ? t(route.tabKey ?? route.titleKey) : undefined
  return (
    <header className="sticky top-0 z-30 hidden h-16 shrink-0 items-center justify-between gap-5 border-b border-line bg-background/85 px-6 backdrop-blur-md lg:flex xl:px-9">
      <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-3 text-xs text-muted-foreground">
        <Link to="/" className="inline-flex items-center gap-1.5 transition-colors hover:text-foreground">
          <House className="size-4" />
          <span>Antares</span>
        </Link>
        {hub ? (
          <>
            <CaretRight className="size-3 shrink-0" />
            {page ? (
              <Link to={hub.tabs[0].path} className="truncate transition-colors hover:text-foreground">
                {t(hub.titleKey)}
              </Link>
            ) : (
              <span className="truncate text-foreground">{t(hub.titleKey)}</span>
            )}
          </>
        ) : null}
        {page ? (
          <>
            <CaretRight className="size-3 shrink-0" />
            <span className="truncate text-foreground">{page}</span>
          </>
        ) : null}
      </nav>
      <div className="flex shrink-0 items-center gap-2">
        <CommandPaletteTrigger variant="topbar" />
        <Button variant="ghost" size="icon" className="rounded-full" aria-label={t('theme.toggle')} onClick={toggleTheme}>
          {theme === 'dark' ? <Sun /> : <Moon />}
        </Button>
      </div>
    </header>
  )
}

/** Hubs pinned to the mobile bottom bar: those owning a `primary` route. */
const BOTTOM_BAR_HUBS = HUBS.filter((h) => h.tabs.some((r) => r.primary))

export function AppShell() {
  const { theme, toggleTheme } = useTheme()
  const { t } = useI18n()
  const isDesktop = useMediaQuery('(min-width: 1024px)')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [railCollapsed, setRailCollapsed] = useLocalStorage('antares.sidebarCollapsed', false)
  const location = useLocation()
  const motionRoot = useRef<HTMLDivElement>(null)
  useReveal(motionRoot)

  useEffect(() => setDrawerOpen(false), [location.pathname])

  const activeHub = hubFor(location.pathname)
  // More reads as active on any hub the bar does not show directly.
  const moreActive = activeHub !== undefined && !BOTTOM_BAR_HUBS.some((h) => h.id === activeHub)

  return (
    <TooltipProvider delayDuration={300}>
      <PageChromeProvider>
        <div ref={motionRoot} className="flex min-h-dvh bg-background">
          {/* Desktop sidebar */}
          <aside
            className={cn(
              'sticky top-0 hidden h-dvh shrink-0 flex-col border-r border-line bg-sidebar lg:flex',
              railCollapsed ? 'w-16' : 'w-64',
            )}
          >
            <Link
              to="/"
              aria-label="Antares"
              className={cn('flex items-center gap-2.5 px-5 pb-5 pt-5', railCollapsed && 'justify-center px-0')}
            >
              <AntaresMark />
              {railCollapsed ? null : (
                <>
                  <span className="text-[22px] font-[350] leading-none tracking-[-0.8px]">antares</span>
                  <span className="ml-auto rounded-full bg-raised px-2 py-0.5 text-[10.5px] font-medium leading-4 text-muted-foreground">
                    {t('nav.subtitle')}
                  </span>
                </>
              )}
            </Link>
            <SidebarFooter collapsed={railCollapsed} />
            <div className={cn('mt-4 flex-1 overflow-y-auto px-3.5 pb-2', railCollapsed && 'px-2')}>
              <NavItems collapsed={railCollapsed} />
            </div>
            <div className={cn('border-t border-line px-3.5 py-3', railCollapsed && 'px-2')}>
              <RailToggle collapsed={railCollapsed} onToggle={() => setRailCollapsed(!railCollapsed)} />
            </div>
          </aside>

          {/* Mobile drawer */}
          {!isDesktop && drawerOpen ? (
            <div className="fixed inset-0 z-50 lg:hidden">
              <button
                aria-label={t('nav.closeMenu')}
                className="m-fade absolute inset-0 bg-[#04050699] backdrop-blur-[4px]"
                onClick={() => setDrawerOpen(false)}
              />
              <div className="m-slide absolute inset-y-0 left-0 flex w-[17rem] max-w-[85vw] flex-col border-r border-line bg-sidebar shadow-2xl">
                <div className="flex items-center justify-between px-4 pb-4 pt-[calc(env(safe-area-inset-top)+1.5rem)]">
                  <div className="flex items-center gap-2.5">
                    <AntaresMark />
                    <span className="text-[22px] font-[350] leading-none tracking-[-0.8px]">antares</span>
                  </div>
                  <Button variant="ghost" size="icon-sm" className="rounded-full" onClick={() => setDrawerOpen(false)}>
                    <X />
                  </Button>
                </div>
                <SidebarFooter />
                <div className="mt-4 flex-1 overflow-y-auto px-3.5 pb-4">
                  <NavItems onNavigate={() => setDrawerOpen(false)} />
                </div>
                <div className="safe-bottom h-2" />
              </div>
            </div>
          ) : null}

          {/* Main column */}
          <div className="flex min-w-0 flex-1 flex-col">
            <header className="safe-top sticky top-0 z-30 flex h-14 items-center gap-2 border-b border-line bg-background/85 px-3 backdrop-blur-md lg:hidden">
              <Button
                variant="ghost"
                size="icon"
                className="rounded-full"
                onClick={() => setDrawerOpen(true)}
                aria-label={t('nav.openMenu')}
              >
                <List />
              </Button>
              <Link to="/" className="flex min-w-0 items-center gap-2">
                <AntaresMark className="size-6" />
                <span className="truncate text-lg font-[350] tracking-[-0.6px]">antares</span>
              </Link>
              <div className="ml-auto">
                <CommandPaletteTrigger variant="icon" />
              </div>
              <Button
                variant="ghost"
                size="icon"
                className="rounded-full"
                aria-label={t('theme.toggle')}
                onClick={toggleTheme}
              >
                {theme === 'dark' ? <Sun /> : <Moon />}
              </Button>
            </header>
            <Topbar />

            <main className="min-w-0 flex-1 pb-[4.5rem] lg:pb-0">
              <PageFrame />
            </main>

            {/* Mobile bottom navigation */}
            <nav className="safe-bottom fixed inset-x-0 bottom-0 z-30 flex border-t border-line bg-background/95 backdrop-blur-md lg:hidden">
              {BOTTOM_BAR_HUBS.map((hub) => {
                const active = hub.id === activeHub
                const Icon = hub.icon
                return (
                  <Link
                    key={hub.id}
                    to={hub.tabs[0].path}
                    aria-current={active ? 'page' : undefined}
                    className={cn(
                      'flex flex-1 flex-col items-center gap-1 px-1 py-2.5 text-[10px] leading-none transition-colors',
                      active ? 'text-foreground' : 'text-muted-foreground',
                    )}
                  >
                    <Icon className="size-5" weight={active ? 'fill' : 'regular'} />
                    <span className="max-w-full truncate">{t(hub.titleKey)}</span>
                  </Link>
                )
              })}
              {/* Everything else lives in the drawer, which lists every hub. */}
              <button
                type="button"
                onClick={() => setDrawerOpen(true)}
                aria-expanded={drawerOpen}
                className={cn(
                  'flex flex-1 flex-col items-center gap-1 px-1 py-2.5 text-[10px] leading-none transition-colors',
                  moreActive ? 'text-foreground' : 'text-muted-foreground',
                )}
              >
                <DotsThreeOutline className="size-5" weight={moreActive ? 'fill' : 'regular'} />
                <span className="max-w-full truncate">{t('nav.more')}</span>
              </button>
            </nav>
          </div>
        </div>
        <CommandPalette theme={theme} onToggleTheme={toggleTheme} />
      </PageChromeProvider>
    </TooltipProvider>
  )
}

/**
 * Vertical rhythm for page content. Every page wraps its sections in this so
 * the gap between blocks is identical everywhere.
 */
export function PageBody({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn('space-y-6', className)}>{children}</div>
}
