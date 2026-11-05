import { useEffect, useRef, useState } from 'react'
import { ArrowSquareOut, Play, Plus, Robot, TrashSimple } from '@phosphor-icons/react'
import { del, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { usePageActions } from '@/components/layout/PageChrome'
import { Button } from '@/components/ui/button'
import { Badge, EmptyState, Input, Label, Textarea } from '@/components/ui/primitives'
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SkeletonList } from '@/components/ui/skeleton'

interface Card_ {
  id: string
  title: string
  prompt: string
  status: 'pending' | 'running' | 'verified' | 'failed' | 'merged'
  result?: string
  verify_output?: string
  branch?: string
  pr?: string
  error?: string
  created_at: string
  updated_at: string
}

const STATUS_VARIANT: Record<
  Card_['status'],
  'outline' | 'warning' | 'success' | 'destructive' | 'secondary'
> = {
  pending: 'outline',
  running: 'warning',
  verified: 'success',
  merged: 'success',
  failed: 'destructive',
}

const STATUSES: Card_['status'][] = ['pending', 'running', 'verified', 'merged', 'failed']

export default function AutopilotPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<{ cards: Card_[] }>('/autopilot')
  const [adding, setAdding] = useState(false)
  const [running, setRunning] = useState(false)
  const [detail, setDetail] = useState<Card_ | null>(null)
  const [statusFilter, setStatusFilter] = useState<Card_['status'] | ''>('')

  const cards = data?.cards ?? []
  const pending = cards.filter((c) => c.status === 'pending').length
  const active = cards.some((c) => c.status === 'running')

  // Poll while anything is running so status updates land without a refresh.
  const reloadRef = useRef(reload)
  reloadRef.current = reload
  useEffect(() => {
    if (!active) return
    const id = setInterval(() => reloadRef.current(), 2500)
    return () => clearInterval(id)
  }, [active])

  const run = async () => {
    setRunning(true)
    try {
      await post('/autopilot/run', {})
      reload()
    } finally {
      setRunning(false)
    }
  }

  usePageActions(
    <div className="flex items-center gap-2">
      <Button
        size="sm"
        variant="outline"
        onClick={run}
        loading={running}
        disabled={pending === 0}
        className="gap-1.5"
      >
        <Play className="size-4" />
        {t('autopilot.run')} {pending > 0 ? `(${pending})` : ''}
      </Button>
      <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
        <Plus className="size-4" />
        {t('autopilot.add')}
      </Button>
    </div>,
    [t, running, pending],
  )

  const counts = STATUSES.reduce<Record<string, number>>((acc, s) => {
    acc[s] = cards.filter((c) => c.status === s).length
    return acc
  }, {})
  const shown = statusFilter ? cards.filter((c) => c.status === statusFilter) : cards

  // Status filter chips stay in view while the grid scrolls.
  const header = cards.length ? (
    <div className="flex flex-wrap gap-1.5">
      <Chip active={statusFilter === ''} onClick={() => setStatusFilter('')}>
        {t('autopilot.all')} <span className="opacity-60">{cards.length}</span>
      </Chip>
      {STATUSES.filter((s) => counts[s] > 0).map((s) => (
        <Chip key={s} active={statusFilter === s} onClick={() => setStatusFilter(s)}>
          {t(`autopilot.status.${s}` as never)} <span className="opacity-60">{counts[s]}</span>
        </Chip>
      ))}
    </div>
  ) : undefined

  return (
    <PageLayout header={header}>
      <AddDialog open={adding} onOpenChange={setAdding} onAdded={reload} />
      {detail ? <DetailDialog card={detail} onClose={() => setDetail(null)} /> : null}

      {loading && !data ? (
        <SkeletonList count={3} />
      ) : cards.length === 0 ? (
        <EmptyState
          icon={<Robot className="size-8" />}
          title={t('autopilot.empty')}
          description={t('autopilot.emptyDesc')}
        />
      ) : (
        <div className="space-y-6">
          <Pipeline counts={counts} />
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {shown.map((c) => (
              <div
                key={c.id}
                data-reveal
                className="tp-panel group relative flex flex-col rounded-[var(--radius-lg)] border border-border bg-card p-4 transition-[border-color,background-color] duration-200 hover:border-line"
              >
                {c.status === 'running' ? (
                  <span
                    aria-hidden
                    className="absolute inset-x-6 -top-px h-[2px] origin-left rounded-full bg-foreground motion-safe:animate-[m-rule_700ms_var(--m-ease)_both]"
                  />
                ) : null}
                <button onClick={() => setDetail(c)} className="min-w-0 flex-1 text-left">
                  <div className="flex items-start justify-between gap-2">
                    <span className="min-w-0 truncate text-sm font-medium">{c.title}</span>
                    <Badge variant={STATUS_VARIANT[c.status]} className="shrink-0">
                      {t(`autopilot.status.${c.status}` as never)}
                    </Badge>
                  </div>
                  <p className="mt-2 line-clamp-2 text-sm text-muted-foreground">{c.prompt}</p>
                  {c.error ? (
                    <p className="mt-2 line-clamp-2 text-xs text-destructive">{c.error}</p>
                  ) : null}
                </button>
                <div className="-mx-4 mt-3.5 flex items-center gap-2 border-t border-border px-4 pt-2.5">
                  <StageTrack status={c.status} />
                  {c.pr ? (
                    <a
                      href={c.pr}
                      target="_blank"
                      rel="noreferrer noopener"
                      onClick={(e) => e.stopPropagation()}
                      className="inline-flex items-center gap-1 font-mono text-[11px] text-foreground underline decoration-line underline-offset-4 hover:decoration-foreground"
                    >
                      <ArrowSquareOut className="size-3.5" /> PR
                    </a>
                  ) : null}
                  <span className="ml-auto font-mono text-[11px] text-dim">
                    <TimeAgo iso={c.updated_at} />
                  </span>
                  {c.status === 'pending' || c.status === 'failed' ? (
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={t('common.remove')}
                      onClick={async () => {
                        await del(`/autopilot/${c.id}`)
                        reload()
                      }}
                      className="text-muted-foreground hover:text-destructive"
                    >
                      <TrashSimple className="size-4" />
                    </Button>
                  ) : null}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </PageLayout>
  )
}

// The path a card walks. Failed sits off it, so it is drawn as a mark, not a stage.
const STAGES: Card_['status'][] = ['pending', 'running', 'verified', 'merged']

/** Stage tiles: a dot that fills once a stage holds cards, and a bar over the one working now. */
function Pipeline({ counts }: { counts: Record<string, number> }) {
  const { t } = useI18n()
  const live = counts.running > 0
  return (
    <ol
      data-reveal
      className="grid grid-cols-2 gap-2 sm:grid-cols-5"
    >
      {[...STAGES, 'failed' as const].map((s) => {
        const n = counts[s] ?? 0
        const failed = s === 'failed'
        return (
          <li
            key={s}
            aria-current={live && s === 'running' ? 'step' : undefined}
            className={cn('relative rounded-[var(--radius-lg)] border border-border bg-card px-4 py-3.5', failed && 'col-span-2 sm:col-span-1')}
          >
            {live && s === 'running' ? (
              <span
                aria-hidden
                className="absolute inset-x-6 -top-px h-[2px] origin-left rounded-full bg-foreground motion-safe:animate-[m-rule_700ms_var(--m-ease)_both]"
              />
            ) : null}
            <p className="eyebrow flex items-center gap-2">
              <span
                aria-hidden
                className={cn(
                  'size-[7px] shrink-0 rounded-full',
                  n === 0
                    ? 'shadow-[inset_0_0_0_1px_var(--line)]'
                    : failed
                      ? 'bg-destructive'
                      : 'bg-foreground',
                )}
              />
              {t(`autopilot.status.${s}` as never)}
            </p>
            <p
              className={cn(
                'mt-2 text-2xl font-medium tabular-nums',
                n === 0 && 'text-dim',
                failed && n > 0 && 'text-destructive',
              )}
            >
              {n}
            </p>
          </li>
        )
      })}
    </ol>
  )
}

/** Where one card is on the path, as four small dots. */
function StageTrack({ status }: { status: Card_['status'] }) {
  const reached = status === 'failed' ? 1 : STAGES.indexOf(status)
  return (
    <span aria-hidden className="flex items-center gap-1">
      {STAGES.map((s, i) => (
        <span
          key={s}
          className={cn(
            'size-[6px] rounded-full',
            i < reached || (i === reached && status !== 'failed')
              ? 'bg-foreground'
              : status === 'failed' && i === reached
                ? 'bg-destructive'
                : 'shadow-[inset_0_0_0_1px_var(--line)]',
          )}
        />
      ))}
    </span>
  )
}

function TimeAgo({ iso }: { iso: string }) {
  const timeAgo = useTimeAgo()
  return <>{timeAgo(iso)}</>
}

function Chip({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        'rounded-full border px-3 py-1.5 text-xs transition-colors',
        active
          ? 'border-transparent bg-nav-active text-foreground'
          : 'border-border text-muted-foreground hover:text-foreground',
      )}
    >
      {children}
    </button>
  )
}

// Full view of one card: prompt, result, verify output, branch, PR.
function DetailDialog({ card, onClose }: { card: Card_; onClose: () => void }) {
  const { t } = useI18n()
  const block = (label: string, value?: string, mono = false) =>
    value ? (
      <div className="space-y-1">
        <p className="eyebrow">
          {label}
        </p>
        <pre
          className={cn(
            'max-h-56 overflow-auto whitespace-pre-wrap break-words rounded-[var(--radius-lg)] border border-border bg-background p-3 text-xs leading-relaxed',
            mono && 'font-mono',
          )}
        >
          {value}
        </pre>
      </div>
    ) : null

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : null)}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <span className="min-w-0 truncate">{card.title}</span>
            <Badge variant={STATUS_VARIANT[card.status]}>
              {t(`autopilot.status.${card.status}` as never)}
            </Badge>
          </DialogTitle>
          <DialogDescription className="font-mono text-[11px]">{card.id}</DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-3.5">
          {block(t('autopilot.cardPrompt'), card.prompt)}
          {block(t('autopilot.detailError'), card.error)}
          {block(t('autopilot.detailResult'), card.result)}
          {block(t('autopilot.detailVerify'), card.verify_output, true)}
          {card.branch ? (
            <div className="flex items-center gap-2 text-xs">
              <span className="text-muted-foreground">{t('autopilot.detailBranch')}</span>
              <code className="rounded-full border border-border px-2 py-0.5 font-mono text-[11px]">
                {card.branch}
              </code>
            </div>
          ) : null}
          {card.pr ? (
            <a
              href={card.pr}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1.5 break-all font-mono text-xs text-foreground underline decoration-line underline-offset-4 hover:decoration-foreground"
            >
              <ArrowSquareOut className="size-4" /> {card.pr}
            </a>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function AddDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onAdded: () => void
}) {
  const { t } = useI18n()
  const [title, setTitle] = useState('')
  const [prompt, setPrompt] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (open) {
      setTitle('')
      setPrompt('')
    }
  }, [open])

  const save = async () => {
    if (!title.trim() || !prompt.trim()) return
    setBusy(true)
    try {
      await post('/autopilot', { title: title.trim(), prompt: prompt.trim() })
      onAdded()
      onOpenChange(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('autopilot.add')}</DialogTitle>
          <DialogDescription>{t('autopilot.addDesc')}</DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="ap-title">{t('autopilot.cardTitle')}</Label>
            <Input
              id="ap-title"
              autoFocus
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder={t('autopilot.cardTitlePlaceholder')}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="ap-prompt">{t('autopilot.cardPrompt')}</Label>
            <Textarea
              id="ap-prompt"
              rows={5}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder={t('autopilot.cardPromptPlaceholder')}
            />
          </div>
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          <Button size="sm" onClick={save} loading={busy} disabled={!title.trim() || !prompt.trim()}>
            {t('autopilot.add')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
