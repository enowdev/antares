import { useEffect, useMemo, useRef, useState } from 'react'
import { CaretDown, Check, MagnifyingGlass, X } from '@phosphor-icons/react'
import { Badge, Input } from '@/components/ui/primitives'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'

export interface Option {
  value: string
  label: string
  hint?: string
}

// Close the open panel when a mousedown lands outside `ref`, or Escape is
// pressed. Shared by both pickers so behaviour stays identical.
function useDismiss(open: boolean, close: () => void, ref: React.RefObject<HTMLElement | null>) {
  useEffect(() => {
    if (!open) return
    const onMouse = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) close()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close()
    }
    document.addEventListener('mousedown', onMouse)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onMouse)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, close, ref])
}

function filterOptions(options: Option[], query: string): Option[] {
  const q = query.trim().toLowerCase()
  if (!q) return options
  return options.filter((o) => o.label.toLowerCase().includes(q))
}

/**
 * Single-select dropdown with an inline search box, mirroring the ModelPicker
 * pattern. Selecting an option calls onChange and closes. When `emptyLabel` is
 * given, a row for the empty value ('') sits at the top so the user can clear
 * back to the default.
 */
export function SearchSelect({
  value,
  onChange,
  options,
  placeholder,
  searchPlaceholder,
  emptyLabel,
  disabled,
}: {
  value: string
  onChange: (v: string) => void
  options: Option[]
  placeholder?: string
  searchPlaceholder?: string
  emptyLabel?: string
  disabled?: boolean
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const ref = useRef<HTMLDivElement>(null)
  useDismiss(open, () => setOpen(false), ref)

  const selected = options.find((o) => o.value === value)
  const triggerLabel = value === '' ? emptyLabel ?? placeholder : selected?.label ?? placeholder

  const shown = useMemo(() => filterOptions(options, query), [options, query])

  const pick = (v: string) => {
    onChange(v)
    setOpen(false)
    setQuery('')
  }

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          'flex h-9 w-full items-center gap-2 rounded-full border border-input bg-transparent px-4 text-sm transition-[border-color,background-color] duration-200 hover:border-line',
          'focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-50',
        )}
      >
        <span className={cn('min-w-0 flex-1 truncate text-left', !triggerLabel && 'text-muted-foreground')}>
          {triggerLabel || placeholder}
        </span>
        <CaretDown className="size-3.5 shrink-0 text-muted-foreground" />
      </button>

      {open ? (
        <div className="absolute left-0 top-full z-30 mt-1 m-open flex max-h-72 w-full flex-col rounded-[var(--radius-lg)] border border-border bg-popover p-1 shadow-[0_10px_28px_-14px_#00000080]">
          <div className="relative p-1">
            <MagnifyingGlass className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={searchPlaceholder ?? t('common.search')}
              className="h-8 pl-8 text-xs"
            />
          </div>
          <div className="max-h-56 min-h-0 flex-1 overflow-auto">
            {emptyLabel !== undefined ? (
              <button
                type="button"
                onClick={() => pick('')}
                className={cn(
                  'flex w-full items-center gap-2 rounded-[var(--radius-sm)] px-2.5 py-1.5 text-left text-xs transition-colors hover:bg-raised',
                  value === '' && 'bg-raised',
                )}
              >
                {value === '' ? (
                  <Check className="size-3.5 shrink-0 text-foreground" />
                ) : (
                  <span className="w-3.5 shrink-0" />
                )}
                <span className="min-w-0 flex-1 truncate text-muted-foreground">{emptyLabel}</span>
              </button>
            ) : null}
            {shown.length === 0 ? (
              <p className="px-2.5 py-4 text-center text-xs text-muted-foreground">
                {t('common.search')}…
              </p>
            ) : (
              shown.map((o) => {
                const isActive = o.value === value
                return (
                  <button
                    key={o.value}
                    type="button"
                    onClick={() => pick(o.value)}
                    className={cn(
                      'flex w-full items-center gap-2 rounded-[var(--radius-sm)] px-2.5 py-1.5 text-left transition-colors hover:bg-raised',
                      isActive && 'bg-raised',
                    )}
                  >
                    {isActive ? (
                      <Check className="size-3.5 shrink-0 text-foreground" />
                    ) : (
                      <span className="w-3.5 shrink-0" />
                    )}
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium">{o.label}</span>
                      {o.hint ? (
                        <span className="block truncate font-mono text-[10px] text-dim">{o.hint}</span>
                      ) : null}
                    </span>
                  </button>
                )
              })
            )}
          </div>
        </div>
      ) : null}
    </div>
  )
}

