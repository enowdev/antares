import { useMemo, useState } from 'react'
import {
  ArrowSquareOut,
  CheckCircle,
  Cpu,
  Desktop,
  Eye,
  EyeSlash,
  Key,
  Lightning,
  Plugs,
  ShieldCheck,
  Trash,
} from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import {
  cursorModelMatches,
  cursorVariantDimensions,
  cursorVariantSummary,
  defaultCursorVariant,
  type CursorModel,
} from '@/lib/cursorModels'
import { agentModelsErrorText, isAgentProvider, providerModelsPath, type ProviderCapability } from '@/lib/providerCapabilities'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import type { ReasoningCapability } from '@/lib/models'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import { Badge, Card, EmptyState, Input, Label, Tabs, TabsList, TabsTrigger } from '@/components/ui/primitives'
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SkeletonList } from '@/components/ui/skeleton'
import ModelsPage from '@/pages/ModelsPage'

interface ProviderInfo {
  id: string
  label: string
  kind: string
  enabled: boolean
  has_key: boolean
  local: boolean
  base_url: string
  active: boolean
  capability: ProviderCapability
  hint?: string
  key_hint?: string
  key_url?: string
  key_label?: string
  note?: string
  needs_region?: boolean
  needs_api_version?: boolean
  needs_base_url?: boolean
  timeout_seconds?: number
}

interface OptionsResponse {
  active: { model: string; provider: string }
  providers: ProviderInfo[]
}

const KEY_URLS: Record<string, string> = {
  openrouter: 'https://openrouter.ai/keys',
  anthropic: 'https://console.anthropic.com/settings/keys',
  openai: 'https://platform.openai.com/api-keys',
  gemini: 'https://aistudio.google.com/apikey',
}

function providerName(label: string): string {
  return label.replace(/\s*\((local|lokal)\)\s*$/i, '').trim()
}

type Group = 'oauth' | 'apikey' | 'local'

// How a provider authenticates decides its group. Only Copilot uses a device
// (OAuth) flow today; local endpoints need no credential; everything else is an
// API key (or cloud env credentials, which still live under "API key" here).
function groupOf(p: ProviderInfo): Group {
  if (p.kind === 'copilot') return 'oauth'
  if (p.local) return 'local'
  return 'apikey'
}

const GROUP_ORDER: Group[] = ['oauth', 'apikey', 'local']

function ProviderStatus({ provider }: { provider: ProviderInfo }) {
  const { t } = useI18n()
  if (provider.has_key) {
    return (
      <Badge variant="success" className="shrink-0">
        <CheckCircle className="size-3" weight="fill" />
        {t('models.connected')}
      </Badge>
    )
  }
  if (provider.local) {
    return (
      <Badge variant="outline" className="shrink-0">
        <Desktop className="size-3" />
        {t('models.localProvider')}
      </Badge>
    )
  }
  return (
    <Badge variant="warning" className="shrink-0">
      {t('models.needsKey')}
    </Badge>
  )
}

/**
 * The Providers tab: a grouped card grid of providers. Selecting one opens a
 * modal to manage its credentials, its models (add/remove, with auto-fetched
 * context window), and advanced settings (base URL, timeout, headers).
 */
