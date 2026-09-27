import { Suspense, useEffect, useState } from 'react'
import { Link, Outlet, useLocation } from 'react-router-dom'
import { DotsThreeOutline, List, Moon, Sun, Translate, X } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { useLocalStorage, useMediaQuery } from '@/lib/hooks'
import { LANGUAGES, useI18n } from '@/lib/i18n'
import { HUBS, hubById, routeFor, type HubDef } from '@/lib/routes'
import { hubFor } from '@/lib/routeManifest'
import { Button } from '@/components/ui/button'
import { Separator, TooltipProvider } from '@/components/ui/primitives'
import { HubTabs } from '@/components/layout/HubTabs'
import { StatusPill } from '@/components/layout/StatusPill'
import { UpdateBanner } from '@/components/layout/UpdateBanner'
import { PageChromeProvider, usePageChrome } from '@/components/layout/PageChrome'
import { ErrorBoundary } from '@/components/layout/ErrorBoundary'
import { SkeletonList, SkeletonStats } from '@/components/ui/skeleton'

/** Applies the persisted theme to <html>. */
function useTheme() {
  const [theme, setTheme] = useLocalStorage<'dark' | 'light'>('antares.theme', 'dark')
  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
    document.documentElement.style.colorScheme = theme
  }, [theme])
  return { theme, setTheme }
}

function AntaresMark({ className }: { className?: string }) {
  return (
    <img
      src="/antares-192.png"
      alt=""
      aria-hidden
      width={32}
      height={32}
      className={cn('size-8 shrink-0 select-none object-contain', className)}
      draggable={false}
    />
  )
}

function HubLink({ hub, active, onNavigate }: { hub: HubDef; active: boolean; onNavigate?: () => void }) {
  const { t } = useI18n()
  const Icon = hub.icon
  return (
    <Link
      to={hub.tabs[0].path}
      onClick={onNavigate}
      aria-current={active ? 'page' : undefined}
      className={cn(
        'flex items-center gap-2.5 rounded-[var(--radius-sm)] px-3 py-2 text-sm transition-colors',
        active
          ? 'bg-primary/12 font-medium text-primary'
          : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
      )}
    >
      <Icon className="size-4.5 shrink-0" weight={active ? 'fill' : 'regular'} />
      <span className="truncate">{t(hub.titleKey)}</span>
    </Link>
  )
}

/**
 * One entry per hub. A hub stays highlighted on any of its tabs (and Chat on a
 * resumed /c/:id), so activeness comes from hubFor() rather than NavLink's own
 * path match. The system tier sits apart at the bottom.
 */
function NavItems({ onNavigate }: { onNavigate?: () => void }) {
  const location = useLocation()
  const activeHub = hubFor(location.pathname)
  const main = HUBS.filter((h) => h.tier !== 'system')
  const system = HUBS.filter((h) => h.tier === 'system')
  return (
    <nav className="flex min-h-full flex-col gap-0.5">
      {main.map((hub) => (
        <HubLink key={hub.id} hub={hub} active={hub.id === activeHub} onNavigate={onNavigate} />
      ))}
      {system.length ? (
        <div className="mt-auto flex flex-col gap-0.5 pt-4">
          <Separator className="mb-2" />
          {system.map((hub) => (
            <HubLink key={hub.id} hub={hub} active={hub.id === activeHub} onNavigate={onNavigate} />
          ))}
        </div>
      ) : null}
    </nav>
  )
}

function LanguagePicker() {
  const { lang, setLang, t } = useI18n()
  return (
    <label className="flex h-9 items-center gap-2 rounded-[var(--radius-sm)] px-3 text-xs text-muted-foreground">
      <Translate className="size-4 shrink-0" />
      <span className="sr-only">{t('nav.language')}</span>
      <select
        value={lang}
        onChange={(e) => setLang(e.target.value as typeof lang)}
        className="w-full cursor-pointer bg-transparent text-xs text-foreground outline-none"
        aria-label={t('nav.language')}
      >
        {LANGUAGES.map((l) => (
          <option key={l.code} value={l.code} className="bg-popover text-popover-foreground">
            {l.label}
          </option>
        ))}
      </select>
    </label>
  )
}