/**
 * Multi-select variant: the trigger shows chosen options as small Badges (or a
 * count when there are many), and clicking a row toggles membership without
 * closing the panel.
 */
export function SearchMultiSelect({
  values,
  onChange,
  options,
  placeholder,
  searchPlaceholder,
  disabled,
}: {
  values: string[]
  onChange: (v: string[]) => void
  options: Option[]
  placeholder?: string
  searchPlaceholder?: string
  disabled?: boolean
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const ref = useRef<HTMLDivElement>(null)
  useDismiss(open, () => setOpen(false), ref)

  const shown = useMemo(() => filterOptions(options, query), [options, query])
  const selectedSet = useMemo(() => new Set(values), [values])

  // Resolve selected values to their labels for the trigger. Values without a
  // matching option (e.g. deleted roles) still render, using the raw value.
  const selectedOptions = values.map((v) => options.find((o) => o.value === v) ?? { value: v, label: v })

  const toggle = (v: string) => {
    if (selectedSet.has(v)) onChange(values.filter((x) => x !== v))
    else onChange([...values, v])
  }

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          'flex min-h-9 w-full items-center gap-2 rounded-[1.125rem] border border-input bg-transparent px-4 py-1 text-sm transition-[border-color,background-color] duration-200 hover:border-line',
          'focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-50',
        )}
      >
        <span className="flex min-w-0 flex-1 flex-wrap gap-1">
          {selectedOptions.length === 0 ? (
            <span className="text-muted-foreground">{placeholder}</span>
          ) : selectedOptions.length > 4 ? (
            <span className="text-xs">{selectedOptions.length} selected</span>
          ) : (
            selectedOptions.map((o) => (
              <Badge key={o.value} variant="secondary" className="max-w-full">
                <span className="truncate">{o.label}</span>
              </Badge>
            ))
          )}
        </span>
        <CaretDown className="size-3.5 shrink-0 text-muted-foreground" />
      </button>

      {open ? (
        <div className="absolute left-0 top-full z-30 mt-1 m-open flex max-h-72 w-full flex-col rounded-[var(--radius-lg)] border border-border bg-popover p-1 shadow-[0_10px_28px_-14px_#00000080]">
          <div className="relative p-1">
            <MagnifyingGlass className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={searchPlaceholder ?? t('common.search')}
              className="h-8 pl-8 text-xs"
            />
          </div>
          {values.length > 0 ? (
            <button
              type="button"
              onClick={() => onChange([])}
              className="mx-1 mb-1 inline-flex items-center gap-1 self-start rounded-full px-2 py-0.5 font-mono text-[10px] text-muted-foreground transition-colors hover:bg-raised hover:text-foreground"
            >
              <X className="size-3" />
              {t('common.clear')}
            </button>
          ) : null}
          <div className="max-h-56 min-h-0 flex-1 overflow-auto">
            {shown.length === 0 ? (
              <p className="px-2.5 py-4 text-center text-xs text-muted-foreground">
                {t('common.search')}…
              </p>
            ) : (
              shown.map((o) => {
                const isActive = selectedSet.has(o.value)
                return (
                  <button
                    key={o.value}
                    type="button"
                    onClick={() => toggle(o.value)}
                    className={cn(
                      'flex w-full items-center gap-2 rounded-[var(--radius-sm)] px-2.5 py-1.5 text-left transition-colors hover:bg-raised',
                      isActive && 'bg-raised',
                    )}
                  >
                    {isActive ? (
                      <Check className="size-3.5 shrink-0 text-foreground" weight="bold" />
                    ) : (
                      <span className="size-3.5 shrink-0 rounded-[0.25rem] border border-border" />
                    )}
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium">{o.label}</span>
                      {o.hint ? (
                        <span className="block truncate font-mono text-[10px] text-dim">{o.hint}</span>
                      ) : null}
                    </span>
                  </button>
                )
              })
            )}
          </div>
        </div>
      ) : null}
    </div>
  )
}
