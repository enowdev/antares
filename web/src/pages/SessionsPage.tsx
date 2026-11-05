import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ChatCircleDots, Broom, FolderOpen, MagnifyingGlass, Trash } from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { formatCount } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Pagination } from '@/components/ui/Pagination'
import { Button } from '@/components/ui/button'
import { Badge, EmptyState, Input, Tabs, TabsList, TabsTrigger } from '@/components/ui/primitives'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { SkeletonList } from '@/components/ui/skeleton'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { usePageActions } from '@/components/layout/PageChrome'

interface Session {
  id: string
  title: string
  platform: string
  model: string
  message_count: number
  tokens_in: number
  tokens_out: number
  updated_at: string
  meta?: { project_dir?: string } | null
}

interface SessionList {
  sessions: Session[]
  total: number
}

interface SearchHit {
  session_id: string
  session_title: string
  role: string
  snippet: string
  created_at: string
}

export default function SessionsPage() {
  const navigate = useNavigate()
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const [busy, setBusy] = useState(false)
  const [hits, setHits] = useState<SearchHit[] | null>(null)
  const [offset, setOffset] = useState(0)
  const [tab, setTab] = useState<'chat' | 'project'>('chat')
  const [showDeleteAll, setShowDeleteAll] = useState(false)
  const PAGE = 20

  const { data, loading, reload } = useApi<SessionList>('/sessions?limit=100')
  const { data: emptyCount, reload: reloadEmpty } = useApi<{ count: number }>('/sessions/empty/count')

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query.trim()), 300)
    return () => clearTimeout(timer)
  }, [query])

  useEffect(() => {
    if (debounced.length < 2) {
      setHits(null)
      return
    }
    let alive = true
    get<{ hits: SearchHit[] }>(`/sessions/search?q=${encodeURIComponent(debounced)}`)
      .then((r) => alive && setHits(r.hits))
      .catch(() => alive && setHits([]))
    return () => {
      alive = false
    }
  }, [debounced])

  const isProject = (s: Session) => Boolean(s.meta?.project_dir)

  // Counts per tab, before the search/text filter, so the tab labels are stable.
  const counts = useMemo(() => {
    const all = data?.sessions ?? []
    let project = 0
    for (const s of all) if (isProject(s)) project++
    return { chat: all.length - project, project }
  }, [data])

  const sessions = useMemo(() => {
    let all = data?.sessions ?? []
    all = all.filter((s) => (tab === 'project' ? isProject(s) : !isProject(s)))
    if (!debounced) return all
    const q = debounced.toLowerCase()
    return all.filter((s) => s.title.toLowerCase().includes(q))
  }, [data, debounced, tab])

  // Reset to the first page whenever the filter changes the result set.
  useEffect(() => setOffset(0), [debounced, tab])
  const paged = sessions.slice(offset, offset + PAGE)

  const removeSession = async (id: string) => {
    setBusy(true)
    try {
      await del(`/sessions/${id}`)
      reload()
      reloadEmpty()
    } finally {
      setBusy(false)
    }
  }

  const purgeEmpty = async () => {
    setBusy(true)
    try {
      await post('/sessions/empty/delete')
      reload()
      reloadEmpty()
    } finally {
      setBusy(false)
    }
  }

  const deleteAllInCategory = async () => {
    setBusy(true)
    try {
      await post('/sessions/delete-all', { category: tab })
      setShowDeleteAll(false)
      reload()
      reloadEmpty()
    } catch {
      /* handled by error state */
    } finally {
      setBusy(false)
    }
  }

  usePageActions(
    <>
      {emptyCount && emptyCount.count > 0 ? (
        <Button variant="outline" size="sm" onClick={purgeEmpty} loading={busy} className="gap-1.5">
          <Broom className="size-4" />
          <span className="hidden sm:inline">{t('sessions.cleanEmpty', { n: emptyCount.count })}</span>
          <span className="sm:hidden">{emptyCount.count}</span>
        </Button>
      ) : null}
      {sessions.length > 0 ? (
        <Button variant="ghost" size="sm" onClick={() => setShowDeleteAll(true)} disabled={busy} className="gap-1.5 text-muted-foreground hover:text-destructive">
          <Trash className="size-4" />
          <span className="hidden sm:inline">{t('sessions.deleteAll')}</span>
        </Button>
      ) : null}
      <Button size="sm" onClick={() => navigate('/', { state: { fresh: true } })} className="gap-1.5">
        <ChatCircleDots className="size-4" />
        {t('common.new')}
      </Button>
    </>,
    [emptyCount, busy, t, sessions.length],
  )

  return (
    <PageLayout
      header={
        <div className="space-y-3">
          <Tabs value={tab} onValueChange={(v) => setTab(v as 'chat' | 'project')}>
            <TabsList>
              <TabsTrigger value="chat" className="gap-1.5">
                <ChatCircleDots className="size-3.5" /> {t('sessions.tabChat')} ({counts.chat})
              </TabsTrigger>
              <TabsTrigger value="project" className="gap-1.5">
                <FolderOpen className="size-3.5" /> {t('sessions.tabProject')} ({counts.project})
              </TabsTrigger>
            </TabsList>
          </Tabs>
          <div className="relative">
            <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('sessions.searchPlaceholder')}
              className="pl-9"
            />
          </div>
        </div>
      }
      footer={
        !debounced ? (
          <Pagination offset={offset} limit={PAGE} total={sessions.length} onChange={setOffset} />
        ) : undefined
      }
    >
      {hits && hits.length > 0 ? (
        <section className="m-rise space-y-2">
          <p className="eyebrow">
            {t('sessions.contentMatches', { n: hits.length })}
          </p>
          {hits.map((h, i) => (
            <Link key={`${h.session_id}-${i}`} to={`/c/${h.session_id}`} className="block">
              <div className="rounded-[var(--radius-lg)] border border-border bg-transparent transition-[border-color,background-color] duration-200 hover:border-line hover:bg-raised p-3.5">
                <div className="flex items-center gap-2">
                  <Badge variant="outline">{h.role}</Badge>
                  <span className="truncate text-xs font-medium">{h.session_title || h.session_id}</span>
                  <span className="ml-auto shrink-0 font-mono text-[11px] text-dim">
                    {timeAgo(h.created_at)}
                  </span>
                </div>
                <p className="mt-1.5 line-clamp-2 text-xs leading-relaxed text-muted-foreground">{h.snippet}</p>
              </div>
            </Link>
          ))}
        </section>
      ) : null}

      {loading && !data ? (
        <SkeletonList count={6} />
      ) : sessions.length === 0 ? (
        <EmptyState
          icon={tab === 'project' ? <FolderOpen className="size-8" /> : <ChatCircleDots className="size-8" />}
          title={tab === 'project' ? t('sessions.noneProject') : t('sessions.none')}
          description={tab === 'project' ? t('sessions.noneProjectDesc') : t('sessions.noneDesc')}
          action={
            <Button size="sm" onClick={() => navigate('/', { state: { fresh: true } })}>
              {t('sessions.startChat')}
            </Button>
          }
        />
      ) : (
        <div className="space-y-2">
          {paged.map((s) => {
            const projectName = s.meta?.project_dir
              ? s.meta.project_dir.split('/').filter(Boolean).pop()
              : ''
            return (
            <div key={s.id} data-reveal className="group flex items-center gap-3 rounded-[var(--radius-md)] border border-border bg-transparent transition-[border-color,background-color] duration-200 hover:border-line hover:bg-raised px-4 py-3.5">
              <Link to={`/c/${s.id}`} className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{s.title || t('sessions.untitled')}</p>
                <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] tabular-nums text-muted-foreground">
                  {projectName ? (
                    <span
                      className="inline-flex items-center gap-1 rounded-full bg-raised px-2 py-0.5 font-mono text-foreground"
                      title={s.meta?.project_dir}
                    >
                      <FolderOpen className="size-3" />
                      {projectName}
                    </span>
                  ) : (
                    <Badge variant="outline">{s.platform}</Badge>
                  )}
                  <span>{t('sessions.messages', { n: s.message_count })}</span>
                  <span>·</span>
                  <span>{t('sessions.tokens', { n: formatCount(s.tokens_in + s.tokens_out) })}</span>
                  <span>·</span>
                  <span>{timeAgo(s.updated_at)}</span>
                </div>
              </Link>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t('sessions.deleteOne')}
                disabled={busy}
                onClick={() => removeSession(s.id)}
                className="shrink-0 text-muted-foreground hover:text-destructive"
              >
                <Trash className="size-4" />
              </Button>
            </div>
            )
          })}
        </div>
      )}

      <ConfirmDialog
        open={showDeleteAll}
        onOpenChange={(o) => !o && setShowDeleteAll(o)}
        title={t('sessions.deleteAllTitle')}
        description={t('sessions.deleteAllDesc', { category: tab === 'project' ? t('sessions.tabProject') : t('sessions.tabChat'), count: sessions.length })}
        confirmLabel={t('common.delete')}
        onConfirm={deleteAllInCategory}
      />
    </PageLayout>
  )
}
