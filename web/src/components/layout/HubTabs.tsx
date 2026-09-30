import { useEffect, useLayoutEffect, useRef, useState } from 'react'
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
  const previous = useRef<string | null>(null)
  const [mark, setMark] = useState({ x: 0, width: 0, on: false, instant: true })

  // The mark sits under the active tab, measured from the tab itself. It
  // fades in where it first appears and slides between tabs after that.
  useLayoutEffect(() => {
    const list = listRef.current
    const appearing = previous.current === null
    previous.current = pathname
    const follow = (instant?: boolean) => {
      const tab = list?.querySelector<HTMLElement>('[aria-current="page"]')
      setMark((cur) => {
        const next = tab
          ? { x: tab.offsetLeft, width: tab.offsetWidth, on: true, instant: instant ?? cur.instant }
          : { ...cur, on: false }
        return next.x === cur.x && next.width === cur.width && next.on === cur.on && next.instant === cur.instant
          ? cur
          : next
      })
    }
    follow(appearing)
    // The tabs move when the web font arrives or the page is zoomed.
    const resize = new ResizeObserver(() => follow())
    if (list) resize.observe(list)
    return () => resize.disconnect()
  }, [pathname])

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
        className="relative isolate inline-flex max-w-full items-center gap-0.5 overflow-x-auto rounded-full border border-border bg-card px-1.5 py-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        <span
          aria-hidden
          className="nav-mark -z-10"
          data-on={mark.on ? '' : undefined}
          data-instant={mark.instant ? '' : undefined}
          style={{ width: mark.width, transform: `translateX(${mark.x}px)` }}
        />
        {hub.tabs.map((tab) => {
          const active = tab.path === pathname
          const link = (
            <Link
              key={tab.path}
              to={tab.path}
              aria-current={active ? 'page' : undefined}
              className={cn(
                'shrink-0 whitespace-nowrap rounded-full px-3.5 py-1.5 text-xs transition-[color,background-color] duration-200',
                active ? 'text-foreground' : 'text-muted-foreground hover:bg-raised/60 hover:text-foreground',
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
