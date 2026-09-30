import { Cpu, Database, HardDrives, Lightning, Robot, Timer } from '@phosphor-icons/react'
import { usePoll } from '@/lib/hooks'
import { formatBytes, formatCount } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import {
  Badge,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
} from '@/components/ui/primitives'
import { SkeletonStats, SkeletonList } from '@/components/ui/skeleton'
import { useI18n } from '@/lib/i18n'

interface SystemStats {
  version: string
  go_version: string
  os: string
  arch: string
  uptime_seconds: number
  goroutines: number
  memory_alloc: number
  memory_sys: number
  cpu_count: number
  workspace: string
  config_path: string
  database: { driver: string; size_bytes: number }
  store: {
    sessions: number
    messages: number
    memories: number
    cron_jobs: number
    tokens_in: number
    tokens_out: number
    cost: number
    empty_sessions: number
  }
  active_shells: number
  model: string
  provider: string
  rag_provider: string
}

function uptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d) return `${d}d ${h}h`
  if (h) return `${h}h ${m}m`
  return `${m}m`
}

function Stat({
  icon,
  label,
  value,
  hint,
}: {
  icon: React.ReactNode
  label: string
  value: string
  hint?: string
}) {
  return (
    <div
      data-reveal
      className="border border-border px-4 py-3.5 transition-[border-color,background-color] duration-200 hover:border-line"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="eyebrow truncate">{label}</span>
        <span className="shrink-0 text-dim">{icon}</span>
      </div>
      <p className="mt-2 text-2xl font-medium tabular-nums tracking-[-0.5px]">{value}</p>
      {hint ? <p className="mt-1 truncate font-mono text-xs text-muted-foreground">{hint}</p> : null}
    </div>
  )
}

export default function SystemPage() {
  const { t } = useI18n()
  const { data, loading, error } = usePoll<SystemStats>('/system/stats', 5000)

  return (
    <PageLayout>
      {loading && !data ? (
        <>
          <SkeletonStats count={4} />
          <SkeletonList count={3} />
        </>
      ) : error || !data ? (
        <EmptyState
          icon={<Robot className="size-8" />}
          title={t('system.down')}
          description={t('system.downDesc')}
        />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <Stat
              icon={<Timer className="size-4" />}
              label={t('system.uptime')}
              value={uptime(data.uptime_seconds)}
              hint={data.version.startsWith('v') ? data.version : `v${data.version}`}
            />
            <Stat
              icon={<Lightning className="size-4" />}
              label={t('system.tokens')}
              value={formatCount(data.store.tokens_in + data.store.tokens_out)}
              hint={`$${data.store.cost.toFixed(4)}`}
            />
            <Stat
              icon={<Database className="size-4" />}
              label={t('system.sessions')}
              value={formatCount(data.store.sessions)}
              hint={t('system.messages', { n: formatCount(data.store.messages) })}
            />
            <Stat
              icon={<HardDrives className="size-4" />}
              label={t('system.database')}
              value={formatBytes(data.database.size_bytes)}
              hint={data.database.driver}
            />
          </div>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <Cpu className="size-4 text-muted-foreground" />
                {t('system.runtime')}
              </CardTitle>
            </CardHeader>
            <CardContent className="grid gap-x-8 text-xs sm:grid-cols-2">
              <Row label="Go" value={`${data.go_version} · ${data.os}/${data.arch}`} />
              <Row label={t('system.goroutines')} value={String(data.goroutines)} />
              <Row label={t('system.heap')} value={formatBytes(data.memory_alloc)} />
              <Row label={t('system.sysMem')} value={formatBytes(data.memory_sys)} />
              <Row label={t('system.cpu')} value={t('system.cores', { n: data.cpu_count })} />
              <Row label={t('system.activeShells')} value={String(data.active_shells)} />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('system.activeConfig')}</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-x-8 text-xs sm:grid-cols-2">
              <Row label={t('system.model')} value={data.model || '—'} wrap />
              <Row label={t('system.provider')} value={data.provider || '—'} />
              <Row
                label={t('system.rag')}
                value={
                  data.rag_provider ? (
                    <Badge variant="success">{data.rag_provider}</Badge>
                  ) : (
                    <Badge variant="outline">{t('memory.inactive')}</Badge>
                  )
                }
              />
              <Row label={t('system.storedMemories')} value={formatCount(data.store.memories)} />
              <Row
                label={t('system.workspace')}
                wrap
                value={<code className="font-mono">{data.workspace}</code>}
              />
              <Row
                label={t('system.configPath')}
                wrap
                value={<code className="font-mono">{data.config_path}</code>}
              />
            </CardContent>
          </Card>
        </>
      )}
    </PageLayout>
  )
}

/**
 * One key/value line. `wrap` is for values with no natural break points —
 * filesystem paths and the like — which otherwise force the whole grid wider
 * than the viewport, since grid items default to min-width:auto.
 */
function Row({
  label,
  value,
  wrap,
}: {
  label: string
  value: React.ReactNode
  wrap?: boolean
}) {
  if (wrap) {
    return (
      <div className="min-w-0 border-t border-border py-2.5">
        <span className="text-muted-foreground">{label}</span>
        <p className="mt-1 break-all font-mono text-xs text-foreground">{value}</p>
      </div>
    )
  }
  return (
    <div className="flex min-w-0 items-center justify-between gap-3 border-t border-border py-2.5">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span className="min-w-0 truncate text-right font-mono text-xs tabular-nums text-foreground">{value}</span>
    </div>
  )
}
