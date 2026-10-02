import { useMemo, useState } from 'react'
import { ArrowLeft, Info, Key, Prohibit, Warning } from '@phosphor-icons/react'
import { useI18n, type MessageKey } from '@/lib/i18n'
import {
  allowedResolutions,
  defaultKeepResolution,
  groupPlan,
  missingInputs,
  selectable,
  type CategoryGroup,
  type ItemState,
  type MigrateItem,
  type MigratePlan,
  type Resolution,
  type Selection,
} from '@/lib/migrate'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge, Input } from '@/components/ui/primitives'

/** Long categories (memory, knowledge) start folded to this many rows. */
const FOLD_AT = 8
const FOLDED_ROWS = 5

export function PlanView({
  plan,
  selection,
  onChange,
  onBack,
  onApply,
  applying,
  notice,
  error,
}: {
  plan: MigratePlan
  selection: Selection
  onChange: (next: Selection) => void
  onBack: () => void
  onApply: () => void
  applying: boolean
  /** Neutral notice above the plan (e.g. the plan expired and was re-read). */
  notice?: string
  error?: string
}) {
  const { t } = useI18n()
  const groups = useMemo(() => groupPlan(plan.items, selection), [plan.items, selection])
  const total = plan.items.filter(selectable).length
  const chosen = plan.items.filter((i) => selection[i.id]?.selected).length
  const missing = missingInputs(plan.items, selection)
  const det = plan.detection
  const warnings = plan.warnings ?? []

  const update = (id: string, patch: Partial<ItemState>) =>
    onChange({ ...selection, [id]: { ...selection[id], ...patch } })

  const setGroup = (group: CategoryGroup, on: boolean) => {
    const next = { ...selection }
    for (const item of group.items) {
      if (!selectable(item)) continue
      const cur = next[item.id]
      next[item.id] = withSelected(item, cur, on)
    }
    onChange(next)
  }

  return (
    <div className="space-y-4">
      <header data-reveal className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div className="min-w-0 space-y-1">
          <p className="eyebrow">{t('migrate.planEyebrow')}</p>
          <h3 className="text-[17px] font-medium tracking-[-0.3px]">
            {t('migrate.planTitle', { name: det.name })}
            {det.profile ? <span className="text-muted-foreground"> · {det.profile}</span> : null}
          </h3>
          <p className="truncate font-mono text-[11px] text-dim">{det.root}</p>
        </div>
        <Button variant="ghost" size="sm" onClick={onBack} className="-ml-2 gap-1.5 self-start sm:ml-0 sm:self-auto">
          <ArrowLeft />
          {t('migrate.changeSource')}
        </Button>
      </header>

      {notice ? (
        <p
          role="status"
          className="m-rise flex items-start gap-2 rounded-[var(--radius-lg)] border border-border bg-card px-4 py-3 text-sm text-muted-foreground"
        >
          <Info className="mt-0.5 size-4 shrink-0" />
          <span className="min-w-0">{notice}</span>
        </p>
      ) : null}

      {warnings.length > 0 || det.running ? (
        <div
          role="alert"
          data-reveal
          className="space-y-1.5 rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--warning)_45%,var(--border))] bg-[color-mix(in_oklch,var(--warning)_8%,var(--card))] px-4 py-3 text-sm text-[var(--warning)]"
        >
          {(warnings.length > 0 ? warnings : [t('migrate.runningHint')]).map((w) => (
            <p key={w} className="flex items-start gap-2">
              <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
              <span className="min-w-0 leading-relaxed">{w}</span>
            </p>
          ))}
        </div>
      ) : null}

      {groups.map((group) => (
        <CategorySection
          key={group.category}
          group={group}
          selection={selection}
          onItem={update}
          onToggle={(item, on) => onChange({ ...selection, [item.id]: withSelected(item, selection[item.id], on) })}
          onAll={(on) => setGroup(group, on)}
        />
      ))}

      {plan.items.length === 0 ? (
        <p className="rounded-[var(--radius-lg)] border border-dashed border-border px-4 py-5 text-sm text-muted-foreground">
          {t('migrate.emptyPlan')}
        </p>
      ) : null}

      <div className="sticky bottom-3 z-10 space-y-2 rounded-[var(--radius-lg)] border border-border bg-card/95 px-4 py-3 shadow-[0_18px_40px_-24px_#00000080] backdrop-blur">
        {error ? (
          <p role="alert" className="flex items-start gap-2 text-xs text-destructive">
            <Warning className="mt-0.5 size-3.5 shrink-0" weight="fill" />
            <span className="min-w-0 break-words">{error}</span>
          </p>
        ) : null}
        {missing.length > 0 ? (
          <p className="flex items-start gap-2 text-xs text-[var(--warning)]">
            <Key className="mt-0.5 size-3.5 shrink-0" />
            <span>{t('migrate.missingInputs', { n: missing.length })}</span>
          </p>
        ) : null}
        <div className="flex items-center justify-between gap-3">
          <span className="font-mono text-[11px] tabular-nums text-muted-foreground">
            {t('migrate.planSummary', { selected: chosen, total })}
          </span>
          <Button onClick={onApply} loading={applying} disabled={chosen === 0}>
            {t('migrate.apply', { n: chosen })}
          </Button>
        </div>
      </div>
    </div>
  )
}

