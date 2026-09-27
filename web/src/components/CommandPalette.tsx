import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  ChatCircleDots,
  MagnifyingGlass,
  Moon,
  NotePencil,
  Plugs,
  Sun,
  type Icon as PhosphorIcon,
} from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { cn } from '@/lib/utils'
import { englishText, useI18n, useTimeAgo } from '@/lib/i18n'
import { useModules } from '@/lib/useModules'
import { HUBS, ROUTES, hubById, type IconComponent } from '@/lib/routes'
import type { HubId } from '@/lib/routeManifest'
import {
  buildActionItems,
  buildPageItems,
  buildSessionItems,
  flattenGroups,
  rankItems,
  type PaletteItem,
  type PaletteKind,
  type PaletteSessionInput,
} from '@/lib/palette'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'

const OPEN_EVENT = 'antares:palette-open'

const IS_MAC =
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent)

/** Label for the shortcut, e.g. in the sidebar trigger. */
const SHORTCUT_LABEL = IS_MAC ? '⌘K' : 'Ctrl K'
const SHORTCUT_ARIA = IS_MAC ? 'Meta+K' : 'Control+K'

/** Open the palette from anywhere (the triggers use this). */
export function openCommandPalette() {
  window.dispatchEvent(new Event(OPEN_EVENT))
}

const GROUP_LABEL = {
  action: 'palette.groupActions',
  page: 'palette.groupPages',
  session: 'palette.groupSessions',
} as const satisfies Record<PaletteKind, string>

const ROUTE_ICON = new Map(ROUTES.map((r) => [r.id, r.icon]))

function iconFor(item: PaletteItem, theme: 'dark' | 'light'): IconComponent | PhosphorIcon {
  if (item.action === 'new-chat') return NotePencil
  if (item.action === 'toggle-theme') return theme === 'dark' ? Sun : Moon
  if (item.action === 'models') return Plugs
  if (item.kind === 'session') return ChatCircleDots
  const byRoute = item.routeId ? ROUTE_ICON.get(item.routeId) : undefined
  if (byRoute) return byRoute
  const hub = item.hubId ? hubById(item.hubId as HubId) : undefined
  return hub?.icon ?? MagnifyingGlass
}

interface SessionList {
  sessions: PaletteSessionInput[]
}

/**
 * Cmd+K / Ctrl+K palette: jump to any hub or tab (including modules that are
 * off), reopen a recent session, or run a small set of actions. Mounted once
 * in AppShell. Ranking lives in lib/palette.ts; this file renders and handles
 * the keyboard.
 */
