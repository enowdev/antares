import { useEffect, useMemo, useState } from 'react'
import { Eye, Lightning, MagnifyingGlass, Wrench } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import type { ReasoningCapability } from '@/lib/models'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Pagination } from '@/components/ui/Pagination'
import { Button } from '@/components/ui/button'
import { Badge, Card, EmptyState, Input } from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'

interface ModelInfo {
  id: string
  name: string
  provider: string
  context_window: number
  max_output: number
  input_cost: number
  output_cost: number
  vision: boolean
  tools: boolean
  reasoning: boolean
  reasoning_capability?: ReasoningCapability
}

interface ProviderInfo {
  id: string
  label: string
  kind: string
  enabled: boolean
  has_key: boolean
  local: boolean
  base_url: string
  active: boolean
  hint?: string
  key_hint?: string
  key_url?: string
  key_label?: string
  note?: string
  needs_region?: boolean
  needs_api_version?: boolean
  needs_base_url?: boolean
}

interface ModelsResponse {
  active: { model: string; provider: string }
  providers: ProviderInfo[]
}

interface AllModel extends ModelInfo {
  provider: string
  provider_label: string
}

interface AllModelsResponse {
  active: { model: string; provider: string }
  models: AllModel[]
  errors: { provider: string; label: string; error: string }[]
}

/**
 * Older configs stored labels like "Ollama (local)". Local endpoints are marked
 * with an icon now, so drop the suffix rather than reading it aloud in a
 * sentence like "Ollama (local) is not running".
 */
function providerName(label: string): string {
  return label.replace(/\s*\((local|lokal)\)\s*$/i, '').trim()
}

// Rendered as the "Models" tab of the Providers page. onManageProviders jumps
// to the Providers tab (where credentials are managed) instead of opening a
// dialog here.
export default function ModelsPage({ onManageProviders }: { onManageProviders?: () => void } = {}) {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<ModelsResponse>('/model/options')
  const [filter, setFilter] = useState('')
  const [saving, setSaving] = useState('')
  const manage = onManageProviders ?? (() => {})

  const [offset, setOffset] = useState(0)
  const PAGE = 20

  // Every connected provider's models, in one list. An authed provider is
  // active and all its models show — there is no per-provider mode.
  const allState = useApi<AllModelsResponse>('/model/list-all')

  // Filter across id, name, and provider; paginate the full filtered list.
  const allModels = useMemo(() => {
    const list = allState.data?.models ?? []
    const q = filter.trim().toLowerCase()
    return q
      ? list.filter(
          (m) =>
            m.id.toLowerCase().includes(q) ||
            m.name.toLowerCase().includes(q) ||
            m.provider_label.toLowerCase().includes(q),
        )
      : list
  }, [allState.data, filter])

  // A new search changes the result set, so start pagination over from the top.
  useEffect(() => setOffset(0), [filter])

  // A model always knows its provider, so switching is one step: set both.
  const selectModel = async (id: string, prov: string) => {
    setSaving(`${prov}/${id}`)
    try {
      await post('/model/set', { model: id, provider: prov })
      reload()
      allState.reload()
    } finally {
      setSaving('')
    }
  }

  const pagedAll = allModels.slice(offset, offset + PAGE)

  return (
    <PageLayout
      header={
        data ? (
          <div className="relative">
            <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder={t('models.searchAll')}
              className="pl-9"
            />
          </div>
        ) : undefined
      }
      footer={<Pagination offset={offset} limit={PAGE} total={allModels.length} onChange={setOffset} />}
    >

      {loading && !data ? (
        <div className="space-y-4">
          <Skeleton className="h-9 w-full max-w-md" />
          <SkeletonList count={5} />
        </div>
      ) : !data ? (
        <EmptyState title={t('models.loadProvidersFailed')} description={t('models.checkBackend')} />
      ) : (
        <>
          <AllModelsView
            state={allState}
            models={pagedAll}
            total={allModels.length}
            active={data.active}
            saving={saving}
            onUse={selectModel}
            onManage={manage}
          />
        </>
      )}
    </PageLayout>
  )
}

/**
 * Every connected provider's models in one searchable list, each tagged with
 * the provider it belongs to. Picking one switches provider and model together.
 */
function AllModelsView({
  state,
  models,
  total,
  active,
  saving,
  onUse,
  onManage,
}: {
  state: { loading: boolean; data?: AllModelsResponse }
  models: AllModel[]
  total: number
  active: { model: string; provider: string }
  saving: string
  onUse: (id: string, provider: string) => void
  onManage: () => void
}) {
  const { t } = useI18n()
  const errors = state.data?.errors ?? []

  return (
    <div className="space-y-3">
      {state.loading && !state.data ? (
        <SkeletonList count={6} />
      ) : models.length === 0 ? (
        <EmptyState title={t('models.none')} description={t('models.noneAllDesc')} />
      ) : (
        <div className="space-y-2">
          {total > models.length ? (
            <p className="text-xs text-muted-foreground">
              {t('models.showingOf', { shown: models.length, total })}
            </p>
          ) : null}
          {models.map((m) => {
            const isActive = m.id === active.model && m.provider === active.provider
            return (
              <Card
                key={`${m.provider}/${m.id}`}
                className={cn(
                  'flex items-center gap-3 p-3.5 transition-colors',
                  isActive ? 'border-primary' : 'hover:border-primary/40',
                )}
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <p className="truncate text-sm font-medium">{m.name}</p>
                    <Badge variant="secondary" className="shrink-0">
                      {providerName(m.provider_label)}
                    </Badge>
                  </div>
                  <p className="truncate font-mono text-[11px] text-muted-foreground">{m.id}</p>
                  <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                    {m.context_window > 0 ? (
                      <Badge variant="outline">
                        {t('models.ctx', { n: Math.round(m.context_window / 1000) })}
                      </Badge>
                    ) : null}
                    {m.vision ? (
                      <Badge variant="secondary" className="hidden sm:inline-flex">
                        <Eye className="size-3" /> {t('models.vision')}
                      </Badge>
                    ) : null}
                    {m.tools ? (
                      <Badge variant="secondary" className="hidden sm:inline-flex">
                        <Wrench className="size-3" /> {t('models.tools')}
                      </Badge>
                    ) : null}
                    {m.reasoning ? (
                      <Badge
                        variant="secondary"
                        className="hidden sm:inline-flex"
                        title={
                          m.reasoning_capability
                            ? m.reasoning_capability.values
                                .map((value) => value.label)
                                .join(', ') || t('reasoning.providerControlled')
                            : t('reasoning.providerControlled')
                        }
                      >
                        <Lightning className="size-3" />{' '}
                        {t('models.reasoning')}
                      </Badge>
                    ) : null}
                  </div>
                </div>
                <Button
                  size="sm"
                  variant={isActive ? 'secondary' : 'outline'}
                  disabled={isActive}
                  loading={saving === `${m.provider}/${m.id}`}
                  onClick={() => onUse(m.id, m.provider)}
                  className="shrink-0"
                >
                  {isActive ? t('common.active') : t('common.use')}
                </Button>
              </Card>
            )
          })}
        </div>
      )}

      {errors.length > 0 ? (
        <p className="text-xs text-muted-foreground">
          {t('models.someUnavailable', { providers: errors.map((e) => providerName(e.label)).join(', ') })}{' '}
          <button className="underline hover:text-foreground" onClick={onManage}>
            {t('models.manageProviders')}
          </button>
        </p>
      ) : null}
    </div>
  )
}

