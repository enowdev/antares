import { useState } from 'react'
import { ChartLineUp } from '@phosphor-icons/react'
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { useApi } from '@/lib/hooks'
import { cn, formatCount } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
  Tabs,
  TabsList,
  TabsTrigger,
} from '@/components/ui/primitives'
import { Skeleton, SkeletonStats } from '@/components/ui/skeleton'
import { useI18n, type TFunc } from '@/lib/i18n'

interface UsagePoint {
  bucket: string
  tokens_in: number
  tokens_out: number
  cost: number
  calls: number
}

interface ModelUsage {
  model: string
  provider: string
  calls: number
  tokens_in: number
  tokens_out: number
  cost: number
}

interface AnalyticsResponse {
  series: UsagePoint[]
  by_model: ModelUsage[]
  totals: { tokens_in: number; tokens_out: number; cost: number; calls: number }
}

const TH = 'border-b border-border px-4 py-3 text-left font-mono text-[11px] font-normal lowercase tracking-[0.04em] text-dim'
const TD = 'border-t border-border px-4 py-3.5'
// Monochrome chart: output in ink, input in grey, so the pair reads without colour.
const SERIES_OUT = 'var(--foreground)'
const SERIES_IN = 'var(--muted-foreground)'
const TICK = { fontSize: 10, fill: 'var(--muted-foreground)', fontFamily: 'var(--font-mono)' }

const RANGES = [
  { id: '24h', labelKey: 'analytics.range24h', bucket: 'hour' },
  { id: '7d', labelKey: 'analytics.range7d', bucket: 'day' },
  { id: '30d', labelKey: 'analytics.range30d', bucket: 'day' },
] as const