function ProvidersTab({ onOpenModels }: { onOpenModels: () => void }) {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<OptionsResponse>('/model/options')
  const [target, setTarget] = useState<ProviderInfo | null>(null)

  const grouped = useMemo(() => {
    const g: Record<Group, ProviderInfo[]> = { oauth: [], apikey: [], local: [] }
    for (const p of data?.providers ?? []) g[groupOf(p)].push(p)
    return g
  }, [data])

  return (
    <PageLayout>
      {loading && !data ? (
        <SkeletonList count={6} />
      ) : !data ? (
        <EmptyState title={t('models.loadProvidersFailed')} description={t('models.checkBackend')} />
      ) : (
        <div className="space-y-6">
          {GROUP_ORDER.map((g) =>
            grouped[g].length === 0 ? null : (
              <section key={g} className="space-y-2">
                <div className="flex items-center gap-2">
                  {g === 'oauth' ? (
                    <ShieldCheck className="size-4 text-primary" weight="fill" />
                  ) : g === 'local' ? (
                    <Desktop className="size-4 text-muted-foreground" />
                  ) : (
                    <Key className="size-4 text-muted-foreground" />
                  )}
                  <h2 className="text-sm font-semibold">{t(`providers.group.${g}` as never)}</h2>
                </div>
                <p className="text-xs text-muted-foreground">
                  {t(`providers.groupDesc.${g}` as never)}
                </p>
                <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
                  {grouped[g].map((p) => (
                    <Card
                      key={p.id}
                      className={cn(
                        'flex flex-col gap-3 p-3.5 transition-colors',
                        p.active ? 'border-primary' : 'hover:border-primary/40',
                      )}
                    >
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <span className="min-w-0 flex-1 truncate text-sm font-medium">
                            {providerName(p.label)}
                          </span>
                          {isAgentProvider(p) ? <Badge variant="outline">{t('providers.agentIntegration')}</Badge> : null}
                          {p.active && !isAgentProvider(p) ? <Badge>{t('models.activeNow')}</Badge> : null}
                        </div>
                        <span className="mt-0.5 block truncate font-mono text-[10px] text-muted-foreground">
                          {p.base_url || p.kind}
                        </span>
                      </div>
                      <div className="flex items-center justify-between gap-2">
                        <ProviderStatus provider={p} />
                        {p.local ? (
                          <Button size="sm" variant="outline" onClick={onOpenModels}>
                            {t('common.use')}
                          </Button>
                        ) : (
                          <Button
                            size="sm"
                            variant={p.has_key ? 'outline' : 'default'}
                            onClick={() => setTarget(p)}
                          >
                            {p.has_key ? t('providers.manage') : t('models.connect')}
                          </Button>
                        )}
                      </div>
                    </Card>
                  ))}
                </div>
              </section>
            ),
          )}
        </div>
      )}

      {target ? (
        <ProviderModal
          provider={target}
          onClose={() => setTarget(null)}
          onChanged={reload}
        />
      ) : null}
    </PageLayout>
  )
}

/**
 * The Providers page hosts two tabs — Providers (connect/manage credentials)
 * and Models (pick the active model) — under one sidebar entry. The tab bar
 * stays pinned while each tab scrolls its own content.
 */
export default function ProvidersPage() {
  const { t } = useI18n()
  const [tab, setTab] = useState('providers')

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="shrink-0 pb-3">
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList>
            <TabsTrigger value="providers" className="gap-1.5">
              <Plugs className="size-3.5" /> {t('providers.tabProviders')}
            </TabsTrigger>
            <TabsTrigger value="models" className="gap-1.5">
              <Cpu className="size-3.5" /> {t('providers.tabModels')}
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </div>
      <div className="flex min-h-0 flex-1 flex-col">
        {tab === 'providers' ? (
          <ProvidersTab onOpenModels={() => setTab('models')} />
        ) : (
          <ModelsPage onManageProviders={() => setTab('providers')} />
        )}
      </div>
    </div>
  )
}

interface AllModel {
  id: string
  name: string
  provider: string
  provider_label: string
  context_window: number
  reasoning: boolean
  reasoning_capability?: ReasoningCapability
}

interface AgentModelsResponse {
  models: CursorModel[]
  needs_key?: boolean
  error?: string
}

/**
 * One Cursor model as the catalogue describes it: its aliases, the parameter
 * values real variants offer, and the variant a run starts from. Choosing a
 * model for execution happens in the composer, not here.
 */
function AgentModelRow({ model }: { model: CursorModel }) {
  const { t } = useI18n()
  const dimensions = cursorVariantDimensions(model)
  const variants = model.variants ?? []
  const preferred = defaultCursorVariant(model)
  const summary = preferred ? cursorVariantSummary(model, preferred) : ''

  return (
    <div className="rounded-[var(--radius-sm)] border border-border p-2.5">
      <p className="truncate font-mono text-xs">{model.id}</p>
      <p className="truncate text-xs text-muted-foreground">{model.name}</p>
      {model.description ? (
        <p className="mt-1 text-[11px] text-muted-foreground">{model.description}</p>
      ) : null}
      {(model.aliases ?? []).length > 0 ? (
        <p className="mt-1 truncate text-[10px] text-muted-foreground">
          {t('providers.aliases', { list: (model.aliases ?? []).join(', ') })}
        </p>
      ) : null}
      {dimensions.map((dimension) => (
        <p key={dimension.id} className="mt-1 text-[10px] text-muted-foreground">
          <span className="font-medium">{dimension.label}:</span>{' '}
          {dimension.values.map((value) => value.label).join(', ')}
        </p>
      ))}
      {variants.length > 0 ? (
        <p className="mt-1 text-[10px] text-muted-foreground">
          {t('providers.variantCount', { n: variants.length })}
          {summary ? ` · ${t('providers.defaultVariant', { summary })}` : ''}
        </p>
      ) : (
        <p className="mt-1 text-[10px] leading-snug text-[var(--warning)]">
          {t('target.cursorNoVariant')}
        </p>
      )}
    </div>
  )
}