/** Ticking a conflict still on skip moves it to a resolution that imports it. */
function withSelected(item: MigrateItem, cur: ItemState, on: boolean): ItemState {
  if (on && item.status === 'conflict' && cur.resolution === 'skip') {
    return { ...cur, selected: true, resolution: defaultKeepResolution(item.category) }
  }
  return { ...cur, selected: on }
}

function CategorySection({
  group,
  selection,
  onItem,
  onToggle,
  onAll,
}: {
  group: CategoryGroup
  selection: Selection
  onItem: (id: string, patch: Partial<ItemState>) => void
  onToggle: (item: MigrateItem, on: boolean) => void
  onAll: (on: boolean) => void
}) {
  const { t } = useI18n()
  const long = group.items.length > FOLD_AT
  const [expanded, setExpanded] = useState(false)
  const rows = long && !expanded ? group.items.slice(0, FOLDED_ROWS) : group.items
  const allOn = group.selectable > 0 && group.selected === group.selectable
  const headingId = `migrate-cat-${group.category}`

  return (
    <section
      data-reveal
      aria-labelledby={headingId}
      className="overflow-hidden rounded-[var(--radius-lg)] border border-border bg-card"
    >
      <div className="flex items-center gap-3 px-4 py-3">
        <h4 id={headingId} className="min-w-0 flex-1 truncate text-[13px] font-medium">
          {t(`migrate.cat.${group.category}` as MessageKey)}
        </h4>
        <span className="rounded-full bg-raised px-2.5 py-0.5 font-mono text-[11px] tabular-nums text-muted-foreground">
          {group.selectable > 0 ? `${group.selected}/${group.selectable}` : group.items.length}
        </span>
        {group.selectable > 1 ? (
          <Button variant="ghost" size="sm" className="-mr-2" onClick={() => onAll(!allOn)}>
            {allOn ? t('migrate.selectNone') : t('migrate.selectAll')}
          </Button>
        ) : null}
      </div>
      <ul className="divide-y divide-border border-t border-border">
        {rows.map((item) => (
          <ItemRow
            key={item.id}
            item={item}
            state={selection[item.id]}
            onToggle={(on) => onToggle(item, on)}
            onPatch={(patch) => onItem(item.id, patch)}
          />
        ))}
      </ul>
      {long ? (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="w-full border-t border-border px-4 py-2.5 text-left font-mono text-[11px] lowercase text-muted-foreground transition-colors duration-200 hover:bg-raised hover:text-foreground"
        >
          {expanded ? t('migrate.showLess') : t('migrate.showAll', { n: group.items.length })}
        </button>
      ) : null}
    </section>
  )
}

