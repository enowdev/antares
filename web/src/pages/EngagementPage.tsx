import { useEffect, useState } from 'react'
import { Check, Copy, DownloadSimple, ShieldCheck, Trash, Warning } from '@phosphor-icons/react'
import { del, downloadFile, get } from '@/lib/api'
import { copyText } from '@/lib/clipboard'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  CardHeader,
  CardTitle,
  EmptyState,
  Tabs,
  TabsList,
  TabsTrigger,
} from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'
import { SearchSelect } from '@/components/ui/SearchSelect'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { Markdown } from '@/components/chat/Markdown'

interface Phase {
  name: string
  title: string
  summary: string
  status: 'not_started' | 'in_progress' | 'complete' | 'blocked'
  evidence: number
}
interface Area {
  name: string
  title: string
  covered: boolean
}
interface Chain {
  name: string
  impact: string
}
interface Finding {
  id: string
  title: string
  severity: string
  target?: string
  status?: string
  cwe?: string
}
interface Intel {
  id: string
  type: string
  value: string
  detail?: string
}
interface Engagement {
  phases?: Phase[]
  coverage?: Area[]
  coverage_percent?: number
  chains?: Chain[]
  findings?: Finding[] | null
  intel?: Intel[] | null
  next?: string
}
interface SessionRow {
  id: string
  title: string
  findings: number
  intel: number
}

// A dot per phase: filled once complete, outlined in ink while in
// progress, amber when blocked, a faint outline before it starts.
const PHASE_MARK: Record<Phase['status'], string> = {
  complete: 'bg-foreground',
  in_progress: 'shadow-[inset_0_0_0_1px_var(--foreground)]',
  blocked: 'bg-[var(--warning)]',
  not_started: 'shadow-[inset_0_0_0_1px_var(--line)]',
}
const SEV_VARIANT: Record<string, 'destructive' | 'warning' | 'secondary' | 'outline'> = {
  critical: 'destructive',
  high: 'destructive',
  medium: 'warning',
  low: 'secondary',
  info: 'outline',
}
const SEV_ORDER: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3, info: 4 }

export default function EngagementPage() {
  const { t } = useI18n()
  const { data: sessData, loading, reload } = useApi<{ sessions: SessionRow[] }>('/engagement/sessions')
  const sessions = sessData?.sessions ?? []
  const [sessionID, setSessionID] = useState('')
  const active = sessionID || sessions[0]?.id || ''
  const [tab, setTab] = useState<'overview' | 'raw'>('overview')
  const [toDelete, setToDelete] = useState<SessionRow | null>(null)
  const [deleting, setDeleting] = useState(false)

  const removeEngagement = async () => {
    if (!toDelete) return
    setDeleting(true)
    try {
      await del(`/engagement/${encodeURIComponent(toDelete.id)}`)
      if (sessionID === toDelete.id) setSessionID('')
      reload()
    } finally {
      setDeleting(false)
      setToDelete(null)
    }
  }

  const [eng, setEng] = useState<Engagement | null>(null)
  useEffect(() => {
    if (!active) {
      setEng(null)
      return
    }
    let cancelled = false
    get<Engagement>(`/engagement?session=${encodeURIComponent(active)}`)
      .then((d) => !cancelled && setEng(d))
      .catch(() => !cancelled && setEng(null))
    return () => {
      cancelled = true
    }
  }, [active])

  if (loading) {
    return (
      <PageLayout>
        <SkeletonList count={3} />
      </PageLayout>
    )
  }
  if (sessions.length === 0) {
    return (
      <PageLayout>
        <EmptyState
          icon={<ShieldCheck className="size-8" />}
          title={t('engagement.empty')}
          description={t('engagement.emptyDesc')}
        />
      </PageLayout>
    )
  }

  const activeTitle = sessions.find((s) => s.id === active)?.title || 'Security Assessment'
  const download = () =>
    downloadFile(
      `/engagement/report?session=${encodeURIComponent(active)}&title=${encodeURIComponent(activeTitle)}`,
      `report-${active}.md`,
    ).catch(() => {})

  const activeRow = sessions.find((s) => s.id === active)
  const header = (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <label className="eyebrow">{t('engagement.pickSession')}</label>
        <div className="min-w-0 flex-1 sm:min-w-[18rem] sm:flex-none">
          <SearchSelect
            value={active}
            onChange={setSessionID}
            options={sessions.map((s) => ({
              value: s.id,
              label: s.title || s.id,
              hint: `${s.findings}✚ / ${s.intel}◆`,
            }))}
            placeholder={t('engagement.pickSession')}
            searchPlaceholder={t('engagement.searchSession')}
          />
        </div>
        <Button variant="outline" size="sm" disabled={!active} onClick={download} className="gap-1.5">
          <DownloadSimple className="size-3.5" />
          <span className="hidden sm:inline">{t('engagement.downloadReport')}</span>
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={!activeRow}
          onClick={() => activeRow && setToDelete(activeRow)}
          aria-label={t('engagement.deleteSession')}
          className="gap-1.5 text-muted-foreground hover:text-destructive"
        >
          <Trash className="size-3.5" />
          <span className="hidden sm:inline">{t('common.delete')}</span>
        </Button>
      </div>
      <Tabs value={tab} onValueChange={(v) => setTab(v as 'overview' | 'raw')}>
        <TabsList>
          <TabsTrigger value="overview">{t('engagement.tabOverview')}</TabsTrigger>
          <TabsTrigger value="raw">{t('engagement.tabRaw')}</TabsTrigger>
        </TabsList>
      </Tabs>
    </div>
  )

  return (
    <PageLayout header={header}>
      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={t('engagement.deleteTitle')}
        description={t('engagement.deleteDesc', { title: toDelete?.title || toDelete?.id || '' })}
        confirmLabel={t('common.delete')}
        loading={deleting}
        onConfirm={() => void removeEngagement()}
      />
      {tab === 'raw' ? (
        <RawReport session={active} title={activeTitle} />
      ) : (
        <Overview eng={eng} />
      )}
    </PageLayout>
  )
}

