import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  ArrowLeft,
  ArrowRight,
  ArrowSquareOut,
  CheckCircle,
  Database,
  Eye,
  EyeSlash,
  MagnifyingGlass,
  Warning,
} from '@phosphor-icons/react'
import { get, post } from '@/lib/api'
import { parseProviderHeaders } from '@/lib/providerHeaders'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { ProviderHeadersField } from '@/components/providers/ProviderHeadersField'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Input,
  Label,
  Switch,
} from '@/components/ui/primitives'
import { Skeleton } from '@/components/ui/skeleton'

interface SetupProvider {
  id: string
  label: string
  kind: string
  hint: string
  key_hint?: string
  key_url?: string
  base_url?: string
  local: boolean
  models?: string[]
  has_key: boolean
}

interface SetupStatus {
  needs_setup: boolean
  model: string
  provider: string
  workspace: string
  home: string
  config_path: string
  providers: SetupProvider[]
  database: string
}

interface TestResult {
  ok: boolean
  error?: string
  note?: string
  models?: string[]
  suggested?: string[]
}

type StepId = 'provider' | 'key' | 'model' | 'workspace' | 'extras' | 'done'

const STEPS: StepId[] = ['provider', 'key', 'model', 'workspace', 'extras', 'done']

export default function SetupPage() {
  const { t } = useI18n()
  const navigate = useNavigate()

  const [status, setStatus] = useState<SetupStatus>()
  const [loading, setLoading] = useState(true)
  const [step, setStep] = useState<StepId>('provider')

  const [providerId, setProviderId] = useState('openrouter')
  const [providerName, setProviderName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [headersText, setHeadersText] = useState('')
  const [revealKey, setRevealKey] = useState(false)
  const [testing, setTesting] = useState(false)
  const [test, setTest] = useState<TestResult>()

  const [model, setModel] = useState('')
  const [modelFilter, setModelFilter] = useState('')
  const [workspace, setWorkspace] = useState('')
  const [dbDriver, setDbDriver] = useState<'sqlite' | 'postgres'>('sqlite')
  const [dbDSN, setDbDSN] = useState('')

  const [ragEnabled, setRagEnabled] = useState(false)
  const [embedProvider, setEmbedProvider] = useState('voyage')
  const [embedModel, setEmbedModel] = useState('voyage-4')
  const [embedKey, setEmbedKey] = useState('')
  const [telegram, setTelegram] = useState('')
  const [dashboardPassword, setDashboardPassword] = useState('')

  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()

  useEffect(() => {
    get<SetupStatus>('/setup/status')
      .then((s) => {
        setStatus(s)
        setWorkspace(s.workspace)
        if (s.provider) setProviderId(s.provider)
        if (s.model) setModel(s.model)
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  const provider = useMemo(
    () => status?.providers.find((p) => p.id === providerId),
    [status, providerId],
  )

  // Local endpoints need no credential, so that step is skipped entirely.
  const skipsKey = !!provider?.local

  const stepIndex = STEPS.indexOf(step)
  const goNext = () => {
    let next = STEPS[Math.min(stepIndex + 1, STEPS.length - 1)]
    if (next === 'key' && skipsKey) next = 'model'
    setStep(next)
  }
  const goBack = () => {
    let prev = STEPS[Math.max(stepIndex - 1, 0)]
    if (prev === 'key' && skipsKey) prev = 'provider'
    setStep(prev)
  }

  const runTest = async () => {
    let headers: Record<string, string> | undefined
    try {
      headers = providerId === 'custom' ? parseProviderHeaders(headersText) : undefined
    } catch {
      setError(t('providers.headersInvalid'))
      return
    }

    setTesting(true)
    setTest(undefined)
    setError(undefined)
    try {
      const r = await post<TestResult>('/setup/test', {
        provider: providerId,
        base_url: baseURL || provider?.base_url || '',
        api_key: apiKey,
        ...(providerId === 'custom' ? { headers } : {}),
      })
      setTest(r)
      if (r.ok) {
        const first = (r.suggested ?? []).find((s) => (r.models ?? []).includes(s))
        if (!model) setModel(first ?? r.suggested?.[0] ?? r.models?.[0] ?? '')
        goNext()
      }
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setTesting(false)
    }
  }

  const finish = async () => {
    let headers: Record<string, string> | undefined
    try {
      headers = providerId === 'custom' ? parseProviderHeaders(headersText) : undefined
    } catch {
      setError(t('providers.headersInvalid'))
      return
    }

    setSaving(true)
    setError(undefined)
    try {
      await post('/setup/complete', {
        provider: providerId,
        name: providerName,
        base_url: baseURL || provider?.base_url || '',
        api_key: apiKey,
        ...(providerId === 'custom' ? { headers } : {}),
        model,
        workspace,
        database: {
          driver: dbDriver,
          dsn: dbDriver === 'postgres' ? dbDSN.trim() : '',
        },
        rag: {
          enabled: ragEnabled,
          embed_provider: embedProvider,
          embed_model: embedModel,
          embed_api_key: embedKey,
        },
        telegram_token: telegram,
        dashboard_password: dashboardPassword,
      })
      setStep('done')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const modelOptions = useMemo(() => {
    const all = test?.models?.length ? test.models : (provider?.models ?? [])
    const q = modelFilter.trim().toLowerCase()
    const filtered = q ? all.filter((m) => m.toLowerCase().includes(q)) : all
    // Keep the curated suggestions at the top; they are the sensible defaults.
    const suggested = (test?.suggested ?? provider?.models ?? []).filter((s) =>
      filtered.includes(s),
    )
    const rest = filtered.filter((m) => !suggested.includes(m))
    return { suggested, rest: rest.slice(0, 60), total: filtered.length }
  }, [test, provider, modelFilter])

  if (loading) {
    return (
      <SetupShell stepIndex={0}>
        <Skeleton className="h-9 w-56" />
        <Skeleton className="h-64 w-full rounded-[var(--radius-lg)]" />
      </SetupShell>
    )
  }

  return (
    <SetupShell stepIndex={stepIndex}>
      {error ? (
        <div className="flex items-start gap-2 rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive">
          <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
          <span className="min-w-0 break-words">{error}</span>
        </div>
      ) : null}

      {step === 'provider' ? (
        <section className="space-y-4">
          <StepHeading
            title={t('setup.providerTitle')}
            description={t('setup.providerDesc')}
          />
          <div className="grid gap-2 sm:grid-cols-2">
            {status?.providers.map((p) => (
              <button
                key={p.id}
                onClick={() => {
                  setProviderId(p.id)
                  setBaseURL(p.base_url ?? '')
                  setTest(undefined)
                }}
                className={cn(
                  'rounded-[var(--radius-md)] border p-3.5 text-left transition-colors',
                  providerId === p.id
                    ? 'border-primary bg-primary/8'
                    : 'border-border hover:border-primary/40',
                )}
              >
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{p.label}</span>
                  {p.has_key ? <Badge variant="success">{t('setup.keySaved')}</Badge> : null}
                  {p.local ? <Badge variant="outline">{t('setup.local')}</Badge> : null}
                </div>
                <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{p.hint}</p>
              </button>
            ))}
          </div>

          {providerId === 'custom' ? (
            <div className="space-y-1.5">
              <Label htmlFor="provider-name">{t('setup.providerName')}</Label>
              <Input
                id="provider-name"
                value={providerName}
                onChange={(e) => setProviderName(e.target.value)}
                placeholder={t('providers.namePlaceholder')}
                autoComplete="off"
              />
              <p className="text-[11px] text-muted-foreground">{t('setup.providerNameHint')}</p>
            </div>
          ) : null}

          {providerId === 'custom' || provider?.local ? (
            <div className="space-y-1.5">
              <Label htmlFor="base-url">{t('setup.baseUrl')}</Label>
              <Input
                id="base-url"
                value={baseURL}
                onChange={(e) => setBaseURL(e.target.value)}
                placeholder="https://api.example.com/v1"
              />
            </div>
          ) : null}
          {providerId === 'custom' ? <ProviderHeadersField id="setup-headers" value={headersText} onChange={setHeadersText} /> : null}

          <StepNav
            onNext={goNext}
            nextLabel={t('setup.next')}
            disabled={providerId === 'custom' && !baseURL.trim()}
          />
        </section>
      ) : null}

      {step === 'key' ? (
        <section className="space-y-4">
          <StepHeading
            title={t('setup.keyTitle', { provider: provider?.label ?? '' })}
            description={t('setup.keyDesc')}
          />

          {provider?.key_url ? (
            <a
              href={provider.key_url}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1.5 text-xs text-primary underline underline-offset-2"
            >
              {t('setup.getKey', { provider: provider.label })}
              <ArrowSquareOut className="size-3.5" />
            </a>
          ) : null}

          <div className="space-y-1.5">
            <Label htmlFor="api-key">{t('setup.apiKey')}</Label>
            <div className="flex gap-2">
              <Input
                id="api-key"
                type={revealKey ? 'text' : 'password'}
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder={provider?.key_hint ?? 'sk-…'}
                autoComplete="off"
                autoFocus
                onKeyDown={(e) => e.key === 'Enter' && runTest()}
              />
              <Button
                variant="outline"
                size="icon"
                onClick={() => setRevealKey((v) => !v)}
                aria-label={t('config.reveal')}
              >
                {revealKey ? <EyeSlash className="size-4" /> : <Eye className="size-4" />}
              </Button>
            </div>
            {provider?.has_key && !apiKey ? (
              <p className="text-[11px] text-muted-foreground">{t('setup.keyKept')}</p>
            ) : null}
          </div>

          {test && !test.ok ? (
            <div className="flex items-start gap-2 rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive">
              <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
              <span className="min-w-0 break-words">{test.error}</span>
            </div>
          ) : null}

          <StepNav
            onBack={goBack}
            onNext={runTest}
            nextLabel={t('setup.testAndContinue')}
            loading={testing}
          />
        </section>
      ) : null}

      {step === 'model' ? (
        <section className="space-y-4">
          <StepHeading
            title={t('setup.modelTitle')}
            description={t('setup.modelDesc')}
          />

          {test?.note ? (
            <p className="rounded-[var(--radius-sm)] border border-border bg-muted/40 p-3 text-xs text-muted-foreground">
              {test.note}
            </p>
          ) : null}

          {modelOptions.total > 12 ? (
            <div className="relative">
              <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={modelFilter}
                onChange={(e) => setModelFilter(e.target.value)}
                placeholder={t('models.searchModel')}
                className="pl-9"
              />
            </div>
          ) : null}

          <div className="max-h-[46dvh] space-y-1.5 overflow-y-auto pr-1">
            {modelOptions.suggested.length > 0 ? (
              <p className="px-1 pt-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                {t('setup.recommended')}
              </p>
            ) : null}
            {modelOptions.suggested.map((id) => (
              <ModelOption key={id} id={id} active={model === id} onSelect={setModel} recommended />
            ))}
            {modelOptions.rest.length > 0 && modelOptions.suggested.length > 0 ? (
              <p className="px-1 pt-3 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                {t('setup.allModels')}
              </p>
            ) : null}
            {modelOptions.rest.map((id) => (
              <ModelOption key={id} id={id} active={model === id} onSelect={setModel} />
            ))}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="model-manual">{t('setup.orTypeId')}</Label>
            <Input
              id="model-manual"
              value={model}
              onChange={(e) => setModel(e.target.value)}
              className="font-mono text-xs"
            />
          </div>

          <StepNav onBack={goBack} onNext={goNext} nextLabel={t('setup.next')} disabled={!model} />
        </section>
      ) : null}

      {step === 'workspace' ? (
        <section className="space-y-4">
          <StepHeading
            title={t('setup.workspaceTitle')}
            description={t('setup.workspaceDesc')}
          />
          <div className="space-y-1.5">
            <Label htmlFor="workspace">{t('system.workspace')}</Label>
            <Input
              id="workspace"
              value={workspace}
              onChange={(e) => setWorkspace(e.target.value)}
              className="font-mono text-xs"
            />
            <p className="text-[11px] text-muted-foreground">{t('setup.workspaceHint')}</p>
          </div>

          <Card>
            <CardHeader>
              <CardTitle>{t('setup.storageTitle')}</CardTitle>
              <CardDescription>{t('setup.storageDesc')}</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="grid gap-2 sm:grid-cols-2">
                {(
                  [
                    ['sqlite', t('setup.storageSqlite')],
                    ['postgres', t('setup.storagePostgres')],
                  ] as const
                ).map(([id, label]) => (
                  <button
                    key={id}
                    type="button"
                    onClick={() => setDbDriver(id)}
                    className={cn(
                      'rounded-[var(--radius-md)] border p-3 text-left text-sm transition-colors',
                      dbDriver === id ? 'border-primary bg-primary/8' : 'border-border hover:border-primary/40',
                    )}
                  >
                    {label}
                  </button>
                ))}
              </div>
              {dbDriver === 'postgres' ? (
                <div className="space-y-1.5">
                  <Label htmlFor="db-dsn">{t('setup.storageDsn')}</Label>
                  <Input
                    id="db-dsn"
                    value={dbDSN}
                    onChange={(e) => setDbDSN(e.target.value)}
                    placeholder="postgres://user:pass@localhost:5432/antares?sslmode=disable"
                    className="font-mono text-xs"
                    autoComplete="off"
                  />
                  <p className="text-[11px] text-muted-foreground">{t('setup.storageDsnHint')}</p>
                </div>
              ) : null}
            </CardContent>
          </Card>

          <StepNav
            onBack={goBack}
            onNext={goNext}
            nextLabel={t('setup.next')}
            disabled={dbDriver === 'postgres' && !dbDSN.trim()}
          />
        </section>
      ) : null}

      {step === 'extras' ? (
        <section className="space-y-4">
          <StepHeading
            title={t('setup.extrasTitle')}
            description={t('setup.extrasDesc')}
          />

          <Card>
            <CardHeader>
              <div className="flex items-start gap-3">
                <Database className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <CardTitle>{t('memory.tabRag')}</CardTitle>
                  <CardDescription>{t('setup.ragDesc')}</CardDescription>
                </div>
                <Switch checked={ragEnabled} onCheckedChange={setRagEnabled} />
              </div>
            </CardHeader>
            {ragEnabled ? (
              <CardContent className="space-y-3">
                <div className="space-y-1.5">
                  <Label>{t('setup.embedProvider')}</Label>
                  <div className="grid grid-cols-3 gap-2">
                    {(
                      [
                        ['voyage', 'Voyage', 'voyage-4'],
                        ['openai', 'OpenAI', 'text-embedding-3-small'],
                        ['custom', t('setup.custom'), ''],
                      ] as const
                    ).map(([id, label, defModel]) => (
                      <button
                        key={id}
                        onClick={() => {
                          setEmbedProvider(id)
                          if (defModel) setEmbedModel(defModel)
                        }}
                        className={cn(
                          'rounded-[var(--radius-sm)] border p-2.5 text-center text-xs transition-colors',
                          embedProvider === id
                            ? 'border-primary bg-primary/8'
                            : 'border-border hover:border-primary/40',
                        )}
                      >
                        {label}
                      </button>
                    ))}
                  </div>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="embed">{t('setup.embedModel')}</Label>
                  <Input
                    id="embed"
                    value={embedModel}
                    onChange={(e) => setEmbedModel(e.target.value)}
                    className="font-mono text-xs"
                  />
                </div>
                {embedProvider !== 'openai' ? (
                  <div className="space-y-1.5">
                    <Label htmlFor="embedkey">{t('setup.embedKey')}</Label>
                    <Input
                      id="embedkey"
                      type="password"
                      value={embedKey}
                      onChange={(e) => setEmbedKey(e.target.value)}
                      placeholder={embedProvider === 'voyage' ? 'pa-…' : t('setup.embedKeyPlaceholder')}
                      autoComplete="off"
                      className="font-mono text-xs"
                    />
                    <p className="text-[11px] text-muted-foreground">{t('setup.embedKeyHint')}</p>
                  </div>
                ) : null}
              </CardContent>
            ) : null}
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('setup.telegramTitle')}</CardTitle>
              <CardDescription>{t('setup.telegramDesc')}</CardDescription>
            </CardHeader>
            <CardContent>
              <Input
                type="password"
                value={telegram}
                onChange={(e) => setTelegram(e.target.value)}
                placeholder={t('setup.telegramPlaceholder')}
                autoComplete="off"
              />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('setup.dashboardPasswordTitle')}</CardTitle>
              <CardDescription>{t('setup.dashboardPasswordDesc')}</CardDescription>
            </CardHeader>
            <CardContent>
              <Input
                type="password"
                value={dashboardPassword}
                onChange={(e) => setDashboardPassword(e.target.value)}
                placeholder={t('setup.dashboardPasswordPlaceholder')}
                autoComplete="new-password"
              />
            </CardContent>
          </Card>

          <StepNav
            onBack={goBack}
            onNext={finish}
            nextLabel={t('setup.finish')}
            loading={saving}
          />
        </section>
      ) : null}

      {step === 'done' ? (
        <section className="space-y-5 text-center">
          <CheckCircle className="mx-auto size-12 text-[var(--success)]" weight="fill" />
          <div className="space-y-1.5">
            <h2 className="text-xl font-semibold tracking-tight">{t('setup.doneTitle')}</h2>
            <p className="mx-auto max-w-md text-sm text-muted-foreground">
              {t('setup.doneDesc', { model })}
            </p>
          </div>
          <div className="flex flex-wrap justify-center gap-2">
            <Button onClick={() => navigate('/', { state: { fresh: true } })} className="gap-1.5">
              {t('setup.startChatting')}
              <ArrowRight className="size-4" />
            </Button>
            <Button variant="outline" onClick={() => navigate('/system/settings')}>
              {t('nav.config')}
            </Button>
          </div>
        </section>
      ) : null}
    </SetupShell>
  )
}

function SetupShell({ children, stepIndex }: { children: React.ReactNode; stepIndex: number }) {
  const { t } = useI18n()
  return (
    <div className="mx-auto flex min-h-dvh w-full max-w-2xl flex-col justify-center px-4 py-8 sm:px-6">
      <div className="mb-6 flex items-center gap-3">
        <img src="/antares-192.png" alt="" aria-hidden className="size-10 object-contain" />
        <div className="min-w-0">
          <p className="text-lg font-semibold tracking-tight">Antares</p>
          <p className="text-xs text-muted-foreground">{t('setup.subtitle')}</p>
        </div>
      </div>

      {/* Step dots: enough orientation without a heavy stepper. */}
      <div className="mb-6 flex items-center gap-1.5" aria-hidden>
        {STEPS.slice(0, -1).map((_, i) => (
          <span
            key={i}
            className={cn(
              'h-1 flex-1 rounded-full transition-colors',
              i <= stepIndex ? 'bg-primary' : 'bg-border',
            )}
          />
        ))}
      </div>

      <div className="space-y-5">{children}</div>
    </div>
  )
}

function StepHeading({ title, description }: { title: string; description: string }) {
  return (
    <div className="space-y-1.5">
      <h2 className="text-base font-semibold tracking-tight sm:text-lg">{title}</h2>
      <p className="text-xs leading-relaxed text-muted-foreground sm:text-sm">{description}</p>
    </div>
  )
}

function StepNav({
  onBack,
  onNext,
  nextLabel,
  loading,
  disabled,
}: {
  onBack?: () => void
  onNext: () => void
  nextLabel: string
  loading?: boolean
  disabled?: boolean
}) {
  const { t } = useI18n()
  return (
    <div className="flex items-center justify-between gap-3 pt-2">
      {onBack ? (
        <Button variant="ghost" size="sm" onClick={onBack} className="gap-1.5">
          <ArrowLeft className="size-4" />
          {t('setup.back')}
        </Button>
      ) : (
        <span />
      )}
      <Button onClick={onNext} loading={loading} disabled={disabled} className="gap-1.5">
        {nextLabel}
        <ArrowRight className="size-4" />
      </Button>
    </div>
  )
}

function ModelOption({
  id,
  active,
  recommended,
  onSelect,
}: {
  id: string
  active: boolean
  recommended?: boolean
  onSelect: (id: string) => void
}) {
  const { t } = useI18n()
  return (
    <button
      onClick={() => onSelect(id)}
      className={cn(
        'flex w-full items-center gap-2 rounded-[var(--radius-sm)] border px-3 py-2 text-left transition-colors',
        active ? 'border-primary bg-primary/8' : 'border-border hover:border-primary/40',
      )}
    >
      <span className="min-w-0 flex-1 truncate font-mono text-xs">{id}</span>
      {recommended ? <Badge variant="secondary">{t('setup.recommendedShort')}</Badge> : null}
      {active ? <CheckCircle className="size-4 shrink-0 text-primary" weight="fill" /> : null}
    </button>
  )
}
