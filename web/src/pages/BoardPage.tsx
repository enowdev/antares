import { useEffect, useState } from 'react'
import { Kanban, TrashSimple, X } from '@phosphor-icons/react'
import { del, streamGet } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'

interface BoardCard {
  id: string
  title: string
  note?: string
  column: string
}
interface Column {
  name: string
  cards: BoardCard[]
}
interface SessionRow {
  id: string
  title: string
}

const COLUMN_TITLES: Record<string, string> = {
  todo: 'To do',
  doing: 'Doing',
  done: 'Done',
}
// Lanes read as stages: an empty square for work not started, a filled one
// once work is under way or done.
const COLUMN_MARK: Record<string, string> = {
  todo: 'shadow-[inset_0_0_0_1px_var(--line)]',
  doing: 'bg-foreground',
  done: 'bg-foreground',
}

export default function BoardPage() {
  const { t } = useI18n()
  const { data: sessData, loading, reload: reloadSessions } = useApi<{ sessions: SessionRow[] }>(
    '/board/sessions',
  )
  const sessions = sessData?.sessions ?? []
  const [sessionID, setSessionID] = useState('')
  const active = sessionID || sessions[0]?.id || ''

  // Subscribe to the board's SSE stream: the server pushes the whole board once
  // immediately and again on every change, so the columns track the agent's
  // task list live, with no polling.
  const [columns, setColumns] = useState<Column[]>([])
  useEffect(() => {
    if (!active) {
      setColumns([])
      return
    }
    const close = streamGet(`/board/stream?session=${encodeURIComponent(active)}`, (e) => {
      const cols = (e as unknown as { columns?: Column[] }).columns
      if (cols) setColumns(cols)
    })
    return close
  }, [active])

  const removeCard = (id: string) =>
    del(`/board/card?session=${encodeURIComponent(active)}&id=${encodeURIComponent(id)}`).catch(
      () => {},
    )

  const clearBoard = () => {
    if (!active) return
    del(`/board?session=${encodeURIComponent(active)}`)
      .then(() => reloadSessions())
      .catch(() => {})
  }

  if (loading) {
    return (
      <PageLayout>
        <SkeletonList count={2} />
      </PageLayout>
    )
  }
  if (sessions.length === 0) {
    return (
      <PageLayout>
        <EmptyState
          icon={<Kanban className="size-8" />}
          title={t('board.empty')}
          description={t('board.emptyDesc')}
        />
      </PageLayout>
    )
  }

  const total = columns.reduce((n, c) => n + c.cards.length, 0)
  const done = columns.find((c) => c.name === 'done')?.cards.length ?? 0

  const header = (
    <div className="flex flex-wrap items-center gap-2">
      <label className="eyebrow">{t('board.pickSession')}</label>
      <select
        value={active}
        onChange={(e) => setSessionID(e.target.value)}
        className="h-8 min-w-0 flex-1 border border-border bg-transparent px-2 text-sm hover:border-line focus-visible:border-foreground focus-visible:outline-none sm:flex-none"
      >
        {sessions.map((s) => (
          <option key={s.id} value={s.id}>
            {s.title || s.id}
          </option>
        ))}
      </select>
      {total > 0 ? (
        <span className="font-mono text-xs tabular-nums text-muted-foreground">
          {t('board.summary', { done, total })}
        </span>
      ) : null}
      <Button
        variant="outline"
        size="sm"
        onClick={clearBoard}
        disabled={!active || total === 0}
        className="ml-auto gap-1.5"
      >
        <TrashSimple className="size-3.5" />
        <span className="hidden sm:inline">{t('common.clear')}</span>
      </Button>
    </div>
  )

  return (
    <PageLayout header={header}>
      {total === 0 ? (
        <EmptyState
          icon={<Kanban className="size-8" />}
          title={t('board.noCards')}
          description={t('board.noCardsDesc')}
        />
      ) : (
        <div className="grid gap-3 md:grid-cols-3">
          {columns.map((col) => (
            <div
              key={col.name}
              data-reveal
              className="tp-panel relative flex min-h-0 flex-col border border-border bg-card"
            >
              {col.name === 'doing' && col.cards.length > 0 ? (
                <span
                  aria-hidden
                  className="absolute inset-x-0 -top-px h-px origin-left bg-foreground motion-safe:animate-[m-rule_700ms_var(--m-ease)_both]"
                />
              ) : null}
              <div className="flex items-center gap-2.5 border-b border-border px-4 py-3">
                <span
                  aria-hidden
                  className={cn(
                    'size-[7px] shrink-0',
                    COLUMN_MARK[col.name] ?? 'shadow-[inset_0_0_0_1px_var(--line)]',
                  )}
                />
                <span className="eyebrow text-foreground">
                  {COLUMN_TITLES[col.name] ?? col.name}
                </span>
                <span className="ml-auto font-mono text-xs tabular-nums text-dim">
                  {String(col.cards.length).padStart(2, '0')}
                </span>
              </div>
              <div className="space-y-2 p-3">
                {col.cards.length === 0 ? (
                  <div className="border border-dashed border-border py-6 text-center text-xs text-dim">
                    {t('board.columnEmpty')}
                  </div>
                ) : (
                  col.cards.map((c) => (
                    <div
                      key={c.id}
                      data-reveal
                      className="group relative border border-border bg-transparent px-3 py-2.5 transition-[border-color,background-color] duration-200 hover:border-line hover:bg-raised"
                    >
                      <button
                        type="button"
                        onClick={() => removeCard(c.id)}
                        aria-label={t('common.delete')}
                        className="absolute right-1 top-1 p-1 text-muted-foreground opacity-0 transition hover:text-destructive focus-visible:opacity-100 group-hover:opacity-100"
                      >
                        <X className="size-3.5" />
                      </button>
                      <p
                        className={cn(
                          'pr-5 text-[13px] font-medium leading-snug',
                          col.name === 'done' && 'text-muted-foreground line-through',
                        )}
                      >
                        {c.title}
                      </p>
                      {c.note ? (
                        <p className="mt-1 text-xs text-muted-foreground">{c.note}</p>
                      ) : null}
                    </div>
                  ))
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </PageLayout>
  )
}
