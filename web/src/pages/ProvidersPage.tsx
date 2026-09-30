import { useMemo, useState } from 'react'
import {
  ArrowSquareOut,
  CheckCircle,
  Cpu,
  Desktop,
  Eye,
  EyeSlash,
  Key,
  Plugs,
  Plus,
  ShieldCheck,
  Trash,
} from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { formatProviderHeaders, parseProviderHeaders } from '@/lib/providerHeaders'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { ProviderHeadersField } from '@/components/providers/ProviderHeadersField'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import { Badge, EmptyState, Input, Label, Tabs, TabsList, TabsTrigger } from '@/components/ui/primitives'
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
  hint?: string
  key_hint?: string
  key_url?: string
  key_label?: string
  note?: string
  needs_region?: boolean
  needs_api_version?: boolean
  needs_base_url?: boolean
  timeout_seconds?: number
  headers?: Record<string, string>
  custom?: boolean
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
// A custom provider is always "API key" — even a localhost endpoint is a
// service the user configured, not a built-in local runtime.
function groupOf(p: ProviderInfo): Group {
  if (p.kind === 'copilot') return 'oauth'
  if (p.custom) return 'apikey'
  if (p.local) return 'local'
  return 'apikey'
}

const GROUP_ORDER: Group[] = ['oauth', 'apikey', 'local']

function ProviderStatus({ provider }: { provider: ProviderInfo }) {
  const { t } = useI18n()
	if (provider.has_key || (provider.custom && Object.keys(provider.headers ?? {}).length > 0)) {
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
    <Badge variant="outline" className="shrink-0">
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
  const [creating, setCreating] = useState(false)

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
              <section key={g} data-reveal className="space-y-3">
                <div className="flex items-center gap-2">
                  {g === 'oauth' ? (
                    <ShieldCheck className="size-4 text-muted-foreground" />
                  ) : g === 'local' ? (
                    <Desktop className="size-4 text-muted-foreground" />
                  ) : (
                    <Key className="size-4 text-muted-foreground" />
                  )}
                  <h2 className="text-[15px] font-medium tracking-[-0.2px]">{t(`providers.group.${g}` as never)}</h2>
                </div>
                <p className="-mt-1.5 text-xs text-muted-foreground">
                  {t(`providers.groupDesc.${g}` as never)}
                </p>
                <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
                  {grouped[g].map((p) => (
                    <div
                      key={p.id}
                      className={cn(
                        'flex flex-col gap-3.5 border bg-transparent p-4 transition-[border-color,background-color] duration-200',
                        p.active ? 'border-foreground' : 'border-border hover:border-line hover:bg-raised',
                      )}
                    >
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <span className="min-w-0 flex-1 truncate text-[15px] font-medium tracking-[-0.2px]">
                            {providerName(p.label)}
                          </span>
                          {p.active ? <Badge>{t('models.activeNow')}</Badge> : null}
                        </div>
                        <span className="mt-1 block truncate font-mono text-xs text-muted-foreground">
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
	                            variant={p.has_key || (p.custom && Object.keys(p.headers ?? {}).length > 0) ? 'outline' : 'default'}
	                            onClick={() => setTarget(p)}
	                          >
	                            {p.has_key || (p.custom && Object.keys(p.headers ?? {}).length > 0) ? t('providers.manage') : t('models.connect')}
                          </Button>
                        )}
                      </div>
                    </div>
                  ))}
                  {g === 'apikey' ? (
                    <button
                      onClick={() => setCreating(true)}
                      className="flex min-h-24 flex-col items-center justify-center gap-1.5 border border-dashed border-border p-4 text-muted-foreground transition-[border-color,background-color,color] duration-200 hover:border-line hover:bg-raised hover:text-foreground"
                    >
                      <Plus className="size-5" />
                      <span className="font-mono text-xs lowercase">{t('providers.addCustom')}</span>
                    </button>
                  ) : null}
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

      {creating ? (
        <AddProviderDialog
          onClose={() => setCreating(false)}
          onChanged={reload}
        />
      ) : null}
    </PageLayout>
  )
}

/**
 * Create a custom provider: a name, an OpenAI-compatible base URL, and an
 * optional key. Local endpoints are accepted; the backend verifies the pair
 * before saving.
 */
