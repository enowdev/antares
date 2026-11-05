import { useState } from 'react'
import { ArrowCounterClockwise, CheckCircle, Warning, WarningCircle } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { backupName, type MigrateItem, type MigrateReport } from '@/lib/migrate'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'

/**
 * Outcome of an apply: counts, failures with their error, the backup and an
 * Undo. `actions` renders the caller's next step (the wizard's Continue).
 */
export function MigrateResult({
  report,
  items,
  sourceName,
  undone,
  onUndone,
  actions,
}: {
  report: MigrateReport
  items: MigrateItem[]
  sourceName: string
  undone: boolean
  onUndone: () => void
  actions?: React.ReactNode
}) {
  const { t } = useI18n()
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const titleOf = (id: string) => items.find((i) => i.id === id)?.title ?? id
  const failed = report.failed ?? []
  const ok = failed.length === 0

  const undo = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post('/migrate/undo', { backup: backupName(report.backup) })
      setConfirm(false)
      onUndone()
    } catch (e) {
      setError((e as Error).message)
      setConfirm(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      <section data-reveal className="space-y-4 rounded-[var(--radius-lg)] border border-border bg-card p-4 sm:p-5">
        <div className="flex items-start gap-3">
          {undone ? (
            <ArrowCounterClockwise className="mt-0.5 size-5 shrink-0 text-muted-foreground" />
          ) : ok ? (
            <CheckCircle className="mt-0.5 size-5 shrink-0 text-[var(--success)]" weight="fill" />
          ) : (
            <WarningCircle className="mt-0.5 size-5 shrink-0 text-[var(--warning)]" weight="fill" />
          )}
          <div className="min-w-0 space-y-1">
            <p className="eyebrow">{t('migrate.resultEyebrow')}</p>
            <h3 className="text-[17px] font-medium tracking-[-0.3px]">
              {undone ? t('migrate.undoneTitle') : ok ? t('migrate.resultTitle', { name: sourceName }) : t('migrate.resultPartial')}
            </h3>
            {undone ? <p className="text-sm text-muted-foreground">{t('migrate.undone')}</p> : null}
            {!undone && report.needs_restart ? (
              <p className="text-sm text-muted-foreground">{t('migrate.restarted')}</p>
            ) : null}
          </div>
        </div>

        <dl className="grid grid-cols-3 gap-2">
          <Stat label={t('migrate.applied')} value={report.applied?.length ?? 0} tone="success" />
          <Stat label={t('migrate.skipped')} value={report.skipped?.length ?? 0} />
          <Stat label={t('migrate.failed')} value={failed.length} tone={failed.length ? 'destructive' : undefined} />
        </dl>

        {failed.length > 0 ? (
          <ul className="space-y-1.5 rounded-[var(--radius-md)] border border-[color-mix(in_oklch,var(--destructive)_40%,var(--border))] px-3.5 py-3">
            {failed.map((f) => (
              <li key={f.id} className="text-xs">
                <span className="font-medium text-foreground">{titleOf(f.id)}</span>
                <span className="text-destructive"> — {f.error}</span>
              </li>
            ))}
          </ul>
        ) : null}

        {(report.skipped?.length ?? 0) > 0 ? (
          <details className="group text-xs text-muted-foreground">
            <summary className="cursor-pointer select-none font-mono text-[11px] lowercase hover:text-foreground">
              {t('migrate.skippedList', { n: report.skipped.length })}
            </summary>
            <p className="mt-1.5 leading-relaxed">{report.skipped.map(titleOf).join(' · ')}</p>
          </details>
        ) : null}

        {report.backup ? (
          <div className="space-y-2 border-t border-border pt-4">
            <p className="eyebrow">{t('migrate.backup')}</p>
            <p className="break-all font-mono text-[11px] text-foreground">{report.backup}</p>
            <p className="text-xs leading-relaxed text-muted-foreground">{t('migrate.backupHint')}</p>
            {error ? (
              <p role="alert" className="flex items-start gap-2 text-xs text-destructive">
                <Warning className="mt-0.5 size-3.5 shrink-0" weight="fill" />
                {error}
              </p>
            ) : null}
          </div>
        ) : null}

        <div className="flex flex-wrap items-center gap-2">
          {report.backup && !undone ? (
            <Button variant="outline" size="sm" onClick={() => setConfirm(true)} className="gap-1.5">
              <ArrowCounterClockwise />
              {t('migrate.undo')}
            </Button>
          ) : null}
          {actions}
        </div>
      </section>

      <ConfirmDialog
        open={confirm}
        onOpenChange={(open) => {
          if (!open && !busy) setConfirm(false)
        }}
        title={t('migrate.undoTitle')}
        description={t('migrate.undoDesc')}
        confirmLabel={t('migrate.undo')}
        loading={busy}
        onConfirm={() => void undo()}
      />
    </div>
  )
}

function Stat({ label, value, tone }: { label: string; value: number; tone?: 'success' | 'destructive' }) {
  const color =
    tone === 'success' && value > 0
      ? 'text-[var(--success)]'
      : tone === 'destructive'
        ? 'text-destructive'
        : 'text-foreground'
  return (
    <div className="rounded-[var(--radius-md)] border border-border px-3 py-2.5">
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className={`font-mono text-lg tabular-nums ${color}`}>{value}</dd>
    </div>
  )
}
