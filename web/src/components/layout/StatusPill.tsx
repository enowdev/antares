import { useEffect } from 'react'
import { CheckCircle, WarningCircle, XCircle } from '@phosphor-icons/react'
import { usePoll } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

export interface StatusResponse {
  ok: boolean
  version: string
  model: string
  provider: string
  provider_ready: boolean
  database: string
  uptime_seconds: number
  active_sessions: number
}

/**
 * Compact backend health indicator shown in the sidebar. `compact` shows the
 * icon alone (collapsed icon rail); the label stays in the tooltip.
 */
export function StatusPill({ className, compact }: { className?: string; compact?: boolean }) {
  const { t } = useI18n()
  const { data, loading, error, reload } = usePoll<StatusResponse>('/status', 10000)

  // Refresh immediately when the model changes — the composer's ModelPicker
  // fires 'antares:model-changed' after a successful /model/set, so the
  // "Connected · <model>" label updates within a frame instead of waiting
  // up to 10 s for the next poll tick.
  useEffect(() => {
    window.addEventListener('antares:model-changed', reload)
    return () => window.removeEventListener('antares:model-changed', reload)
  }, [reload])

  if (loading && !data) {
    return <Skeleton className={cn('h-9 w-full rounded-[var(--radius-sm)]', className)} />
  }

  const offline = !!error || !data
  const degraded = !offline && !data!.provider_ready
  const Icon = offline ? XCircle : degraded ? WarningCircle : CheckCircle
  const tone = offline
    ? 'text-destructive'
    : degraded
      ? 'text-[var(--warning)]'
      : 'text-[var(--success)]'
  const label = offline ? t('status.offline') : degraded ? t('status.notReady') : t('status.connected')

  return (
    <div
      className={cn(
        'flex items-center gap-2 rounded-[var(--radius-sm)] border border-border px-2.5 py-1.5',
        compact && 'h-9 justify-center px-0',
        className,
      )}
      title={
        offline
          ? t('status.offlineHint')
          : compact
            ? `${label} · ${data?.provider} · ${data?.model}`
            : `${data?.provider} · ${data?.model}`
      }
      aria-label={compact ? label : undefined}
    >
      <Icon className={cn('size-4 shrink-0', tone)} weight="fill" />
      <div className={cn('min-w-0 flex-1', compact && 'sr-only')}>
        <p className="truncate text-[11px] font-medium leading-tight">{label}</p>
        {data ? (
          <p className="truncate text-[10px] leading-tight text-muted-foreground">
            {data.model || t('status.noModel')}
          </p>
        ) : null}
      </div>
    </div>
  )
}
