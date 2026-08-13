import { useState } from 'react'
import { Check, ShieldWarning, Warning, X } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { parseCursorApproval, type ApprovalView, type CursorApprovalDetails } from '@/lib/chatEvents'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'

export type { ApprovalView } from '@/lib/chatEvents'

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

  // Cursor operations are paid and change remote state, so they get the full
  // projection the server published rather than a JSON blob.
  const cursor = parseCursorApproval(approval)
  const pretty = cursor ? '' : formatArguments(approval.arguments)

  return (
    <div className="fade-up rounded-[var(--radius-md)] border border-[var(--warning)]/50 bg-[color-mix(in_oklch,var(--warning)_8%,transparent)] p-3.5">
      <div className="flex items-start gap-2.5">
        <ShieldWarning className="mt-0.5 size-5 shrink-0 text-[var(--warning)]" weight="fill" />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium">{approval.message || t('approval.title')}</p>
          {cursor ? <CursorApprovalDetail details={cursor} /> : null}
          {pretty ? (
            <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-[var(--radius-sm)] bg-muted/60 p-2.5 font-mono text-[11px] leading-relaxed">
              {pretty}
            </pre>
          ) : null}

          {approval.decided ? (
            <p className="mt-2 text-xs text-muted-foreground">
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
              <span className="self-center text-[11px] text-muted-foreground">
                {t('approval.hint')}
              </span>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/** What an approved Cursor operation will do, exactly as the server retained it. */
function CursorApprovalDetail({ details }: { details: CursorApprovalDetails }) {
  const { t } = useI18n()
  const operation =
    details.operation === 'cancel'
      ? t('cursorApproval.cancel')
      : details.newAgent
        ? t('cursorApproval.start')
        : t('cursorApproval.followUp')

  const rows: Array<{ label: string; value: string }> = [
    { label: t('cursorApproval.operation'), value: operation },
  ]
  if (details.model) rows.push({ label: t('cursorApproval.model'), value: details.model })
  if (details.params.length > 0) {
    rows.push({
      label: t('cursorApproval.params'),
      value: details.params.map((param) => `${param.id}=${param.value}`).join(' · '),
    })
  }
  if (details.operation === 'cancel') {
    if (details.agentId) {
      rows.push({ label: t('cursorApproval.agent'), value: details.agentId })
    }
    if (details.runId) {
      rows.push({ label: t('cursorApproval.run'), value: details.runId })
    }
  } else {
    rows.push({
      label: t('cursorApproval.repository'),
      value: details.repositoryUrl || t('cursorApproval.noRepository'),
    })
    if (details.startingRef) {
      rows.push({ label: t('cursorApproval.startingRef'), value: details.startingRef })
    }
    if (details.mode) {
      rows.push({
        label: t('cursorApproval.mode'),
        value: details.mode === 'plan' ? t('cursor.modePlan') : t('cursor.modeAgent'),
      })
    }
    rows.push({
      label: t('cursorApproval.autoPR'),
      value: details.autoCreatePR ? t('common.yes') : t('common.no'),
    })
    if (details.imageCount > 0) {
      rows.push({
        label: t('cursorApproval.images'),
        value: String(details.imageCount),
      })
    }
  }

  return (
    <div className="mt-2 space-y-2">
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px] leading-relaxed">
        {rows.map((row) => (
          <div key={row.label} className="col-span-2 grid grid-cols-subgrid">
            <dt className="text-muted-foreground">{row.label}</dt>
            <dd className="min-w-0 break-words font-mono">{row.value}</dd>
          </div>
        ))}
      </dl>
      {details.promptPreview ? (
        <p className="max-h-24 overflow-auto whitespace-pre-wrap break-words rounded-[var(--radius-sm)] bg-muted/60 p-2.5 text-[11px] leading-relaxed">
          {details.promptPreview}
        </p>
      ) : null}
      {details.warnings.length > 0 ? (
        <ul className="space-y-1">
          {details.warnings.map((warning) => (
            <li key={warning} className="flex items-start gap-1.5 text-[11px] leading-snug">
              <Warning className="mt-0.5 size-3 shrink-0 text-[var(--warning)]" weight="fill" />
              <span className="min-w-0">{warning}</span>
            </li>
          ))}
        </ul>
      ) : null}
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