function Overview({ eng }: { eng: Engagement | null }) {
  const { t } = useI18n()
  const findings = [...(eng?.findings ?? [])].sort(
    (a, b) => (SEV_ORDER[a.severity] ?? 9) - (SEV_ORDER[b.severity] ?? 9),
  )
  const intel = eng?.intel ?? []
  const chains = eng?.chains ?? []
  const pct = eng?.coverage_percent ?? 0

  const stats: { label: string; value: string; tone?: string }[] = [
    { label: t('engagement.findings'), value: String(findings.length) },
    { label: t('engagement.intel'), value: String(intel.length) },
    { label: t('engagement.coverage'), value: `${pct}%` },
    {
      label: t('engagement.chains'),
      value: String(chains.length),
      tone: chains.length ? 'text-destructive' : undefined,
    },
  ]

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {stats.map((s) => (
          <div key={s.label} data-reveal className="rounded-[var(--radius-lg)] border border-border px-4 py-3.5">
            <p className="eyebrow">{s.label}</p>
            <p className={cn('mt-2 text-2xl font-medium tabular-nums', s.tone)}>{s.value}</p>
          </div>
        ))}
      </div>

      {/* Methodology */}
      <Card className="p-5">
        <CardHeader className="p-0 pb-4">
          <CardTitle>{t('engagement.methodology')}</CardTitle>
        </CardHeader>
        <ol className="overflow-hidden rounded-[var(--radius-lg)] border border-border">
          {(eng?.phases ?? []).map((p) => (
            <li
              key={p.name}
              data-reveal
              className="relative flex items-start gap-3 border-t border-border px-4 py-3 text-sm first:border-t-0"
            >
              {p.status === 'in_progress' ? (
                <span aria-hidden className="absolute inset-y-2.5 left-0 w-[2px] rounded-full bg-foreground" />
              ) : null}
              <span aria-hidden className={cn('mt-[7px] size-[7px] shrink-0 rounded-full', PHASE_MARK[p.status])} />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{p.title}</span>
                  {p.evidence > 0 ? (
                    <Badge variant="secondary">{t('engagement.recorded', { n: p.evidence })}</Badge>
                  ) : null}
                </div>
                {p.summary ? (
                  <p className="mt-0.5 text-xs text-muted-foreground">{p.summary}</p>
                ) : null}
              </div>
            </li>
          ))}
        </ol>
        {eng?.next ? (
          <p className="mt-3 rounded-[var(--radius-md)] border border-border px-4 py-3 text-xs text-muted-foreground">
            <span className="font-mono text-foreground">→</span> {eng.next}
          </p>
        ) : null}
      </Card>

      {/* Coverage */}
      <Card className="p-5">
        <CardHeader className="p-0 pb-3">
          <CardTitle className="flex items-center justify-between">
            {t('engagement.coverage')}
            <span className="font-mono text-xs font-normal tabular-nums text-muted-foreground">{pct}%</span>
          </CardTitle>
        </CardHeader>
        <div className="mb-4 h-1 overflow-hidden rounded-full bg-raised">
          <div
            className="h-full rounded-full bg-foreground transition-[width] duration-500"
            style={{ width: `${pct}%` }}
          />
        </div>
        <div className="grid grid-cols-2 gap-x-3 gap-y-2 sm:grid-cols-3 lg:grid-cols-4">
          {(eng?.coverage ?? []).map((a) => (
            <div key={a.name} className="flex items-center gap-2 text-xs">
              {a.covered ? (
                <Check className="size-3.5 shrink-0 text-[var(--success)]" weight="bold" />
              ) : (
                <span className="size-3.5 shrink-0 rounded-full border border-line" />
              )}
              <span className={a.covered ? '' : 'text-muted-foreground'}>{a.title}</span>
            </div>
          ))}
        </div>
      </Card>

      {/* Chains */}
      {chains.length > 0 ? (
        <Card className="border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] p-5">
          <CardHeader className="p-0 pb-3">
            <CardTitle className="flex items-center gap-2 text-destructive">
              <Warning className="size-4" weight="fill" />
              {t('engagement.chains')}
            </CardTitle>
          </CardHeader>
          <div className="space-y-1.5">
            {chains.map((c) => (
              <div key={c.name} className="text-sm">
                <span className="font-medium">{c.name}</span>
                <span className="text-muted-foreground"> — {c.impact}</span>
              </div>
            ))}
          </div>
        </Card>
      ) : null}

      {/* Findings */}
      <Card className="p-5">
        <CardHeader className="p-0 pb-3">
          <CardTitle className="flex items-center justify-between">
            {t('engagement.findings')}
            <span className="font-mono text-xs font-normal tabular-nums text-muted-foreground">
              {findings.length}
            </span>
          </CardTitle>
        </CardHeader>
        {findings.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t('engagement.noFindings')}</p>
        ) : (
          <div className="overflow-hidden rounded-[var(--radius-lg)] border border-border">
            {findings.map((f) => (
              <div
                key={f.id}
                data-reveal
                className="flex flex-wrap items-center gap-2 border-t border-border px-4 py-3 text-sm transition-colors duration-200 first:border-t-0 hover:bg-raised"
              >
                <Badge variant={SEV_VARIANT[f.severity] ?? 'outline'}>{f.severity}</Badge>
                <span className="min-w-0 flex-1 font-medium">{f.title}</span>
                {f.cwe ? (
                  <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{f.cwe}</span>
                ) : null}
                {f.target ? (
                  <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
                    {f.target}
                  </span>
                ) : null}
                {f.status && f.status !== 'new' && f.status !== 'confirmed' ? (
                  <Badge variant="outline">{f.status}</Badge>
                ) : null}
              </div>
            ))}
          </div>
        )}
      </Card>

      {/* Intel */}
      {intel.length > 0 ? (
        <Card className="p-5">
          <CardHeader className="p-0 pb-3">
            <CardTitle className="flex items-center justify-between">
              {t('engagement.intel')}
              <span className="font-mono text-xs font-normal tabular-nums text-muted-foreground">
                {intel.length}
              </span>
            </CardTitle>
          </CardHeader>
          <div className="overflow-hidden rounded-[var(--radius-lg)] border border-border">
            {intel.map((it) => (
              <div
                key={it.id}
                className="flex items-center gap-2 border-t border-border px-4 py-2.5 text-xs first:border-t-0 hover:bg-raised"
              >
                <Badge variant="outline">{it.type}</Badge>
                <span className="min-w-0 truncate font-mono">{it.value}</span>
                {it.detail ? (
                  <span className="min-w-0 flex-1 truncate text-muted-foreground">— {it.detail}</span>
                ) : null}
              </div>
            ))}
          </div>
        </Card>
      ) : null}
    </div>
  )
}

