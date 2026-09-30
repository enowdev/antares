import { useEffect, useState } from 'react'
import {
  CheckCircle,
  ClockCounterClockwise,
  Play,
  Plus,
  TrashSimple,
  Warning,
  XCircle,
} from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { usePageActions } from '@/components/layout/PageChrome'
import { Button } from '@/components/ui/button'
import {
  Badge,
  EmptyState,
  Input,
  Label,
  Switch,
  Textarea,
} from '@/components/ui/primitives'
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
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'

interface CronJob {
  id: string
  name: string
  schedule: string
  prompt: string
  enabled: boolean
  target: string
  last_run: string | null
  next_run: string | null
  last_state: string
}

interface CronRun {
  id: string
  status: string
  output: string
  error: string
  started_at: string
  finished_at: string | null
}

const EMPTY_DRAFT = { name: '', schedule: '0 8 * * *', prompt: '' }

export default function CronPage() {
  const { t, locale } = useI18n()
  const timeAgo = useTimeAgo()
  const { data, loading, reload } = useApi<{ jobs: CronJob[] }>('/cron/jobs')
  const [busy, setBusy] = useState('')
  const [open, setOpen] = useState(false)
  const [viewing, setViewing] = useState<CronJob | null>(null)

  usePageActions(
    <Button size="sm" onClick={() => setOpen(true)} className="gap-1.5">
      <Plus className="size-4" />
      {t('common.new')}
    </Button>,
    [t],
  )

  const act = async (id: string, fn: () => Promise<unknown>) => {
    setBusy(id)
    try {
      await fn()
      reload()
    } finally {
      setBusy('')
    }
  }

  const jobs = data?.jobs ?? []

  return (
    <PageLayout>
      <NewJobDialog open={open} onOpenChange={setOpen} onCreated={reload} />
      {viewing ? <RunsDialog job={viewing} onClose={() => setViewing(null)} /> : null}

      {loading && !data ? (
        <SkeletonList count={3} />
      ) : jobs.length === 0 ? (
        <EmptyState
          icon={<ClockCounterClockwise className="size-8" />}
          title={t('cron.none')}
          description={t('cron.noneDesc')}
          action={
            <Button size="sm" onClick={() => setOpen(true)} className="gap-1.5">
              <Plus className="size-4" />
              {t('cron.create')}
            </Button>
          }
        />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {jobs.map((j) => (
            <div
              key={j.id}
              data-reveal
              className={cn(
                'tp-panel group flex flex-col border border-border bg-card p-4 transition-[border-color,background-color] duration-200 hover:border-line',
                !j.enabled && 'opacity-60',
              )}
            >
              <button onClick={() => setViewing(j)} className="min-w-0 flex-1 text-left">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="min-w-0 truncate text-sm font-medium">{j.name}</span>
                  {j.last_state ? (
                    <Badge variant={j.last_state === 'ok' ? 'success' : 'destructive'}>
                      {j.last_state}
                    </Badge>
                  ) : null}
                </div>
                <p className="mt-2 inline-block border border-border px-2 py-0.5 font-mono text-xs text-foreground">
                  {j.schedule}
                </p>
                <p className="mt-2.5 line-clamp-2 text-sm text-muted-foreground">{j.prompt}</p>
                <p className="mt-2.5 font-mono text-[11px] text-dim">
                  {t('cron.lastRun', {
                    time: timeAgo(j.last_run),
                    next: j.next_run ? new Date(j.next_run).toLocaleString(locale) : '—',
                  })}
                </p>
              </button>
              <div className="-mx-4 mt-3.5 flex items-center justify-between border-t border-border px-4 pt-2.5">
                <label className="flex items-center gap-2 font-mono text-[11px] lowercase text-muted-foreground">
                  <Switch
                    checked={j.enabled}
                    disabled={busy === j.id}
                    onCheckedChange={(v) =>
                      act(j.id, () => post(`/cron/jobs/${j.id}/toggle`, { enabled: v }))
                    }
                    aria-label={t('common.enable')}
                  />
                  {j.enabled ? t('cron.on') : t('cron.off')}
                </label>
                <div className="flex items-center gap-1">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    disabled={busy === j.id}
                    onClick={() => act(j.id, () => post(`/cron/jobs/${j.id}/run`))}
                    aria-label={t('common.run')}
                  >
                    <Play className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    disabled={busy === j.id}
                    onClick={() => act(j.id, () => del(`/cron/jobs/${j.id}`))}
                    aria-label={t('common.delete')}
                    className="text-muted-foreground hover:text-destructive"
                  >
                    <TrashSimple className="size-4" />
                  </Button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </PageLayout>
  )
}

// A job's execution history — surfaced from /cron/jobs/{id}/runs (was unused).
function RunsDialog({ job, onClose }: { job: CronJob; onClose: () => void }) {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const { data, loading } = useApi<{ runs: CronRun[] }>(`/cron/jobs/${job.id}/runs`, [job.id])
  const runs = data?.runs ?? []

  const glyph = (status: string) =>
    status === 'ok' ? (
      <CheckCircle className="size-3.5 shrink-0 text-[var(--success)]" weight="fill" />
    ) : status === 'error' ? (
      <XCircle className="size-3.5 shrink-0 text-destructive" weight="fill" />
    ) : status === 'running' ? (
      <ClockCounterClockwise className="size-3.5 shrink-0 animate-spin text-foreground" />
    ) : (
      <Warning className="size-3.5 shrink-0 text-muted-foreground" />
    )

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : null)}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{job.name}</DialogTitle>
          <DialogDescription>
            <span className="font-mono">{job.schedule}</span> · {t('cron.runHistory')}
          </DialogDescription>
        </DialogHeader>
        <DialogBody>
          {loading && !data ? (
            <Skeleton className="h-24 w-full" />
          ) : runs.length === 0 ? (
            <p className="text-xs text-muted-foreground">{t('cron.noRuns')}</p>
          ) : (
            <div className="space-y-2">
              {runs.map((r) => (
                <div key={r.id} data-reveal className="border border-border px-3.5 py-3 transition-colors duration-200 hover:border-line hover:bg-raised">
                  <div className="flex items-center gap-2">
                    {glyph(r.status)}
                    <span className="font-mono text-xs">{r.status}</span>
                    <span className="ml-auto font-mono text-[11px] text-dim">
                      {timeAgo(r.started_at)}
                    </span>
                  </div>
                  {r.error ? (
                    <p className="mt-1.5 whitespace-pre-wrap break-words text-[11px] text-destructive">
                      {r.error}
                    </p>
                  ) : r.output ? (
                    <pre className="mt-1.5 max-h-48 overflow-auto whitespace-pre-wrap break-words font-mono text-[10px] leading-relaxed text-muted-foreground">
                      {r.output}
                    </pre>
                  ) : null}
                </div>
              ))}
            </div>
          )}
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

function NewJobDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onCreated: () => void
}) {
  const { t, locale } = useI18n()
  const [draft, setDraft] = useState(EMPTY_DRAFT)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [preview, setPreview] = useState<{ valid: boolean; upcoming?: string[]; error?: string }>()

  useEffect(() => {
    if (open) {
      setDraft(EMPTY_DRAFT)
      setError(undefined)
    }
  }, [open])

  useEffect(() => {
    const expr = draft.schedule.trim()
    if (!open || !expr) {
      setPreview(undefined)
      return
    }
    const timer = setTimeout(() => {
      get<{ valid: boolean; upcoming?: string[]; error?: string }>(
        `/cron/validate?schedule=${encodeURIComponent(expr)}`,
      )
        .then(setPreview)
        .catch(() => setPreview(undefined))
    }, 350)
    return () => clearTimeout(timer)
  }, [draft.schedule, open])

  const create = async () => {
    if (!draft.name.trim() || !draft.prompt.trim()) return
    setSaving(true)
    setError(undefined)
    try {
      await post('/cron/jobs', draft)
      onCreated()
      onOpenChange(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const invalid = preview?.valid === false || !draft.name.trim() || !draft.prompt.trim()

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('cron.newSchedule')}</DialogTitle>
          <DialogDescription>{t('cron.newScheduleDesc')}</DialogDescription>
        </DialogHeader>

        <DialogBody>
          <div className="space-y-1.5">
            <Label htmlFor="cron-name">{t('cron.name')}</Label>
            <Input
              id="cron-name"
              autoFocus
              value={draft.name}
              onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
              placeholder={t('cron.namePlaceholder')}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="cron-schedule">{t('cron.schedule')}</Label>
            <Input
              id="cron-schedule"
              value={draft.schedule}
              onChange={(e) => setDraft((d) => ({ ...d, schedule: e.target.value }))}
              className="font-mono"
              aria-invalid={preview?.valid === false}
            />
            {preview ? (
              preview.valid ? (
                <p className="text-[11px] leading-relaxed text-muted-foreground">
                  {t('cron.nextRuns')}:{' '}
                  {(preview.upcoming ?? [])
                    .slice(0, 3)
                    .map((iso) =>
                      new Date(iso).toLocaleString(locale, {
                        day: 'numeric',
                        month: 'short',
                        hour: '2-digit',
                        minute: '2-digit',
                      }),
                    )
                    .join(' · ')}
                </p>
              ) : (
                <p className="m-rise text-[11px] text-destructive">{preview.error}</p>
              )
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="cron-prompt">{t('cron.instruction')}</Label>
            <Textarea
              id="cron-prompt"
              value={draft.prompt}
              onChange={(e) => setDraft((d) => ({ ...d, prompt: e.target.value }))}
              placeholder={t('cron.instructionPlaceholder')}
              className="h-24"
            />
          </div>

          {error ? (
            <p className="m-rise border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-4 py-3 text-sm text-destructive">
              {error}
            </p>
          ) : null}
        </DialogBody>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          <Button size="sm" onClick={create} loading={saving} disabled={invalid} className="gap-1.5">
            <Plus className="size-4" />
            {t('cron.create')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
