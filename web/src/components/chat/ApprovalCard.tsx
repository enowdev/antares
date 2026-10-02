import { useState } from 'react'
import { Check, ShieldWarning, X } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'

export interface ApprovalView {
  id: string
  tool: string
  arguments: string
  message: string
  /** Set once answered, so the card shows the outcome instead of buttons. */
  decided?: 'allowed' | 'refused' | 'expired'
}

/**
 * A tool is waiting on a decision. The run is blocked until this is answered
 * or its deadline passes, so the card has to make both the action and its
 * consequence obvious rather than being a subtle inline hint.
 */
export function ApprovalCard({
  approval,
  onDecided,
}: {
  approval: ApprovalView
  onDecided: (id: string, decision: 'allowed' | 'refused' | 'expired') => void
}) {
  const { t } = useI18n()
  const [busy, setBusy] = useState<'allow' | 'deny' | null>(null)

  const decide = async (allow: boolean) => {
    setBusy(allow ? 'allow' : 'deny')
    try {
      await post(`/approvals/${approval.id}`, { allow })
      onDecided(approval.id, allow ? 'allowed' : 'refused')
    } catch {
      // A request that is no longer waiting has already timed out; saying so
      // is more useful than an error banner.
      onDecided(approval.id, 'expired')
    } finally {
      setBusy(null)
    }
  }

  const pretty = formatArguments(approval.arguments)

  return (
    <div className="fade-up tp-panel rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--warning)_45%,var(--border))] bg-card p-4">
      <div className="flex items-start gap-3">
        <ShieldWarning className="mt-0.5 size-4 shrink-0 text-[var(--warning)]" weight="fill" />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium">{approval.message || t('approval.title')}</p>
          {pretty ? (
            <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-[var(--radius-md)] border border-border bg-background/40 p-2.5 font-mono text-[11px] leading-relaxed">
              {pretty}
            </pre>
          ) : null}

          {approval.decided ? (
            <p className="m-rise mt-2 text-xs text-muted-foreground">
              {approval.decided === 'allowed'
                ? t('approval.allowed')
                : approval.decided === 'refused'
                  ? t('approval.refused')
                  : t('approval.expired')}
            </p>
          ) : (
            <div className="mt-3 flex flex-wrap gap-2">
              <Button size="sm" loading={busy === 'allow'} onClick={() => decide(true)} className="gap-1.5">
                <Check className="size-4" />
                {t('approval.allow')}
              </Button>
              <Button
                size="sm"
                variant="outline"
                loading={busy === 'deny'}
                onClick={() => decide(false)}
                className="gap-1.5"
              >
                <X className="size-4" />
                {t('approval.refuse')}
              </Button>
              <span className="self-center text-xs text-muted-foreground">
                {t('approval.hint')}
              </span>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/** Show the command or path rather than the raw JSON envelope around it. */
function formatArguments(raw: string): string {
  if (!raw) return ''
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    for (const key of ['command', 'path', 'url', 'query']) {
      const v = parsed[key]
      if (typeof v === 'string' && v) return v
    }
    return JSON.stringify(parsed, null, 2)
  } catch {
    return raw
  }
}