// The full Markdown assessment report, rendered inline — the readable "raw"
// view of everything the report download would contain.
function RawReport({ session, title }: { session: string; title: string }) {
  const { t } = useI18n()
  const [md, setMd] = useState<string | null>(null)
  const [err, setErr] = useState<string>()
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!session) return
    let cancelled = false
    setMd(null)
    setErr(undefined)
    get<string>(
      `/engagement/report?session=${encodeURIComponent(session)}&title=${encodeURIComponent(title)}`,
    )
      .then((text) => !cancelled && setMd(typeof text === 'string' ? text : String(text)))
      .catch((e) => !cancelled && setErr((e as Error).message))
    return () => {
      cancelled = true
    }
  }, [session, title])

  const copy = async () => {
    if (md && (await copyText(md))) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }

  if (err)
    return (
      <p className="m-rise rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-4 py-3 text-sm text-destructive">
        {err}
      </p>
    )
  if (md === null) return <Skeleton className="h-64 w-full" />

  return (
    <Card className="p-5">
      <div className="mb-4 flex items-center justify-between">
        <CardTitle>{t('engagement.rawReport')}</CardTitle>
        <Button variant="outline" size="sm" onClick={copy} className="gap-1.5">
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? t('common.copied') : t('common.copy')}
        </Button>
      </div>
      <div className="prose-sm max-w-none text-[13px] leading-relaxed">
        <Markdown content={md} />
      </div>
    </Card>
  )
}