/**
 * Manage one provider in a modal: credentials, its models (add/remove with an
 * auto-fetched context window), and advanced settings. Each section saves to
 * its own endpoint so a change is committed the moment you make it.
 */
function ProviderModal({
  provider,
  onClose,
  onChanged,
}: {
  provider: ProviderInfo
  onClose: () => void
  onChanged: () => void
}) {
  const { t } = useI18n()
  const p = provider

  // Which section is open. Credentials first — it is why most people open this.
  type Section = 'credentials' | 'models' | 'advanced'
  const [section, setSection] = useState<Section>('credentials')

  // Credentials
  const [key, setKey] = useState('')
  const [baseURL, setBaseURL] = useState(p.base_url ?? '')
  const [region, setRegion] = useState('')
  const [apiVersion, setApiVersion] = useState('')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  // Both hooks must run for every provider; the irrelevant endpoint is disabled.
  const agentOnly = isAgentProvider(p)
  const agentModelsState = useApi<AgentModelsResponse>(providerModelsPath(p))
  const llmModelsState = useApi<{ models: AllModel[] }>(agentOnly ? null : '/model/list-all')
  const myModels = (llmModelsState.data?.models ?? []).filter((m) => m.provider === p.id)
  const agentModelsError = agentModelsErrorText(agentModelsState.data, agentModelsState.error)
  const [agentQuery, setAgentQuery] = useState('')
  const agentModels = useMemo(
    () =>
      (agentModelsState.data?.models ?? []).filter((model) =>
        cursorModelMatches(model, agentQuery),
      ),
    [agentModelsState.data, agentQuery],
  )
  const [newModel, setNewModel] = useState('')
  const [newCtx, setNewCtx] = useState('')
  const [ctxAuto, setCtxAuto] = useState(false)
  const [modelBusy, setModelBusy] = useState(false)

  // Advanced
  const [timeout, setTimeoutSecs] = useState(String(p.timeout_seconds ?? ''))

  const keyRequired = p.kind !== 'bedrock' && !p.local
  const canConnect =
    (!keyRequired || key.trim() !== '') &&
    (!p.needs_base_url || baseURL.trim() !== '') &&
    (!p.needs_region || region.trim() !== '')
  const keyURL = p.key_url ?? KEY_URLS[p.id]

  const saveKey = async () => {
    if (!canConnect) return
    setBusy(true)
    setError(undefined)
    try {
      const r = await post<{ ok: boolean; error?: string }>(
        `/providers/${encodeURIComponent(p.id)}/key`,
        { api_key: key.trim(), base_url: baseURL.trim(), region: region.trim(), api_version: apiVersion.trim() },
      )
      if (!r.ok) {
        setError(r.error ?? t('models.connectFailed'))
        return
      }
      onChanged()
      onClose()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  // Try to auto-fill the context window from the provider when the id looks set.
  const autoFetchCtx = async (id: string) => {
    const q = id.trim()
    if (!q) return
    try {
      const r = await get<{ found: boolean; context_window?: number }>(
        `/providers/${encodeURIComponent(p.id)}/model-info?model=${encodeURIComponent(q)}`,
      )
      if (r.found && r.context_window) {
        setNewCtx(String(r.context_window))
        setCtxAuto(true)
      } else {
        setCtxAuto(false)
      }
    } catch {
      setCtxAuto(false)
    }
  }

  const addModel = async () => {
    const id = newModel.trim()
    if (!id) return
    setModelBusy(true)
    try {
      await post(`/providers/${encodeURIComponent(p.id)}/model`, {
        model: id,
        context_window: newCtx ? Number(newCtx) : 0,
      })
      setNewModel('')
      setNewCtx('')
      setCtxAuto(false)
      llmModelsState.reload()
      onChanged()
    } finally {
      setModelBusy(false)
    }
  }

  const removeModel = async (id: string) => {
    await del(`/providers/${encodeURIComponent(p.id)}/model/${encodeURIComponent(id)}`)
    llmModelsState.reload()
    onChanged()
  }

  const saveSettings = async () => {
    setBusy(true)
    try {
      await post(`/providers/${encodeURIComponent(p.id)}/settings`, {
        base_url: baseURL.trim(),
        timeout_seconds: timeout ? Number(timeout) : 0,
      })
      onChanged()
    } finally {
      setBusy(false)
    }
  }

  const tabBtn = (id: Section, label: string) => (
    <button
      onClick={() => setSection(id)}
      className={cn(
        'rounded-[var(--radius-sm)] px-3 py-1.5 text-xs font-medium transition-colors',
        section === id ? 'bg-primary/12 text-primary' : 'text-muted-foreground hover:text-foreground',
      )}
    >
      {label}
    </button>
  )

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : null)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{providerName(p.label)}</DialogTitle>
          <DialogDescription>{agentOnly ? t('providers.agentManageDesc') : t('providers.manageDesc')}</DialogDescription>
        </DialogHeader>

        <div className="flex gap-1 border-b border-border pb-2">
          {tabBtn('credentials', t('providers.secCredentials'))}
          {tabBtn('models', t('providers.secModels'))}
          {tabBtn('advanced', t('providers.secAdvanced'))}
        </div>

        <DialogBody className="space-y-4">
          {section === 'credentials' ? (
            <>
              {p.needs_base_url ? (
                <div className="space-y-1.5">
                  <Label htmlFor="m-baseurl">{t('models.baseUrl')}</Label>
                  <Input id="m-baseurl" value={baseURL} onChange={(e) => setBaseURL(e.target.value)} autoComplete="off" />
                </div>
              ) : null}
              {keyRequired ? (
                <div className="space-y-1.5">
                  <Label htmlFor="m-key">{p.key_label ?? t('setup.apiKey')}</Label>
                  <div className="flex gap-2">
                    <Input
                      id="m-key"
                      type={reveal ? 'text' : 'password'}
                      autoFocus
                      value={key}
                      onChange={(e) => setKey(e.target.value)}
                      onKeyDown={(e) => e.key === 'Enter' && canConnect && saveKey()}
                      placeholder={p.key_hint ?? 'sk-…'}
                      autoComplete="off"
                    />
                    <Button variant="outline" size="icon" onClick={() => setReveal((v) => !v)} aria-label={t('config.reveal')}>
                      {reveal ? <EyeSlash className="size-4" /> : <Eye className="size-4" />}
                    </Button>
                  </div>
                </div>
              ) : null}
              {p.needs_region ? (
                <div className="space-y-1.5">
                  <Label htmlFor="m-region">{t('models.region')}</Label>
                  <Input id="m-region" value={region} onChange={(e) => setRegion(e.target.value)} placeholder="us-east-1" autoComplete="off" />
                </div>
              ) : null}
              {p.needs_api_version ? (
                <div className="space-y-1.5">
                  <Label htmlFor="m-apiver">{t('models.apiVersion')}</Label>
                  <Input id="m-apiver" value={apiVersion} onChange={(e) => setApiVersion(e.target.value)} placeholder="2024-10-21" autoComplete="off" />
                </div>
              ) : null}
              {p.note ? <p className="text-[11px] leading-relaxed text-muted-foreground">{p.note}</p> : null}
              {keyURL ? (
                <a href={keyURL} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-1.5 text-xs text-primary underline underline-offset-2">
                  {t('setup.getKey', { provider: providerName(p.label) })}
                  <ArrowSquareOut className="size-3.5" />
                </a>
              ) : null}
              {error ? (
                <p className="rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/10 p-2.5 text-xs text-destructive">{error}</p>
              ) : null}
            </>
          ) : null}

          {section === 'models' ? (
            <>
              {agentOnly ? (
                <>
                  <p className="text-xs text-muted-foreground">{t('providers.agentModelsReadOnly')}</p>
                  {agentModelsState.data?.needs_key ? (
                    <p className="py-4 text-center text-xs text-muted-foreground">{t('models.needsKey')}</p>
                  ) : agentModelsError ? (
                    <p className="rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/10 p-2.5 text-xs text-destructive">
                      {agentModelsError}
                    </p>
                  ) : (agentModelsState.data?.models ?? []).length === 0 ? (
                    <p className="py-4 text-center text-xs text-muted-foreground">{t('models.none')}</p>
                  ) : (
                    <>
                      <Input
                        value={agentQuery}
                        onChange={(e) => setAgentQuery(e.target.value)}
                        placeholder={t('providers.searchModels')}
                        aria-label={t('providers.searchModels')}
                        className="h-8 text-xs"
                      />
                      {agentModels.length === 0 ? (
                        <p className="py-4 text-center text-xs text-muted-foreground">{t('models.none')}</p>
                      ) : (
                        <div className="max-h-64 space-y-1.5 overflow-y-auto">
                          {agentModels.map((m) => (
                            <AgentModelRow key={m.id} model={m} />
                          ))}
                        </div>
                      )}
                    </>
                  )}
                </>
              ) : (
                <>
                  <div className="space-y-1.5 rounded-[var(--radius-sm)] border border-border p-3">
                    <Label>{t('providers.addModel')}</Label>
                    <div className="flex flex-col gap-2 sm:flex-row">
                      <Input
                        value={newModel}
                        onChange={(e) => {
                          setNewModel(e.target.value)
                          setCtxAuto(false)
                        }}
                        onBlur={() => autoFetchCtx(newModel)}
                        placeholder={t('providers.modelIdPlaceholder')}
                        className="sm:flex-1"
                      />
                      <Input
                        value={newCtx}
                        onChange={(e) => setNewCtx(e.target.value)}
                        placeholder={t('providers.ctxPlaceholder')}
                        inputMode="numeric"
                        className="sm:w-40"
                      />
                      <Button size="sm" onClick={addModel} loading={modelBusy} disabled={!newModel.trim()}>
                        {t('providers.add')}
                      </Button>
                    </div>
                    <p className="text-[11px] text-muted-foreground">
                      {ctxAuto ? t('providers.ctxAuto') : t('providers.ctxHint')}
                    </p>
                  </div>

                  {myModels.length === 0 ? (
                    <p className="py-4 text-center text-xs text-muted-foreground">{t('models.none')}</p>
                  ) : (
                    <div className="max-h-64 space-y-1.5 overflow-y-auto">
                      {myModels.map((m) => (
                        <div key={m.id} className="flex items-center gap-2 rounded-[var(--radius-sm)] border border-border p-2.5">
                          <div className="min-w-0 flex-1">
                            <p className="truncate font-mono text-xs">{m.id}</p>
                            {m.context_window > 0 ? (
                              <p className="text-[10px] text-muted-foreground">
                                {t('models.ctx', { n: Math.round(m.context_window / 1000) })}
                              </p>
                            ) : null}
                            {m.reasoning ? (
                              <p className="flex items-center gap-1 text-[10px] text-muted-foreground">
                                <Lightning className="size-3" />
                                {m.reasoning_capability
                                  ? t('models.reasoning')
                                  : t('reasoning.providerControlled')}
                              </p>
                            ) : null}
                          </div>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label={t('common.delete')}
                            onClick={() => removeModel(m.id)}
                            className="shrink-0 text-muted-foreground hover:text-destructive"
                          >
                            <Trash className="size-4" />
                          </Button>
                        </div>
                      ))}
                    </div>
                  )}
                </>
              )}
            </>
          ) : null}

          {section === 'advanced' ? (
            <>
              <div className="space-y-1.5">
                <Label htmlFor="m-baseurl2">{t('models.baseUrl')}</Label>
                <Input id="m-baseurl2" value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder={p.kind} autoComplete="off" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="m-timeout">{t('providers.timeout')}</Label>
                <Input id="m-timeout" value={timeout} onChange={(e) => setTimeoutSecs(e.target.value)} placeholder="0" inputMode="numeric" />
                <p className="text-[11px] text-muted-foreground">{t('providers.timeoutHint')}</p>
              </div>
            </>
          ) : null}
        </DialogBody>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">{t('common.close')}</Button>
          </DialogClose>
          {section === 'credentials' ? (
            <Button size="sm" onClick={saveKey} loading={busy} disabled={!canConnect}>
              {t('models.connect')}
            </Button>
          ) : section === 'advanced' ? (
            <Button size="sm" onClick={saveSettings} loading={busy}>
              {t('common.save')}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