function SidebarFooter() {
  const { theme, setTheme } = useTheme()
  const { t } = useI18n()
  return (
    <div className="space-y-1 border-t border-border p-3">
      <UpdateBanner />
      <StatusPill />
      <LanguagePicker />
      <Button
        variant="ghost"
        size="sm"
        className="h-9 w-full justify-start gap-2 px-3"
        onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
      >
        {theme === 'dark' ? <Sun className="size-4" /> : <Moon className="size-4" />}
        {theme === 'dark' ? t('theme.light') : t('theme.dark')}
      </Button>
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

  return (
    <div
      className={cn(
        'mx-auto w-full max-w-6xl px-4 py-5 sm:px-6 sm:py-7 lg:px-8',
        fill && 'lg:flex lg:h-dvh lg:flex-col lg:overflow-hidden',
      )}
    >
      {tabbed ? (
        <header
          className={cn(
            'mb-6 flex flex-col gap-3 lg:flex-row lg:items-center lg:gap-4',
            fill && 'lg:shrink-0',
          )}
        >
          <div className="flex min-w-0 flex-1 flex-col gap-2 sm:flex-row sm:items-center sm:gap-4">
            <h1 className="shrink-0 text-lg font-semibold leading-tight tracking-tight sm:text-xl">
              {t(tabbed.titleKey)}
            </h1>
            <HubTabs hub={tabbed} className="flex-1" />
          </div>
          {actions ? (
            <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>
          ) : null}
        </header>
      ) : route ? (
        <header
          className={cn(
            'mb-6 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4',
            fill && 'lg:shrink-0',
          )}
        >
          <div className="min-w-0 space-y-1.5">
            <h1 className="text-lg font-semibold leading-tight tracking-tight text-balance sm:text-xl">
              {t(route.titleKey)}
            </h1>
            {route.descKey ? (
              <p className="max-w-2xl text-xs leading-relaxed text-muted-foreground sm:text-sm">
                {t(route.descKey)}
              </p>
            ) : null}
          </div>
          {actions ? (
            <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>
          ) : null}
        </header>
      ) : null}

      <ErrorBoundary resetKey={location.pathname} labels={labels}>
        <Suspense fallback={<RouteFallback />}>
          <div className={cn(fill && 'lg:flex lg:min-h-0 lg:flex-1 lg:flex-col')}>
            <Outlet />
          </div>
        </Suspense>
      </ErrorBoundary>
    </div>
  )
}

/** Hubs pinned to the mobile bottom bar: those owning a `primary` route. */
const BOTTOM_BAR_HUBS = HUBS.filter((h) => h.tabs.some((r) => r.primary))

export function AppShell() {
  const { theme, setTheme } = useTheme()
  const { t } = useI18n()
  const isDesktop = useMediaQuery('(min-width: 1024px)')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const location = useLocation()

  useEffect(() => setDrawerOpen(false), [location.pathname])

  const activeHub = hubFor(location.pathname)
  // More reads as active on any hub the bar does not show directly.
  const moreActive = activeHub !== undefined && !BOTTOM_BAR_HUBS.some((h) => h.id === activeHub)

  return (
    <TooltipProvider delayDuration={300}>
      <PageChromeProvider>
        <div className="flex min-h-dvh bg-background">
          {/* Desktop sidebar */}
          <aside className="sticky top-0 hidden h-dvh w-60 shrink-0 flex-col border-r border-border bg-sidebar lg:flex">
            <div className="flex items-center gap-2.5 px-4 py-4">
              <AntaresMark />
              <div className="min-w-0">
                <p className="truncate text-sm font-semibold tracking-tight">Antares</p>
                <p className="truncate text-[11px] text-muted-foreground">{t('nav.subtitle')}</p>
              </div>
            </div>
            <div className="flex-1 overflow-y-auto px-2 pb-2">
              <NavItems />
            </div>
            <SidebarFooter />
          </aside>

          {/* Mobile drawer */}
          {!isDesktop && drawerOpen ? (
            <div className="fixed inset-0 z-50 lg:hidden">
              <button
                aria-label={t('nav.closeMenu')}
                className="absolute inset-0 bg-black/60 backdrop-blur-[2px]"
                onClick={() => setDrawerOpen(false)}
              />
              <div className="absolute inset-y-0 left-0 flex w-[17rem] max-w-[85vw] flex-col bg-sidebar shadow-2xl fade-up">
                <div className="flex items-center justify-between px-4 pb-4 pt-[calc(env(safe-area-inset-top)+1.5rem)]">
                  <div className="flex items-center gap-2.5">
                    <AntaresMark />
                    <p className="text-sm font-semibold tracking-tight">Antares</p>
                  </div>
                  <Button variant="ghost" size="icon-sm" onClick={() => setDrawerOpen(false)}>
                    <X />
                  </Button>
                </div>
                <div className="flex-1 overflow-y-auto px-2 pb-4">
                  <NavItems onNavigate={() => setDrawerOpen(false)} />
                </div>
                <div className="safe-bottom">
                  <SidebarFooter />
                </div>
              </div>
            </div>
          ) : null}

          {/* Main column */}
          <div className="flex min-w-0 flex-1 flex-col">
            <header className="safe-top sticky top-0 z-30 flex h-14 items-center gap-2 border-b border-border bg-background/85 px-3 backdrop-blur-md lg:hidden">
              <Button
                variant="ghost"
                size="icon"
                onClick={() => setDrawerOpen(true)}
                aria-label={t('nav.openMenu')}
              >
                <List />
              </Button>
              <div className="flex min-w-0 items-center gap-2">
                <AntaresMark className="size-7" />
                <span className="truncate text-sm font-semibold">Antares</span>
              </div>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t('theme.toggle')}
                onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
                className="ml-auto"
              >
                {theme === 'dark' ? <Sun /> : <Moon />}
              </Button>
            </header>

            <main className="min-w-0 flex-1 pb-[4.5rem] lg:pb-0">
              <PageFrame />
            </main>

            {/* Mobile bottom navigation */}
            <nav className="safe-bottom fixed inset-x-0 bottom-0 z-30 flex border-t border-border bg-background/95 backdrop-blur-md lg:hidden">
              {BOTTOM_BAR_HUBS.map((hub) => {
                const active = hub.id === activeHub
                const Icon = hub.icon
                return (
                  <Link
                    key={hub.id}
                    to={hub.tabs[0].path}
                    aria-current={active ? 'page' : undefined}
                    className={cn(
                      'flex flex-1 flex-col items-center gap-1 px-1 py-2.5 text-[10px] font-medium leading-none transition-colors',
                      active ? 'text-primary' : 'text-muted-foreground',
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
                  'flex flex-1 flex-col items-center gap-1 px-1 py-2.5 text-[10px] font-medium leading-none transition-colors',
                  moreActive ? 'text-primary' : 'text-muted-foreground',
                )}
              >
                <DotsThreeOutline className="size-5" weight={moreActive ? 'fill' : 'regular'} />
                <span className="max-w-full truncate">{t('nav.more')}</span>
              </button>
            </nav>
          </div>
        </div>
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