export function CommandPalette({ theme, onToggleTheme }: { theme: 'dark' | 'light'; onToggleTheme: () => void }) {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const navigate = useNavigate()
  const { active: activeModules } = useModules()

  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const [sessions, setSessions] = useState<PaletteSessionInput[] | undefined>()
  const listRef = useRef<HTMLDivElement>(null)
  const baseId = useId()
  const listId = `${baseId}-list`
  const optionId = (i: number) => `${baseId}-opt-${i}`

  // Global shortcut toggles; triggers dispatch OPEN_EVENT.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = IS_MAC ? e.metaKey : e.ctrlKey
      if (!mod || e.altKey || e.shiftKey || e.isComposing || e.key.toLowerCase() !== 'k') return
      e.preventDefault()
      setOpen((o) => !o)
    }
    const onOpen = () => setOpen(true)
    window.addEventListener('keydown', onKey)
    window.addEventListener(OPEN_EVENT, onOpen)
    return () => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener(OPEN_EVENT, onOpen)
    }
  }, [])

  // Fresh state on every open. Sessions load only once the palette is opened,
  // then refresh on each later open while the previous list stays visible.
  useEffect(() => {
    if (!open) return
    setQuery('')
    setActiveIndex(0)
    let cancelled = false
    get<SessionList>('/sessions?limit=8')
      .then((res) => {
        if (!cancelled) setSessions(res.sessions ?? [])
      })
      .catch(() => {
        // Sessions are a convenience here; pages and actions still work.
        if (!cancelled) setSessions((prev) => prev ?? [])
      })
    return () => {
      cancelled = true
    }
  }, [open])

  const items = useMemo(() => {
    const en = englishText
    return [
      ...buildActionItems({ t, en, theme }),
      ...buildPageItems(HUBS, { t, en, activeModules }),
      ...buildSessionItems(sessions ?? [], { untitled: t('sessions.untitled'), timeAgo }),
    ]
  }, [t, theme, activeModules, sessions, timeAgo])

  const groups = useMemo(() => rankItems(items, query), [items, query])
  const flat = useMemo(() => flattenGroups(groups), [groups])
  const indexOf = useMemo(() => new Map(flat.map((item, i) => [item.id, i])), [flat])
  const current = Math.min(activeIndex, Math.max(flat.length - 1, 0))

  useEffect(() => {
    listRef.current?.querySelector('[data-active="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [current, open])

  const run = useCallback(
    (item: PaletteItem) => {
      setOpen(false)
      if (item.action === 'new-chat') navigate('/', { state: { fresh: true } })
      else if (item.action === 'toggle-theme') onToggleTheme()
      else if (item.to) navigate(item.to)
    },
    [navigate, onToggleTheme],
  )

  const onInputKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return
    const n = flat.length
    if (e.key === 'ArrowDown' && n) {
      e.preventDefault()
      setActiveIndex((current + 1) % n)
    } else if (e.key === 'ArrowUp' && n) {
      e.preventDefault()
      setActiveIndex((current - 1 + n) % n)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      const item = flat[current]
      if (item) run(item)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent
        aria-describedby={undefined}
        className={cn(
          // Phone: a panel under the top edge so the on-screen keyboard never
          // covers the input or the results.
          'inset-x-3 bottom-auto top-[calc(env(safe-area-inset-top)+0.75rem)] max-h-[70dvh] rounded-[var(--radius-xl)] border',
          // Desktop: anchored high rather than centred, so the input stays put
          // while the result list grows and shrinks.
          'sm:top-[12vh] sm:max-h-[76dvh] sm:max-w-xl sm:translate-y-0',
        )}
      >
        <DialogTitle className="sr-only">{t('palette.title')}</DialogTitle>
        <div className="flex shrink-0 items-center gap-2.5 border-b border-border pl-4 pr-12">
          <MagnifyingGlass className="size-4 shrink-0 text-muted-foreground" />
          <input
            role="combobox"
            aria-expanded
            aria-controls={listId}
            aria-autocomplete="list"
            aria-activedescendant={flat.length ? optionId(current) : undefined}
            aria-label={t('palette.title')}
            autoComplete="off"
            autoCorrect="off"
            spellCheck={false}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setActiveIndex(0)
            }}
            onKeyDown={onInputKeyDown}
            placeholder={t('palette.placeholder')}
            className="h-12 w-full min-w-0 bg-transparent text-base outline-none placeholder:text-muted-foreground sm:text-sm"
          />
        </div>

        <div
          ref={listRef}
          id={listId}
          role="listbox"
          aria-label={t('palette.title')}
          className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-2"
        >
          {flat.length === 0 ? (
            <p className="px-3 py-8 text-center text-sm text-muted-foreground">{t('palette.empty')}</p>
          ) : (
            groups.map((group) => {
              const headingId = `${baseId}-group-${group.kind}`
              return (
                <div key={group.kind} role="group" aria-labelledby={headingId} className="pb-1">
                  <div
                    id={headingId}
                    role="presentation"
                    className="px-2.5 pb-1 pt-2 text-[11px] font-medium text-muted-foreground"
                  >
                    {t(GROUP_LABEL[group.kind])}
                  </div>
                  {group.items.map((item) => {
                    const index = indexOf.get(item.id) ?? 0
                    const isActive = index === current
                    const Icon = iconFor(item, theme)
                    return (
                      <div
                        key={item.id}
                        id={optionId(index)}
                        role="option"
                        aria-selected={isActive}
                        data-active={isActive}
                        onMouseMove={() => {
                          if (!isActive) setActiveIndex(index)
                        }}
                        // Keep focus in the input so the keyboard keeps working.
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => run(item)}
                        className={cn(
                          'flex cursor-pointer items-center gap-2.5 rounded-[var(--radius-sm)] px-2.5 py-2 text-sm',
                          isActive ? 'bg-accent text-accent-foreground' : 'text-foreground',
                        )}
                      >
                        <Icon
                          className={cn('size-4 shrink-0', isActive ? 'text-primary' : 'text-muted-foreground')}
                        />
                        <span className="min-w-0 flex-1 truncate">{item.label}</span>
                        {item.off ? (
                          <span
                            title={t('palette.offHint')}
                            className="shrink-0 rounded-[var(--radius-xs)] border border-border px-1.5 py-px text-[10px] font-medium leading-4 text-muted-foreground"
                          >
                            {t('palette.off')}
                          </span>
                        ) : null}
                        {item.hint ? (
                          <span className="hidden max-w-[40%] shrink-0 truncate text-xs text-muted-foreground sm:inline">
                            {item.hint}
                          </span>
                        ) : null}
                      </div>
                    )
                  })}
                </div>
              )
            })
          )}
          {sessions === undefined && !query ? (
            <p className="px-2.5 py-2 text-xs text-muted-foreground">{t('palette.loadingSessions')}</p>
          ) : null}
        </div>

        <div className="hidden shrink-0 items-center gap-4 border-t border-border px-4 py-2 text-[11px] text-muted-foreground sm:flex">
          <span className="flex items-center gap-1.5">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd>
            {t('palette.hintNavigate')}
          </span>
          <span className="flex items-center gap-1.5">
            <Kbd>↵</Kbd>
            {t('palette.hintOpen')}
          </span>
          <span className="flex items-center gap-1.5">
            <Kbd>Esc</Kbd>
            {t('palette.hintClose')}
          </span>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function Kbd({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <kbd
      className={cn(
        'inline-flex h-5 min-w-5 items-center justify-center rounded-[var(--radius-xs)] border border-border bg-muted px-1 font-sans text-[10px] font-medium text-muted-foreground',
        className,
      )}
    >
      {children}
    </kbd>
  )
}

/**
 * Opens the palette. `sidebar` is the full-width search field under the logo;
 * `icon` is the compact button for the mobile header.
 */
export function CommandPaletteTrigger({ variant }: { variant: 'sidebar' | 'icon' }) {
  const { t } = useI18n()
  if (variant === 'icon') {
    return (
      <Button
        variant="ghost"
        size="icon"
        aria-label={t('palette.open')}
        aria-keyshortcuts={SHORTCUT_ARIA}
        onClick={openCommandPalette}
      >
        <MagnifyingGlass />
      </Button>
    )
  }
  return (
    <button
      type="button"
      onClick={openCommandPalette}
      aria-label={t('palette.open')}
      aria-keyshortcuts={SHORTCUT_ARIA}
      className="flex h-9 w-full items-center gap-2 rounded-[var(--radius-sm)] border border-border bg-background/60 px-3 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
    >
      <MagnifyingGlass className="size-4 shrink-0" />
      <span className="flex-1 truncate text-left">{t('palette.search')}</span>
      <Kbd>{SHORTCUT_LABEL}</Kbd>
    </button>
  )
}
