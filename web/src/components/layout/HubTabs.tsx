import { useEffect, useRef } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { useI18n } from '@/lib/i18n'
import type { HubDef } from '@/lib/routes'
import { Tooltip } from '@/components/ui/primitives'

/**
 * Tab strip for a multi-page hub. Each tab is a route, so tabs are links
 * (bookmarkable, lazy per page) rather than in-page Radix tabs. The page
 * description lives in the tab tooltip now that the hub header no longer
 * renders it.
 *
 * Plain Link with a computed active state, not NavLink: the tooltip trigger
 * is a Radix Slot, which joins className values as strings and would turn
 * NavLink's className callback into garbage.
 */
export function HubTabs({ hub, className }: { hub: HubDef; className?: string }) {
  const { t } = useI18n()
  const { pathname } = useLocation()
  const listRef = useRef<HTMLDivElement>(null)

  // On a narrow screen the active tab can start off-screen (e.g. landing on
  // /system/settings). Scroll the strip itself, not scrollIntoView, so the
  // page never moves vertically.
  useEffect(() => {
    const list = listRef.current
    const active = list?.querySelector<HTMLElement>('[aria-current="page"]')
    if (!list || !active) return
    const start = active.offsetLeft
    const end = start + active.offsetWidth
    if (start < list.scrollLeft || end > list.scrollLeft + list.clientWidth) {
      list.scrollLeft = start
    }
  }, [pathname])

  return (
    <nav aria-label={t(hub.titleKey)} className={cn('min-w-0', className)}>
      <div
        ref={listRef}
        className="relative flex items-center gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        {hub.tabs.map((tab) => {
          const active = tab.path === pathname
          const link = (
            <Link
              key={tab.path}
              to={tab.path}
              aria-current={active ? 'page' : undefined}
              className={cn(
                'shrink-0 whitespace-nowrap rounded-[var(--radius-sm)] px-3 py-1.5 text-sm transition-colors',
                active
                  ? 'bg-primary/12 font-medium text-primary'
                  : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
              )}
            >
              {t(tab.tabKey ?? tab.titleKey)}
            </Link>
          )
          return tab.descKey ? (
            <Tooltip
              key={tab.path}
              side="bottom"
              label={<span className="block max-w-72 leading-relaxed">{t(tab.descKey)}</span>}
            >
              {link}
            </Tooltip>
          ) : (
            link
          )
        })}
      </div>
    </nav>
  )
}