function AddProviderDialog({
  onClose,
  onChanged,
}: {
  onClose: () => void
  onChanged: () => void
}) {
  const { t } = useI18n()
  const [name, setName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [key, setKey] = useState('')
  const [headersText, setHeadersText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const create = async () => {
    if (!name.trim() || !baseURL.trim()) return
    let headers: Record<string, string>
    try {
      headers = parseProviderHeaders(headersText)
    } catch {
      setError(t('providers.headersInvalid'))
      return
    }

    setBusy(true)
    setError(undefined)
    try {
      const r = await post<{ ok: boolean; error?: string }>('/providers', {
        name: name.trim(),
        base_url: baseURL.trim(),
        api_key: key.trim(),
        headers,
      })
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

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : null)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('providers.newTitle')}</DialogTitle>
          <DialogDescription>{t('providers.newDesc')}</DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="np-name">{t('providers.name')}</Label>
            <Input
              id="np-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('providers.namePlaceholder')}
              autoFocus
              autoComplete="off"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="np-url">{t('setup.baseUrl')}</Label>
            <Input
              id="np-url"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
              placeholder="https://api.example.com/v1"
              className="font-mono text-xs"
              autoComplete="off"
              onKeyDown={(e) => e.key === 'Enter' && create()}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="np-key">{t('setup.apiKey')}</Label>
            <Input
              id="np-key"
              type="password"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder="sk-…"
              autoComplete="off"
              onKeyDown={(e) => e.key === 'Enter' && create()}
            />
            <p className="text-[11px] text-muted-foreground">{t('providers.keyOptional')}</p>
          </div>
          <ProviderHeadersField id="np-headers" value={headersText} onChange={setHeadersText} />
          {error ? (
            <p className="m-rise border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-3 py-2.5 text-xs text-destructive">{error}</p>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">{t('update.cancel')}</Button>
          </DialogClose>
          <Button size="sm" onClick={create} loading={busy} disabled={!name.trim() || !baseURL.trim()}>
            {t('providers.add')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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

  const llmModelsState = useApi<{ models: AllModel[] }>('/model/list-all')
  const myModels = (llmModelsState.data?.models ?? []).filter((m) => m.provider === p.id)
  const [newModel, setNewModel] = useState('')
  const [newCtx, setNewCtx] = useState('')
  const [ctxAuto, setCtxAuto] = useState(false)
  const [modelBusy, setModelBusy] = useState(false)

  // Advanced
  const [label, setLabel] = useState(p.label)
  const [timeout, setTimeoutSecs] = useState(String(p.timeout_seconds ?? ''))
  const [headersText, setHeadersText] = useState(() => formatProviderHeaders(p.headers))

  // A local runtime needs no key; bedrock takes AWS env credentials. A custom
  // provider usually wants one but a keyless service is fine, so the field is
  // shown yet optional there.
  const keyRequired = p.kind !== 'bedrock' && !p.local && !p.custom
  const showKey = p.kind !== 'bedrock' && !p.local
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
        `/providers/${encodeURIComponent(p.id)}/model-info?id=${encodeURIComponent(q)}`,
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
    let headers: Record<string, string> | undefined
    try {
      headers = p.custom ? parseProviderHeaders(headersText) : undefined
    } catch {
      setError(t('providers.headersInvalid'))
      return
    }

    setBusy(true)
    setError(undefined)
    try {
      await post(`/providers/${encodeURIComponent(p.id)}/settings`, {
        base_url: baseURL.trim(),
        timeout_seconds: timeout ? Number(timeout) : 0,
        ...(p.custom ? { label: label.trim(), headers } : {}),
      })
      onChanged()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const removeProvider = async () => {
    setBusy(true)
    try {
      await del(`/providers/${encodeURIComponent(p.id)}`)
      onChanged()
      onClose()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const tabBtn = (id: Section, label: string) => (
    <button
      onClick={() => setSection(id)}
      className={cn(
        'rounded-full border px-3 py-1.5 text-xs transition-colors duration-200',
        section === id
          ? 'border-transparent bg-nav-active text-foreground'
          : 'border-border text-muted-foreground hover:text-foreground',
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
          <DialogDescription>{t('providers.manageDesc')}</DialogDescription>
        </DialogHeader>

        <div className="flex shrink-0 flex-wrap gap-1.5 border-b border-border px-4 py-3 sm:px-5">
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
              {showKey ? (
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
                  {p.has_key && !key ? (
                    <p className="text-[11px] text-muted-foreground">{t('setup.keyKept')}</p>
                  ) : p.custom ? (
                    <p className="text-[11px] text-muted-foreground">{t('providers.keyOptional')}</p>
                  ) : null}
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
                <a href={keyURL} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-1.5 text-xs text-foreground underline decoration-line underline-offset-4 hover:decoration-foreground">
                  {t('setup.getKey', { provider: providerName(p.label) })}
                  <ArrowSquareOut className="size-3.5" />
                </a>
              ) : null}
              {error ? (
                <p className="m-rise border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-3 py-2.5 text-xs text-destructive">{error}</p>
              ) : null}
            </>
          ) : null}

          {section === 'models' ? (
            <>
              <div className="space-y-1.5 border border-border p-3.5">
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
                    className="font-mono text-xs sm:flex-1"
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
                    <div key={m.id} className="flex items-center gap-2 border border-border px-3 py-2.5 transition-[border-color,background-color] duration-200 hover:border-line hover:bg-raised">
                      <div className="min-w-0 flex-1">
                        <p className="truncate font-mono text-xs">{m.id}</p>
                        {m.context_window > 0 ? (
                          <p className="mt-0.5 font-mono text-[11px] tabular-nums text-dim">
                            {t('models.ctx', { n: Math.round(m.context_window / 1000) })}
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
          ) : null}

          {section === 'advanced' ? (
            <>
              {p.custom ? (
                <div className="space-y-1.5">
                  <Label htmlFor="m-name">{t('providers.name')}</Label>
                  <Input id="m-name" value={label} onChange={(e) => setLabel(e.target.value)} autoComplete="off" />
                </div>
              ) : null}
              <div className="space-y-1.5">
                <Label htmlFor="m-baseurl2">{t('models.baseUrl')}</Label>
                <Input id="m-baseurl2" value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder={p.kind} autoComplete="off" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="m-timeout">{t('providers.timeout')}</Label>
                <Input id="m-timeout" value={timeout} onChange={(e) => setTimeoutSecs(e.target.value)} placeholder="0" inputMode="numeric" />
                <p className="text-[11px] text-muted-foreground">{t('providers.timeoutHint')}</p>
              </div>
              {p.custom ? <ProviderHeadersField id="m-headers" value={headersText} onChange={setHeadersText} /> : null}
              {error ? (
                <p className="m-rise border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-3 py-2.5 text-xs text-destructive">{error}</p>
              ) : null}
            </>
          ) : null}
        </DialogBody>

        <DialogFooter>
          {p.custom ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={removeProvider}
              loading={busy}
              className="mr-auto gap-1.5 text-muted-foreground hover:text-destructive"
            >
              <Trash className="size-3.5" />
              {t('providers.deleteProvider')}
            </Button>
          ) : null}
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
