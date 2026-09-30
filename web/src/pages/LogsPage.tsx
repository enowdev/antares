import { useEffect, useMemo, useRef, useState } from 'react'
import { Broom, DownloadSimple, Pause, Play, Terminal } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import { EmptyState, Input, Tabs, TabsList, TabsTrigger } from '@/components/ui/primitives'
import { Skeleton } from '@/components/ui/skeleton'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { usePageActions } from '@/components/layout/PageChrome'

interface LogEntry {
  time: string
  level: string
  message: string
  attrs?: Record<string, unknown>
}

const LEVELS = ['ALL', 'DEBUG', 'INFO', 'WARN', 'ERROR']

// Only warnings and errors carry colour; routine lines stay in greys.
const LEVEL_TONE: Record<string, string> = {
  DEBUG: 'text-dim',
  INFO: 'text-muted-foreground',
  WARN: 'text-[var(--warning)]',
  ERROR: 'text-destructive',
}

const LEVEL_DOT: Record<string, string> = {
  DEBUG: 'bg-line',
  INFO: 'bg-muted-foreground',
  WARN: 'bg-[var(--warning)]',
  ERROR: 'bg-destructive',
}

export default function LogsPage() {
  const { t, locale } = useI18n()
  const timeAgo = useTimeAgo()
  const [entries, setEntries] = useState<LogEntry[]>([])
  const [level, setLevel] = useState('ALL')
  const [filter, setFilter] = useState('')
  const [live, setLive] = useState(true)
  const [loading, setLoading] = useState(true)
  const bottomRef = useRef<HTMLDivElement>(null)

  usePageActions(
    <>
      <Button variant="outline" size="sm" onClick={() => setLive((v) => !v)} className="gap-1.5">
        {live ? <Pause className="size-4" /> : <Play className="size-4" />}
        {live ? t('common.pause') : t('common.resume')}
      </Button>
      <Button variant="outline" size="sm" onClick={() => download()} className="gap-1.5">
        <DownloadSimple className="size-4" />
        <span className="hidden sm:inline">{t('logs.download')}</span>
      </Button>
      <Button variant="outline" size="sm" onClick={() => setEntries([])} className="gap-1.5">
        <Broom className="size-4" />
        <span className="hidden sm:inline">{t('common.clear')}</span>
      </Button>
    </>,
    [live, t],
  )

  useEffect(() => {
    let alive = true
    let timer: ReturnType<typeof setInterval> | undefined

    const load = () =>
      get<{ entries: LogEntry[] }>(`/logs?limit=500&level=${level}`)
        .then((r) => {
          if (alive) setEntries(r.entries ?? [])
        })
        .catch(() => {})
        .finally(() => {
          if (alive) setLoading(false)
        })

    load()
    if (live) timer = setInterval(load, 2000)

    return () => {
      alive = false
      if (timer) clearInterval(timer)
    }
  }, [level, live])

  useEffect(() => {
    if (live) bottomRef.current?.scrollIntoView({ block: 'end' })
  }, [entries, live])

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase()
    if (!q) return entries
    return entries.filter((e) => e.message.toLowerCase().includes(q))
  }, [entries, filter])

  // Count entries per level for the tab badges (from the full unfiltered set).
  const counts = useMemo(() => {
    const c: Record<string, number> = {}
    for (const e of entries) c[e.level] = (c[e.level] ?? 0) + 1
    return c
  }, [entries])

  // Download the currently visible lines as a plain text file, client-side.
  const download = () => {
    const text = visible
      .map((e) => {
        const attrs = e.attrs
          ? ' ' +
            Object.entries(e.attrs)
              .map(([k, v]) => `${k}=${String(v)}`)
              .join(' ')
          : ''
        return `${e.time} ${e.level} ${e.message}${attrs}`
      })
      .join('\n')
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `antares-logs-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.log`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  }

  return (
    <PageLayout
      header={
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Tabs value={level} onValueChange={setLevel}>
            <TabsList>
              {LEVELS.map((l) => {
                const n = l === 'ALL' ? entries.length : (counts[l] ?? 0)
                return (
                  <TabsTrigger key={l} value={l} className="gap-1.5 font-mono text-[11px] lowercase">
                    {l}
                    {n > 0 ? (
                      <span
                        className={cn(
                          'rounded-full px-1.5 text-[10px] tabular-nums',
                          l === 'ERROR' && counts.ERROR
                            ? 'bg-destructive/15 text-destructive'
                            : l === 'WARN' && counts.WARN
                              ? 'bg-[color-mix(in_oklch,var(--warning)_15%,transparent)] text-[var(--warning)]'
                              : 'bg-raised text-muted-foreground',
                        )}
                      >
                        {n}
                      </span>
                    ) : null}
                  </TabsTrigger>
                )
              })}
            </TabsList>
          </Tabs>
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t('logs.filterPlaceholder')}
            className="sm:max-w-xs"
          />
        </div>
      }
    >
      {loading ? (
        <div className="space-y-2 border border-border bg-card p-4">
          {Array.from({ length: 12 }).map((_, i) => (
            <Skeleton key={i} className="h-4" style={{ width: `${60 + ((i * 37) % 40)}%` }} />
          ))}
        </div>
      ) : visible.length === 0 ? (
        <EmptyState icon={<Terminal className="size-8" />} title={t('logs.none')} />
      ) : (
        <div className="tp-panel overflow-hidden border border-border bg-card">
          <div className="px-4 py-3 font-mono text-[11px] leading-relaxed">
            {visible.map((e, i) => (
              <div key={i} className="-mx-2 flex items-baseline gap-2.5 px-2 py-0.5 transition-colors duration-150 hover:bg-raised">
                <span
                  className={cn(
                    'mt-1.5 size-1.5 shrink-0 self-start rounded-full',
                    LEVEL_DOT[e.level] ?? 'bg-muted-foreground',
                  )}
                />
                <span className="shrink-0 tabular-nums text-dim">
                  {new Date(e.time).toLocaleTimeString(locale, { hour12: false })}
                </span>
                <span className={cn('w-12 shrink-0 lowercase', LEVEL_TONE[e.level] ?? '')}>
                  {e.level}
                </span>
                <span className="min-w-0 break-words text-foreground">
                  {e.message}
                  {e.attrs
                    ? Object.entries(e.attrs).map(([k, v]) => (
                        <span key={k} className="ml-2 text-dim">
                          {k}={String(v)}
                        </span>
                      ))
                    : null}
                </span>
              </div>
            ))}
            <div ref={bottomRef} />
          </div>
        </div>
      )}
      <p className="font-mono text-[11px] tabular-nums text-dim">
        {t('logs.lines', { n: visible.length, time: timeAgo(new Date()) })}
      </p>
    </PageLayout>
  )
}
