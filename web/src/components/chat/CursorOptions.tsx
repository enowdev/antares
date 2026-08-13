import { useEffect, useRef, useState } from 'react'
import { CaretDown, Cloud, Warning } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import type { CursorMode, CursorOptionsValue, CursorRunBaseline } from '@/lib/composerTargets'
import { startsNewCursorAgent } from '@/lib/composerTargets'
import {
  cursorFilterCommit,
  cursorFilterFromVariant,
  cursorFilterMatches,
  cursorOtherDimensions,
  cursorReasoningDimension,
  cursorVariantSummary,
  withCursorFilter,
  type CursorDimension,
  type CursorFilterEntry,
} from '@/lib/cursorModels'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Input, Label, Switch } from '@/components/ui/primitives'

interface RepositoryPreflight {
  repository: boolean
  url?: string
  starting_ref?: string
  dirty: boolean
  local_only_commits: number
  remote_ref_known: boolean
  warning?: string
}

/**
 * The Cursor half of the composer: the exact variant to run, the conversation
 * mode, and the repository the cloud VM will clone. Every control filters the
 * catalogue's own variants, so a selection is always one Cursor returned.
 */
export function CursorOptions({
  value,
  onChange,
  projectDir,
  lastStarted,
  disabled,
}: {
  value: CursorOptionsValue
  onChange: (value: CursorOptionsValue) => void
  projectDir?: string
  /** The run a follow-up would continue, if this session has one. */
  lastStarted: CursorRunBaseline | null
  disabled?: boolean
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [preflight, setPreflight] = useState<RepositoryPreflight>()
  // The controls narrow the catalogue rather than editing a selection: what
  // runs only changes once the filter leaves exactly one upstream variant.
  const [filter, setFilter] = useState<CursorFilterEntry[]>(() =>
    cursorFilterFromVariant(value.model, value.variant),
  )
  const ref = useRef<HTMLDivElement>(null)

  // A newly committed variant, a different model, and opening or closing the
  // popover all restart the filter from what is actually selected. Staging an
  // ambiguous filter changes none of those, so work in progress survives until
  // it either commits or is abandoned.
  useEffect(() => {
    setFilter(cursorFilterFromVariant(value.model, value.variant))
  }, [open, value.model, value.variant])

  useEffect(() => {
    if (!open) return
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [open])

  // The repository preflight is local-only and cheap, but it shells out to git;
  // run it when the popover opens rather than on every keystroke elsewhere.
  useEffect(() => {
    if (!open || !projectDir) {
      if (!projectDir) setPreflight(undefined)
      return
    }
    let cancelled = false
    get<RepositoryPreflight>(
      `/project/cursor-repository?dir=${encodeURIComponent(projectDir)}`,
    )
      .then((info) => {
        if (!cancelled) setPreflight(info)
      })
      .catch(() => {
        if (!cancelled) setPreflight(undefined)
      })
    return () => {
      cancelled = true
    }
  }, [open, projectDir])

  const reasoning = cursorReasoningDimension(value.model)
  const others = cursorOtherDimensions(value.model)
  const summary = cursorVariantSummary(value.model, value.variant)
  const newAgent = startsNewCursorAgent(lastStarted, value)

  const selected = filterSelectionOf(filter)
  const remaining = cursorFilterMatches(value.model, filter)

  const pickDimension = (dimension: CursorDimension, option: string) => {
    const next = withCursorFilter(value.model, filter, dimension.id, option)
    setFilter(next)
    // Only a filter that leaves one upstream variant changes what will run;
    // while several remain, the current selection stands untouched.
    const variant = cursorFilterCommit(value.model, next)
    if (variant && variant !== value.variant) onChange({ ...value, variant })
  }

  const discoveredRepo = preflight?.repository ? (preflight.url ?? '') : ''
  const discoveredRef = preflight?.repository ? (preflight.starting_ref ?? '') : ''
  const warnings: string[] = []
  if (preflight?.repository && !preflight.remote_ref_known) {
    warnings.push(t('cursor.warnRemoteUnknown'))
  }
  if (preflight?.dirty) warnings.push(t('cursor.warnDirty'))
  if ((preflight?.local_only_commits ?? 0) > 0) {
    warnings.push(t('cursor.warnLocalOnly', { n: preflight?.local_only_commits ?? 0 }))
  }
  if (preflight?.repository && !preflight.url) {
    warnings.push(t('cursor.warnUnsupportedOrigin'))
  }

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="dialog"
        aria-expanded={open}
        title={t('cursor.options')}
        className="flex h-8 items-center gap-1.5 rounded-[var(--radius-md)] border border-border bg-card px-2.5 text-xs transition-colors hover:border-primary/40"
      >
        <Cloud className="size-3.5 shrink-0 text-primary" />
        <span className="hidden max-w-32 truncate sm:inline">
          {summary || t('cursor.options')}
        </span>
        <CaretDown className="size-3 shrink-0 text-muted-foreground" />
      </button>

      {open ? (
        <div
          role="dialog"
          aria-label={t('cursor.options')}
          className="absolute bottom-full left-0 z-30 mb-2 max-h-[26rem] w-80 space-y-3 overflow-y-auto rounded-[var(--radius-lg)] border border-border bg-card p-3 shadow-lg"
        >
          <div>
            <p className="text-xs font-medium">{value.model.name}</p>
            <p className="truncate font-mono text-[10px] text-muted-foreground">
              {value.model.id}
            </p>
          </div>

          {[...(reasoning ? [reasoning] : []), ...others].map((dimension) => (
            <DimensionRow
              key={dimension.id}
              dimension={dimension}
              selected={selected[dimension.id]}
              onPick={(option) => pickDimension(dimension, option)}
              disabled={disabled}
            />
          ))}
          {remaining.length > 1 ? (
            <p
              role="status"
              className="rounded-[var(--radius-sm)] border border-border bg-muted/50 px-2.5 py-2 text-[10.5px] leading-snug text-muted-foreground"
            >
              {t('cursor.variantPending', { n: remaining.length })}
            </p>
          ) : remaining.length === 0 ? (
            <p
              role="alert"
              className="rounded-[var(--radius-sm)] border border-[var(--warning)]/40 bg-[color-mix(in_oklch,var(--warning)_8%,transparent)] px-2.5 py-2 text-[10.5px] leading-snug text-muted-foreground"
            >
              {t('cursor.variantUnavailable')}
            </p>
          ) : null}

          <div role="group" aria-label={t('cursor.mode')} className="space-y-1.5">
            <Label className="text-[11px]">{t('cursor.mode')}</Label>
            <div className="flex flex-wrap gap-1">
              {(['agent', 'plan'] as CursorMode[]).map((mode) => (
                <OptionChip
                  key={mode}
                  active={value.mode === mode}
                  disabled={disabled}
                  onClick={() => onChange({ ...value, mode })}
                  label={mode === 'agent' ? t('cursor.modeAgent') : t('cursor.modePlan')}
                />
              ))}
            </div>
            <p className="text-[10px] leading-snug text-muted-foreground">
              {t('cursor.modeHint')}
            </p>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="cursor-repo" className="text-[11px]">
              {t('cursor.repository')}
            </Label>
            <Input
              id="cursor-repo"
              value={value.repositoryUrl ?? discoveredRepo}
              disabled={disabled}
              placeholder={discoveredRepo || t('cursor.repositoryNone')}
              onChange={(e) => onChange({ ...value, repositoryUrl: e.target.value })}
              className="h-8 text-xs"
              autoComplete="off"
              spellCheck={false}
            />
            <Label htmlFor="cursor-ref" className="text-[11px]">
              {t('cursor.startingRef')}
            </Label>
            <Input
              id="cursor-ref"
              value={value.startingRef ?? discoveredRef}
              disabled={disabled}
              placeholder={discoveredRef || t('cursor.startingRefAuto')}
              onChange={(e) => onChange({ ...value, startingRef: e.target.value })}
              className="h-8 text-xs"
              autoComplete="off"
              spellCheck={false}
            />
            {value.repositoryUrl !== null || value.startingRef !== null ? (
              <button
                type="button"
                onClick={() =>
                  onChange({ ...value, repositoryUrl: null, startingRef: null })
                }
                className="text-[10px] font-medium text-primary underline underline-offset-2"
              >
                {t('cursor.repositoryReset')}
              </button>
            ) : (
              <p className="text-[10px] leading-snug text-muted-foreground">
                {projectDir ? t('cursor.repositoryAuto') : t('cursor.repositoryNoProject')}
              </p>
            )}
          </div>

          <label className="flex items-center justify-between gap-3">
            <span className="min-w-0">
              <span className="block text-[11px] font-medium">{t('cursor.autoPR')}</span>
              <span className="block text-[10px] leading-snug text-muted-foreground">
                {t('cursor.autoPRHint')}
              </span>
            </span>
            <Switch
              checked={value.autoCreatePR}
              disabled={disabled}
              onCheckedChange={(checked) =>
                onChange({ ...value, autoCreatePR: checked })
              }
              aria-label={t('cursor.autoPR')}
            />
          </label>

          {warnings.length > 0 ? (
            <div className="space-y-1 rounded-[var(--radius-sm)] border border-[var(--warning)]/40 bg-[color-mix(in_oklch,var(--warning)_8%,transparent)] p-2.5">
              {warnings.map((warning) => (
                <p
                  key={warning}
                  className="flex items-start gap-1.5 text-[10.5px] leading-snug text-muted-foreground"
                >
                  <Warning className="mt-0.5 size-3 shrink-0 text-[var(--warning)]" weight="fill" />
                  <span className="min-w-0">{warning}</span>
                </p>
              ))}
            </div>
          ) : null}

          {newAgent ? (
            <p className="rounded-[var(--radius-sm)] border border-border bg-muted/50 px-2.5 py-2 text-[10.5px] leading-snug text-muted-foreground">
              {t('cursor.newAgentNotice')}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

/** The dimensions a filter currently pins, as an id → value map. */
function filterSelectionOf(filter: CursorFilterEntry[]): Record<string, string> {
  const selection: Record<string, string> = {}
  for (const entry of filter) selection[entry.id] = entry.value
  return selection
}

function DimensionRow({
  dimension,
  selected,
  onPick,
  disabled,
}: {
  dimension: CursorDimension
  selected?: string
  onPick: (value: string) => void
  disabled?: boolean
}) {
  return (
    <div role="group" aria-label={dimension.label} className="space-y-1.5">
      <Label className="text-[11px]">{dimension.label}</Label>
      <div className="flex flex-wrap gap-1">
        {dimension.values.map((option) => (
          <OptionChip
            key={option.value}
            active={selected === option.value}
            disabled={disabled}
            onClick={() => onPick(option.value)}
            label={option.label}
          />
        ))}
      </div>
    </div>
  )
}

function OptionChip({
  active,
  label,
  onClick,
  disabled,
  title,
}: {
  active: boolean
  label: string
  onClick: () => void
  disabled?: boolean
  title?: string
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-pressed={active}
      className={cn(
        'rounded-[var(--radius-sm)] border px-2 py-1 text-[11px] transition-colors disabled:cursor-not-allowed disabled:opacity-50',
        active
          ? 'border-primary bg-primary/10 font-medium text-primary'
          : 'border-border text-muted-foreground hover:border-primary/40 hover:text-foreground',
      )}
    >
      {label}
    </button>
  )
}
