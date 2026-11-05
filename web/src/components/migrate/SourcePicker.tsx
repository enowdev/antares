import { useState } from 'react'
import { ArrowRight, CaretDown, FolderOpen, Warning } from '@phosphor-icons/react'
import { useI18n } from '@/lib/i18n'
import type { Detection, MigrateSource } from '@/lib/migrate'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge, Input } from '@/components/ui/primitives'

export interface PlanTarget {
  source: string
  name: string
  root: string
  profile: string
}

/**
 * Detected installs first, as cards; every other registered source below,
 * dimmed, with a way to point at its folder when it lives somewhere unusual.
 */
export function SourcePicker({
  sources,
  busy,
  onPick,
  foldMissing = false,
}: {
  sources: MigrateSource[]
  /** Start the undetected list folded (the wizard, where detections lead). */
  foldMissing?: boolean
  /** Key of the target being planned (source/profile/root), for its spinner. */
  busy?: string
  onPick: (target: PlanTarget) => void
}) {
  const { t } = useI18n()
  const detected = sources.flatMap((s) => (s.detected ?? []).map((d) => ({ source: s, det: d })))
  const missing = sources.filter((s) => !(s.detected ?? []).length)
  const [showMissing, setShowMissing] = useState(!foldMissing || detected.length === 0)

  return (
    <div className="space-y-5">
      {detected.length > 0 ? (
        <section data-reveal className="space-y-2.5">
          <p className="eyebrow px-1">{t('migrate.detected')}</p>
          <div className="grid gap-2 sm:grid-cols-2">
            {detected.map(({ source, det }) => (
              <DetectionCard
                key={`${det.source}/${det.profile ?? ''}/${det.root}`}
                det={det}
                name={det.name || source.name}
                loading={busy === targetKey(det.source, det.profile ?? '', det.root)}
                disabled={!!busy}
                onPick={() =>
                  onPick({ source: det.source, name: det.name || source.name, root: det.root, profile: det.profile ?? '' })
                }
              />
            ))}
          </div>
        </section>
      ) : (
        <p
          data-reveal
          className="rounded-[var(--radius-lg)] border border-dashed border-border px-4 py-4 text-sm leading-relaxed text-muted-foreground"
        >
          {t('migrate.noneDetected')}
        </p>
      )}

      {missing.length > 0 ? (
        <section data-reveal className="space-y-2.5">
          {showMissing ? (
            <p className="eyebrow px-1">{t('migrate.otherAgents')}</p>
          ) : (
            <button
              type="button"
              onClick={() => setShowMissing(true)}
              aria-expanded={false}
              className="inline-flex items-center gap-1.5 px-1 font-mono text-[11px] lowercase text-muted-foreground underline decoration-line underline-offset-4 transition-colors hover:text-foreground"
            >
              <CaretDown className="size-3" />
              {t('migrate.showOthers', { n: missing.length })}
            </button>
          )}
          {showMissing ? (
          <ul className="divide-y divide-border overflow-hidden rounded-[var(--radius-lg)] border border-border bg-card">
            {missing.map((s) => (
              <MissingRow key={s.id} source={s} busy={busy} onPick={onPick} />
            ))}
          </ul>
          ) : null}
        </section>
      ) : null}
    </div>
  )
}

export function targetKey(source: string, profile: string, root: string): string {
  return `${source}/${profile}/${root}`
}

function DetectionCard({
  det,
  name,
  loading,
  disabled,
  onPick,
}: {
  det: Detection
  name: string
  loading: boolean
  disabled: boolean
  onPick: () => void
}) {
  const { t } = useI18n()
  return (
    <button
      type="button"
      onClick={onPick}
      disabled={disabled}
      className={cn(
        'group flex flex-col gap-2 rounded-[var(--radius-lg)] border border-border bg-card p-4 text-left transition-[border-color,background-color] duration-200',
        'hover:border-line hover:bg-raised disabled:cursor-progress',
        loading && 'border-foreground',
      )}
    >
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="text-sm font-medium">{name}</span>
        {det.profile ? <Badge variant="outline">{t('migrate.profile', { name: det.profile })}</Badge> : null}
        {det.version ? <span className="font-mono text-[11px] text-dim">v{det.version}</span> : null}
        {det.running ? (
          <Badge variant="warning">
            <Warning className="size-3" weight="fill" />
            {t('migrate.running')}
          </Badge>
        ) : null}
      </div>
      <p className="truncate font-mono text-[11px] text-dim">{det.root}</p>
      {det.summary ? <p className="text-xs leading-relaxed text-muted-foreground">{det.summary}</p> : null}
      {det.running ? (
        <p className="text-[11px] leading-relaxed text-[var(--warning)]">{t('migrate.runningHint')}</p>
      ) : null}
      <span className="mt-auto inline-flex items-center gap-1.5 pt-1 font-mono text-[11px] lowercase text-foreground">
        {loading ? t('migrate.reading') : t('migrate.review')}
        <ArrowRight className="size-3.5 transition-transform duration-200 group-hover:translate-x-0.5" />
      </span>
    </button>
  )
}

function MissingRow({
  source,
  busy,
  onPick,
}: {
  source: MigrateSource
  busy?: string
  onPick: (target: PlanTarget) => void
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [root, setRoot] = useState('')
  const inputId = `migrate-root-${source.id}`
  const loading = busy === targetKey(source.id, '', root.trim())

  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <div className={cn('min-w-0 flex-1', !open && 'opacity-60')}>
          <span className="text-[13px] font-medium">{source.name}</span>
          <span className="ml-2 text-xs text-muted-foreground">{t('migrate.notFound')}</span>
        </div>
        {!open ? (
          <Button variant="ghost" size="sm" onClick={() => setOpen(true)} className="-mr-2 gap-1.5">
            <FolderOpen />
            {t('migrate.chooseFolder')}
          </Button>
        ) : null}
      </div>
      {open ? (
        <form
          className="m-rise mt-2.5 flex flex-col gap-2 sm:flex-row"
          onSubmit={(e) => {
            e.preventDefault()
            if (root.trim()) onPick({ source: source.id, name: source.name, root: root.trim(), profile: '' })
          }}
        >
          <label htmlFor={inputId} className="sr-only">
            {t('migrate.folderLabel')}
          </label>
          <Input
            id={inputId}
            value={root}
            onChange={(e) => setRoot(e.target.value)}
            placeholder={t('migrate.folderPlaceholder', { id: source.id })}
            autoComplete="off"
            spellCheck={false}
            autoFocus
          />
          <div className="flex gap-2">
            <Button type="submit" size="sm" className="h-9" loading={loading} disabled={!root.trim() || !!busy}>
              {t('migrate.review')}
            </Button>
            <Button type="button" variant="ghost" size="sm" className="h-9" onClick={() => setOpen(false)}>
              {t('common.cancel')}
            </Button>
          </div>
        </form>
      ) : null}
    </li>
  )
}