export default function AnalyticsPage() {
  const { t } = useI18n()
  const [range, setRange] = useState<(typeof RANGES)[number]['id']>('7d')
  const bucket = RANGES.find((r) => r.id === range)!.bucket
  const { data, loading } = useApi<AnalyticsResponse>(`/analytics?range=${range}&bucket=${bucket}`, [range])

  return (
    <PageLayout>
      <Tabs value={range} onValueChange={(v) => setRange(v as typeof range)}>
        <TabsList>
          {RANGES.map((r) => (
            <TabsTrigger key={r.id} value={r.id}>
              {t(r.labelKey)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {loading && !data ? (
        <>
          <SkeletonStats count={4} />
          <Skeleton className="h-56 w-full" />
        </>
      ) : !data || data.totals.calls === 0 ? (
        <EmptyState
          icon={<ChartLineUp className="size-8" />}
          title={t('analytics.none')}
          description={t('analytics.noneDesc')}
        />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <Stat label={t('analytics.calls')} value={formatCount(data.totals.calls)} />
            <Stat label={t('analytics.tokensIn')} value={formatCount(data.totals.tokens_in)} />
            <Stat label={t('analytics.tokensOut')} value={formatCount(data.totals.tokens_out)} />
            <Stat label={t('analytics.cost')} value={`$${data.totals.cost.toFixed(4)}`} />
          </div>

          <Card>
            <CardHeader>
              <CardTitle>{t('analytics.perPeriod')}</CardTitle>
            </CardHeader>
            <CardContent>
              <UsageChart series={data.series} t={t} />
            </CardContent>
          </Card>

          <section data-reveal className="space-y-3">
            <h3 className="text-[15px] font-medium tracking-[-0.2px]">{t('analytics.byModel')}</h3>
            <div className="overflow-auto rounded-[var(--radius-lg)] border border-border bg-card">
              <table className="w-full text-[13px]">
                <thead>
                  <tr>
                    <th className={TH}>{t('analytics.model')}</th>
                    <th className={cn(TH, 'text-right')}>{t('analytics.calls')}</th>
                    <th className={cn(TH, 'text-right')}>{t('analytics.tokensIn')}</th>
                    <th className={cn(TH, 'text-right')}>{t('analytics.tokensOut')}</th>
                    <th className={cn(TH, 'text-right')}>{t('analytics.cost')}</th>
                  </tr>
                </thead>
                <tbody>
                  {data.by_model.map((m) => (
                    <tr key={`${m.provider}/${m.model}`} className="transition-colors duration-200 hover:bg-raised">
                      <td className={cn(TD, 'max-w-56 truncate font-mono text-xs')}>{m.model}</td>
                      <td className={cn(TD, 'text-right tabular-nums')}>{m.calls}</td>
                      <td className={cn(TD, 'text-right tabular-nums')}>{formatCount(m.tokens_in)}</td>
                      <td className={cn(TD, 'text-right tabular-nums')}>{formatCount(m.tokens_out)}</td>
                      <td className={cn(TD, 'text-right tabular-nums')}>${m.cost.toFixed(4)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        </>
      )}
    </PageLayout>
  )
}

// UsageChart draws a smooth stacked area chart (output + input tokens) with
// gradient fills, a themed tooltip, and gridlines, via Recharts. Colours come
// from CSS variables so it tracks the light/dark theme.
function UsageChart({ series, t }: { series: UsagePoint[]; t: TFunc }) {
  return (
    <div>
      <ResponsiveContainer width="100%" height={224}>
        <AreaChart data={series} margin={{ top: 8, right: 8, bottom: 0, left: -8 }}>
          <defs>
            <linearGradient id="gradOut" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={SERIES_OUT} stopOpacity={0.22} />
              <stop offset="100%" stopColor={SERIES_OUT} stopOpacity={0} />
            </linearGradient>
            <linearGradient id="gradIn" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={SERIES_IN} stopOpacity={0.18} />
              <stop offset="100%" stopColor={SERIES_IN} stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke="var(--border)" vertical={false} />
          <XAxis
            dataKey="bucket"
            tick={TICK}
            tickLine={false}
            axisLine={{ stroke: 'var(--border)' }}
            minTickGap={24}
          />
          <YAxis
            tick={TICK}
            tickLine={false}
            axisLine={false}
            width={48}
            tickFormatter={(v: number) => formatCount(v)}
          />
          <Tooltip
            content={({ active, payload, label }) => (
              <ChartTooltip
                active={active}
                payload={payload as readonly TooltipEntry[] | undefined}
                label={label as string | number | undefined}
                t={t}
              />
            )}
            cursor={{ stroke: 'var(--line)', strokeWidth: 1 }}
          />
          {/* Not stacked: each area is drawn from the zero baseline so the
              real gap between output and input is visible. Output is drawn
              first (usually larger); input's stroke sits on top. */}
          <Area
            type="monotone"
            dataKey="tokens_out"
            stroke={SERIES_OUT}
            strokeWidth={1.5}
            fill="url(#gradOut)"
            name={t('analytics.tokensOut')}
          />
          <Area
            type="monotone"
            dataKey="tokens_in"
            stroke={SERIES_IN}
            strokeWidth={1.5}
            strokeDasharray="4 3"
            fill="url(#gradIn)"
            name={t('analytics.tokensIn')}
          />
        </AreaChart>
      </ResponsiveContainer>

      <div className="mt-3 flex items-center justify-center gap-5 font-mono text-[11px] text-muted-foreground">
        <Legend swatch={SERIES_OUT} label={t('analytics.tokensOut')} />
        <Legend swatch={SERIES_IN} label={t('analytics.tokensIn')} />
      </div>
    </div>
  )
}

interface TooltipEntry {
  name?: string
  value?: number
  dataKey?: string | number
  color?: string
}

function ChartTooltip({
  active,
  payload,
  label,
  t,
}: {
  active?: boolean
  payload?: readonly TooltipEntry[]
  label?: string | number
  t: TFunc
}) {
  if (!active || !payload?.length) return null
  const point = (payload[0] as { payload?: UsagePoint })?.payload
  return (
    <div className="rounded-[var(--radius-lg)] border border-border bg-popover px-3 py-2.5 text-xs shadow-[0_10px_28px_-14px_#00000080]">
      <p className="font-mono text-[11px] text-dim">{label}</p>
      <div className="mt-1.5 flex flex-col gap-1">
        {payload.map((e) => (
          <Legend
            key={String(e.dataKey)}
            swatch={e.color ?? SERIES_OUT}
            label={e.name ?? ''}
            value={formatCount(Number(e.value ?? 0))}
          />
        ))}
        {point ? (
          <p className="border-t border-border pt-1.5 font-mono text-[11px] tabular-nums text-muted-foreground">
            {point.calls} {t('analytics.calls').toLowerCase()} · ${point.cost.toFixed(4)}
          </p>
        ) : null}
      </div>
    </div>
  )
}

function Legend({ swatch, label, value }: { swatch: string; label: string; value?: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className="rounded-full size-2" style={{ background: swatch }} />
      <span className="text-muted-foreground">{label}</span>
      {value != null ? <span className="ml-1 font-medium tabular-nums text-foreground">{value}</span> : null}
    </span>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div data-reveal className="flex flex-col gap-1 rounded-[var(--radius-lg)] border border-border px-4 py-3.5">
      <p className="eyebrow">{label}</p>
      <p className="text-2xl font-medium tabular-nums tracking-[-0.4px]">{value}</p>
    </div>
  )
}