function ItemRow({
  item,
  state,
  onToggle,
  onPatch,
}: {
  item: MigrateItem
  state?: ItemState
  onToggle: (on: boolean) => void
  onPatch: (patch: Partial<ItemState>) => void
}) {
  const { t } = useI18n()
  const unsupported = !selectable(item) || !state
  const checkboxId = `migrate-item-${item.id}`

  return (
    <li className={cn('flex gap-3 px-4 py-3', unsupported && 'bg-[color-mix(in_oklch,var(--raised)_45%,transparent)]')}>
      <div className="flex h-5 w-4 shrink-0 items-center justify-center">
        {unsupported ? (
          <Prohibit className="size-4 text-dim" aria-hidden />
        ) : (
          <input
            id={checkboxId}
            type="checkbox"
            checked={state.selected}
            onChange={(e) => onToggle(e.target.checked)}
            className="size-4 cursor-pointer accent-[var(--foreground)]"
          />
        )}
      </div>

      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <label
            htmlFor={unsupported ? undefined : checkboxId}
            className={cn(
              'min-w-0 break-words text-[13px] font-medium',
              unsupported ? 'text-muted-foreground' : 'cursor-pointer',
            )}
          >
            {item.title}
          </label>
          {item.secret ? (
            <Badge variant="outline">
              <Key className="size-3" />
              {t('migrate.hasKey')}
            </Badge>
          ) : null}
          {item.status === 'conflict' ? <Badge variant="warning">{t('migrate.status.conflict')}</Badge> : null}
          {item.status === 'needs_input' ? <Badge variant="warning">{t('migrate.status.needs_input')}</Badge> : null}
          {item.status === 'unsupported' ? <Badge variant="secondary">{t('migrate.status.unsupported')}</Badge> : null}
        </div>
        {item.detail ? <p className="break-words text-xs text-muted-foreground">{item.detail}</p> : null}
        {item.reason ? (
          <p className={cn('break-words text-xs leading-relaxed', unsupported ? 'text-dim' : 'text-muted-foreground')}>
            {item.reason}
          </p>
        ) : null}

        {!unsupported && item.status === 'conflict' ? (
          <ResolutionSelect
            item={item}
            value={state.resolution}
            onChange={(resolution) => onPatch({ resolution, selected: resolution !== 'skip' })}
          />
        ) : null}

        {!unsupported && item.status === 'needs_input' ? (
          <div className="pt-1">
            <label htmlFor={`${checkboxId}-input`} className="sr-only">
              {item.input === 'api_key' ? t('migrate.keyLabel') : t('migrate.inputLabel')}
            </label>
            <Input
              id={`${checkboxId}-input`}
              type={item.input === 'api_key' ? 'password' : 'text'}
              value={state.input}
              onChange={(e) => {
                const input = e.target.value
                onPatch({ input, selected: input.trim() ? true : state.selected })
              }}
              placeholder={item.input === 'api_key' ? t('migrate.keyPlaceholder') : t('migrate.inputPlaceholder', { name: item.input ?? '' })}
              autoComplete="off"
              spellCheck={false}
              className="sm:max-w-sm"
            />
          </div>
        ) : null}
      </div>
    </li>
  )
}

function ResolutionSelect({
  item,
  value,
  onChange,
}: {
  item: MigrateItem
  value: Resolution
  onChange: (r: Resolution) => void
}) {
  const { t } = useI18n()
  const id = `migrate-res-${item.id}`
  return (
    <div className="flex flex-wrap items-center gap-2 pt-1">
      <label htmlFor={id} className="text-[11px] text-muted-foreground">
        {t('migrate.resolution')}
      </label>
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value as Resolution)}
        className="h-8 cursor-pointer rounded-full border border-input bg-transparent px-3 text-base transition-[border-color] duration-200 hover:border-line focus-visible:border-ring focus-visible:outline-none sm:text-xs"
      >
        {allowedResolutions(item.category).map((r) => (
          <option key={r} value={r} className="bg-popover text-popover-foreground">
            {t(`migrate.res.${r}` as MessageKey)}
          </option>
        ))}
      </select>
    </div>
  )
}
