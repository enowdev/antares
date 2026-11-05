import { useMemo, useState } from 'react'
import { MagnifyingGlass, ShieldWarning, Toolbox } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Badge, EmptyState, Input, Switch } from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'
import { useI18n } from '@/lib/i18n'
import { toolsetsOrEmpty } from '@/lib/toolPayload'

interface ToolInfo {
  name: string
  description: string
  enabled: boolean
  requires_approval: boolean
  toolsets: string[] | null
}

interface ToolsResponse {
  toolset: string
  toolsets: string[] | null
  tools: ToolInfo[]
}

export default function ToolsPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<ToolsResponse>('/tools')
  const [busy, setBusy] = useState('')
  const [filter, setFilter] = useState('')
  const [mutatingOnly, setMutatingOnly] = useState(false)

  const toggle = async (name: string, enabled: boolean) => {
    setBusy(name)
    try {
      await post('/tools/toggle', { name, enabled })
      reload()
    } finally {
      setBusy('')
    }
  }

  const setToolset = async (toolset: string) => {
    setBusy(toolset)
    try {
      await post('/tools/toolset', { toolset })
      reload()
    } finally {
      setBusy('')
    }
  }

  const tools = data?.tools ?? []
  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return tools.filter((item) => {
      if (mutatingOnly && !item.requires_approval) return false
      if (!q) return true
      return (
        item.name.toLowerCase().includes(q) || item.description.toLowerCase().includes(q)
      )
    })
  }, [tools, filter, mutatingOnly])

  const enabledCount = tools.filter((x) => x.enabled).length

  const header = data ? (
    <div className="space-y-3">
      {/* Active toolset preset — stays in view while the list scrolls. */}
      <div>
        <p className="eyebrow mb-2">
          {t('tools.activeToolset')}
        </p>
        <div className="flex flex-wrap gap-1.5">
          {toolsetsOrEmpty(data.toolsets).map((preset) => (
            <button
              key={preset}
              disabled={!!busy}
              onClick={() => setToolset(preset)}
              className={cn(
                'rounded-full border px-3 py-1.5 font-mono text-xs transition-colors duration-200',
                preset === data.toolset
                  ? 'border-transparent bg-nav-active text-foreground'
                  : 'border-border text-muted-foreground hover:text-foreground',
              )}
            >
              {preset}
            </button>
          ))}
        </div>
      </div>

      {/* Search + filters + counter. */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-0 flex-1">
          <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t('tools.search')}
            className="pl-9"
          />
        </div>
        <button
          onClick={() => setMutatingOnly((v) => !v)}
          className={cn(
            'inline-flex shrink-0 items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs transition-colors duration-200',
            mutatingOnly
              ? 'border-transparent bg-nav-active text-foreground'
              : 'border-border text-muted-foreground hover:text-foreground',
          )}
        >
          <ShieldWarning className={cn('size-3.5', mutatingOnly && 'text-[var(--warning)]')} />
          {t('tools.filterMutating')}
        </button>
        <span className="shrink-0 font-mono text-xs tabular-nums text-muted-foreground">
          {t('tools.enabledCount', { on: enabledCount, total: tools.length })}
        </span>
      </div>
    </div>
  ) : undefined

  return (
    <PageLayout header={header}>
      {loading && !data ? (
        <SkeletonList count={8} />
      ) : !data ? (
        <EmptyState title={t('tools.loadFailed')} />
      ) : tools.length === 0 ? (
        <EmptyState icon={<Toolbox className="size-8" />} title={t('tools.none')} />
      ) : shown.length === 0 ? (
        <EmptyState icon={<MagnifyingGlass className="size-8" />} title={t('tools.noMatch')} />
      ) : (
        <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
          {shown.map((item) => (
            <div
              key={item.name}
              data-reveal
              className={cn(
                'flex flex-col rounded-[var(--radius-lg)] border border-border bg-card p-3.5 transition-[border-color,background-color,opacity] duration-200 hover:border-line hover:bg-raised',
                !item.enabled && 'opacity-60',
              )}
            >
              <div className="flex items-start justify-between gap-2">
                <span className="min-w-0 break-all font-mono text-xs text-foreground">{item.name}</span>
                <Switch
                  checked={item.enabled}
                  disabled={busy === item.name}
                  onCheckedChange={(v) => toggle(item.name, v)}
                  aria-label={`${t('common.enable')} ${item.name}`}
                />
              </div>
              <p className="mt-1.5 line-clamp-3 flex-1 text-xs leading-relaxed text-muted-foreground">
                {item.description}
              </p>
              <div className="mt-3 flex flex-wrap gap-1.5">
                {item.requires_approval ? (
                  <Badge variant="warning">
                    <ShieldWarning className="size-3" weight="fill" />
                    {t('tools.mutates')}
                  </Badge>
                ) : null}
                {toolsetsOrEmpty(item.toolsets).slice(0, 4).map((ts) => (
                  <Badge key={ts} variant="outline">
                    {ts}
                  </Badge>
                ))}
                {toolsetsOrEmpty(item.toolsets).length > 4 ? (
                  <Badge variant="outline">+{toolsetsOrEmpty(item.toolsets).length - 4}</Badge>
                ) : null}
              </div>
            </div>
          ))}
        </div>
      )}
    </PageLayout>
  )
}
