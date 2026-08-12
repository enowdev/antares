import { useEffect, useRef, useState } from 'react'
import { Brain, CaretDown, Check } from '@phosphor-icons/react'
import { useI18n } from '@/lib/i18n'
import type { ReasoningCapability, ReasoningValue } from '@/lib/models'
import { reasoningOptions } from '@/lib/reasoning'
import { cn } from '@/lib/utils'

export interface ReasoningPickerProps {
  value: string
  capability?: ReasoningCapability
  onChange(value: string): void
  compact?: boolean
}

/**
 * Present the exact reasoning values supported by the selected model. Storage
 * and model changes are owned by the composer; this component only renders the
 * supplied value and capability.
 */
export function ReasoningPicker({
  value,
  capability,
  onChange,
  compact = false,
}: ReasoningPickerProps) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [open])

  const options = reasoningOptions(capability)
  const current = options.find((option) => option.value === value) ?? options[0]
  const label = (option: ReasoningValue) =>
    option.value === '' ? t('reasoning.auto') : option.label

  const pick = (v: string) => {
    onChange(v)
    setOpen(false)
  }

  // Unknown/Auto-only models expose no trustworthy override values. Auto is
  // still the behavior, but there is no useful composer control to show.
  if (!capability) return null

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        title={t('roles.fEffort')}
        className={cn(
          'flex items-center gap-1.5 border border-border bg-card transition-colors hover:border-primary/40 focus-visible:border-ring',
          compact
            ? 'h-8 rounded-[var(--radius-md)] px-2.5 text-xs'
            : 'h-[3.25rem] rounded-[var(--radius-xl)] px-3 text-sm shadow-sm',
        )}
      >
        <Brain className={cn('shrink-0 text-muted-foreground', compact ? 'size-3.5' : 'size-4')} />
        <span className="hidden max-w-24 truncate sm:inline">{label(current)}</span>
        <CaretDown className="size-3 shrink-0 text-muted-foreground" />
      </button>

      {open ? (
        <div className="absolute bottom-full left-0 z-30 mb-2 w-52 overflow-y-auto rounded-[var(--radius-lg)] border border-border bg-card p-1 shadow-lg">
          {options.map((option) => (
            <button
              type="button"
              key={option.value || 'auto'}
              onClick={() => pick(option.value)}
              className={cn(
                'flex w-full items-start gap-2 rounded-[var(--radius-sm)] px-2.5 py-1.5 text-left transition-colors hover:bg-muted',
                value === option.value && 'bg-primary/5',
              )}
            >
              {value === option.value ? (
                <Check className="mt-0.5 size-3.5 shrink-0 text-primary" />
              ) : (
                <span className="w-3.5 shrink-0" />
              )}
              <span className="min-w-0">
                <span className="block text-xs font-medium">{label(option)}</span>
                {option.value === '' ? (
                  <span className="block text-[11px] leading-snug text-muted-foreground">
                    {t('reasoning.autoHint')}
                  </span>
                ) : null}
              </span>
            </button>
          ))}
          {capability.mandatory || capability.values.length === 0 ? (
            <p className="mx-1 mt-1 border-t border-border px-2 py-1.5 text-[10px] leading-snug text-muted-foreground">
              {capability.mandatory
                ? t('reasoning.mandatory')
                : t('reasoning.providerControlled')}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
