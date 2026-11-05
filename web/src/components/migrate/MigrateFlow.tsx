import { useEffect, useState } from 'react'
import { Warning } from '@phosphor-icons/react'
import { ApiError, get, isDashboardPasswordRequired, post } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import {
  buildChoices,
  initialSelection,
  type MigratePlan,
  type MigrateReport,
  type MigrateSource,
  type Selection,
} from '@/lib/migrate'
import { SkeletonList } from '@/components/ui/skeleton'
import { MigrateResult } from './MigrateResult'
import { PlanView } from './PlanView'
import { SourcePicker, targetKey, type PlanTarget } from './SourcePicker'

export interface AppliedMigration {
  report: MigrateReport
  target: PlanTarget
}

/**
 * The whole migrate flow — pick a source, review the plan, apply, see the
 * result — shared by the setup wizard and Settings › Migrate.
 *
 * Pass `sources` when the caller already fetched them (the wizard does, to
 * decide whether to show its migrate step); otherwise they are loaded here.
 */
export function MigrateFlow({
  sources: given,
  onApplied,
  onUndone,
  resultActions,
  compact = false,
}: {
  sources?: MigrateSource[]
  /** Fold the undetected agents list (the wizard). */
  compact?: boolean
  onApplied?: (applied: AppliedMigration) => void
  onUndone?: (applied: AppliedMigration) => void
  /** Extra buttons beside Undo on the result (e.g. the wizard's Continue). */
  resultActions?: (applied: AppliedMigration) => React.ReactNode
}) {
  const { t } = useI18n()
  const [sources, setSources] = useState<MigrateSource[] | undefined>(given)
  const [sourcesError, setSourcesError] = useState<string>()

  const [target, setTarget] = useState<PlanTarget>()
  const [planning, setPlanning] = useState<string>()
  const [plan, setPlan] = useState<MigratePlan>()
  const [selection, setSelection] = useState<Selection>({})
  const [applying, setApplying] = useState(false)
  const [notice, setNotice] = useState<string>()
  const [error, setError] = useState<string>()

  const [applied, setApplied] = useState<AppliedMigration>()
  const [undone, setUndone] = useState(false)

  useEffect(() => {
    if (given) {
      setSources(given)
      return
    }
    let alive = true
    get<{ sources: MigrateSource[] }>('/migrate/sources')
      .then((r) => alive && setSources(r.sources ?? []))
      .catch((e: Error) => alive && setSourcesError(e.message))
    return () => {
      alive = false
    }
  }, [given])

  const describe = (e: unknown) =>
    isDashboardPasswordRequired(e) ? t('migrate.needPassword') : (e as Error).message

  const loadPlan = async (next: PlanTarget, keep?: Selection): Promise<boolean> => {
    setPlanning(targetKey(next.source, next.profile, next.root))
    setError(undefined)
    try {
      const p = await post<MigratePlan>('/migrate/plan', {
        source: next.source,
        root: next.root,
        profile: next.profile,
      })
      setTarget(next)
      setPlan(p)
      setSelection(initialSelection(p.items ?? [], keep))
      return true
    } catch (e) {
      setError(describe(e))
      return false
    } finally {
      setPlanning(undefined)
    }
  }

  const pick = (next: PlanTarget) => {
    setNotice(undefined)
    void loadPlan(next)
  }

  const apply = async () => {
    if (!plan || !target) return
    setApplying(true)
    setError(undefined)
    setNotice(undefined)
    try {
      const report = await post<MigrateReport>('/migrate/apply', {
        plan_id: plan.plan_id,
        items: buildChoices(plan.items, selection),
      })
      const done = { report, target }
      setApplied(done)
      setUndone(false)
      onApplied?.(done)
    } catch (e) {
      if (e instanceof ApiError && e.status === 410) {
        // The cached plan outlived its 10 minutes: read the source again,
        // keep the user's choices by item id, and let them confirm again.
        if (await loadPlan(target, selection)) setNotice(t('migrate.expired'))
      } else {
        setError(describe(e))
      }
    } finally {
      setApplying(false)
    }
  }

  const reset = () => {
    setPlan(undefined)
    setTarget(undefined)
    setSelection({})
    setApplied(undefined)
    setUndone(false)
    setNotice(undefined)
    setError(undefined)
  }

  if (applied && plan) {
    return (
      <MigrateResult
        report={applied.report}
        items={plan.items}
        sourceName={applied.target.name}
        undone={undone}
        onUndone={() => {
          setUndone(true)
          onUndone?.(applied)
        }}
        actions={
          <>
            {resultActions?.(applied)}
            <button
              type="button"
              onClick={reset}
              className="px-2 font-mono text-[11px] lowercase text-muted-foreground underline decoration-line underline-offset-4 transition-colors hover:text-foreground"
            >
              {t('migrate.again')}
            </button>
          </>
        }
      />
    )
  }

  if (plan) {
    return (
      <PlanView
        plan={plan}
        selection={selection}
        onChange={setSelection}
        onBack={reset}
        onApply={() => void apply()}
        applying={applying || !!planning}
        notice={notice}
        error={error}
      />
    )
  }

  return (
    <div className="space-y-4">
      {error ? <ErrorLine message={error} /> : null}
      {sourcesError ? (
        <ErrorLine message={t('migrate.sourcesFailed', { error: sourcesError })} />
      ) : !sources ? (
        <SkeletonList count={3} />
      ) : (
        <SourcePicker sources={sources} busy={planning} onPick={pick} foldMissing={compact} />
      )}
    </div>
  )
}

function ErrorLine({ message }: { message: string }) {
  return (
    <p
      role="alert"
      className="m-rise flex items-start gap-2 rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-4 py-3 text-sm text-destructive"
    >
      <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
      <span className="min-w-0 break-words">{message}</span>
    </p>
  )
}
