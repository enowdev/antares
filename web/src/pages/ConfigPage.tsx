import { useEffect, useMemo, useState } from 'react'
import {
  ArrowSquareOut,
  CaretDown,
  CheckCircle,
  Eye,
  EyeSlash,
  FloppyDisk,
  GoogleLogo,
  Lock,
  MagnifyingGlass,
  Sparkle,
  Warning,
} from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import type { ReasoningCapability } from '@/lib/models'
import { reasoningOptions } from '@/lib/reasoning'
import { cn } from '@/lib/utils'
import { usePageActions } from '@/components/layout/PageChrome'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  EmptyState,
  Input,
  Label,
  Switch,
  Textarea,
} from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'

type Tier = 'essential' | 'common' | 'advanced'

interface Field {
  path: string
  label: string
  group: string
  type: 'string' | 'number' | 'boolean' | 'string[]' | 'object'
  tier: Tier
  default: unknown
  secret: boolean
  enum?: string[]
  options_source?: string
  help?: string
}

interface ConfigResponse {
  values: Record<string, unknown>
  schema: Field[]
}

interface ReasoningModelsResponse {
  active: { model: string; provider: string }
  models: Array<{
    id: string
    provider: string
    reasoning_capability?: ReasoningCapability
  }>
}

const ESSENTIALS = '__essentials'
const YAML = '__yaml'

function readPath(obj: Record<string, unknown>, path: string): unknown {
  return path.split('.').reduce<unknown>((acc, key) => {
    if (acc && typeof acc === 'object') return (acc as Record<string, unknown>)[key]
    return undefined
  }, obj)
}

const ACRONYMS: Record<string, string> = {
  rag: 'RAG',
  mcp: 'MCP',
  yaml: 'YAML',
  dsn: 'DSN',
  api: 'API',
}

/** "prompt_caching" → "Prompt caching", "rag" → "RAG" */
function humanizeGroup(name: string): string {
  return name
    .split('_')
    .map((w, i) => ACRONYMS[w] ?? (i === 0 ? w.charAt(0).toUpperCase() + w.slice(1) : w))
    .join(' ')
}

export default function ConfigPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<ConfigResponse>('/config')
  const rawState = useApi<{ yaml: string }>('/config/raw')
  const modelsState = useApi<ReasoningModelsResponse>('/model/list-all')

  const [edits, setEdits] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string>()
  const [filter, setFilter] = useState('')
  const [revealed, setRevealed] = useState<Record<string, boolean>>({})
  const [yamlDraft, setYamlDraft] = useState<string | null>(null)
  const [section, setSection] = useState<string>(ESSENTIALS)
  const [showAdvanced, setShowAdvanced] = useState(false)

  const configuredProvider = String(
    edits['model.provider'] ??
      (data ? readPath(data.values, 'model.provider') : '') ??
      '',
  )
  const configuredModel = String(
    edits['model.default'] ??
      (data ? readPath(data.values, 'model.default') : '') ??
      '',
  )
  const reasoningCapability = useMemo(
    () =>
      modelsState.data?.models.find(
        (model) =>
          model.provider === configuredProvider && model.id === configuredModel,
      )?.reasoning_capability,
    [configuredModel, configuredProvider, modelsState.data],
  )

  const query = filter.trim().toLowerCase()
  const searching = query.length > 0
  const dirty = Object.keys(edits).length

  const fields = useMemo(() => (data?.schema ?? []).filter((f) => f.type !== 'object'), [data])

  const groups = useMemo(() => {
    const seen: string[] = []
    for (const f of fields) if (!seen.includes(f.group)) seen.push(f.group)
    return seen
  }, [fields])

  // Searching spans every group and tier so nothing hides behind disclosure.
  const results = useMemo(
    () =>
      fields.filter(
        (f) =>
          !query || f.path.toLowerCase().includes(query) || f.label.toLowerCase().includes(query),
      ),
    [fields, query],
  )

  const sectionFields = useMemo(() => {
    if (section === ESSENTIALS) return fields.filter((f) => f.tier === 'essential')
    return fields.filter((f) => f.group === section)
  }, [fields, section])

  const visible = sectionFields.filter((f) => showAdvanced || f.tier !== 'advanced')
  const hiddenCount = sectionFields.length - visible.length

  const dirtyPerGroup = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const f of fields) if (f.path in edits) counts[f.group] = (counts[f.group] ?? 0) + 1
    return counts
  }, [fields, edits])

  const dirtyEssentials = useMemo(
    () => fields.filter((f) => f.tier === 'essential' && f.path in edits).length,
    [fields, edits],
  )

  // Reset disclosure when moving between sections.
  useEffect(() => setShowAdvanced(false), [section])

  const valueOf = (f: Field): unknown => {
    if (f.path in edits) return edits[f.path]
    const v = data ? readPath(data.values, f.path) : undefined
    return v === undefined ? f.default : v
  }

  const setValue = (path: string, v: unknown) => {
    setEdits((prev) => ({ ...prev, [path]: v }))
    setSaved(false)
  }

  const save = async () => {
    if (!dirty) return
    setSaving(true)
    setError(undefined)
    try {
      await post('/config', { updates: edits })
      setEdits({})
      setSaved(true)
      reload()
      setTimeout(() => setSaved(false), 2500)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const saveYaml = async () => {
    if (yamlDraft === null) return
    setSaving(true)
    setError(undefined)
    try {
      await post('/config/raw', { yaml: yamlDraft })
      setYamlDraft(null)
      reload()
      rawState.reload()
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  usePageActions(
    <Button size="sm" onClick={save} loading={saving} disabled={!dirty} className="gap-1.5">
      {saved ? <CheckCircle className="size-4" weight="fill" /> : <FloppyDisk className="size-4" />}
      {saved ? t('common.saved') : dirty ? t('config.saveN', { n: dirty }) : t('common.save')}
    </Button>,
    // `edits` (not just its count `dirty`) must be a dependency: `save` closes
    // over the edits map, so without this the header button keeps a stale
    // closure while you edit ONE field — dirty stays 1, the button is never
    // re-registered, and Save keeps posting only the first keystroke. That was
    // the "only one character saves per save" bug.
    [edits, dirty, saving, saved, t],
  )

  const renderRows = (list: Field[], withGroup = false) => (
    <Card>
      <CardContent className="divide-y divide-border p-0">
        {list.map((f) => (
          <FieldRow
            key={f.path}
            field={f}
            showGroup={withGroup}
            value={valueOf(f)}
            reasoningCapability={reasoningCapability}
            dirty={f.path in edits}
            revealed={!!revealed[f.path]}
            onReveal={() => setRevealed((r) => ({ ...r, [f.path]: !r[f.path] }))}
            onChange={(v) => setValue(f.path, v)}
          />
        ))}
      </CardContent>
    </Card>
  )

  return (
    <div className="flex flex-col gap-5 lg:min-h-0 lg:flex-1">
      {error ? (
        <div className="flex items-start gap-2 rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive">
          <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
          <span className="min-w-0 break-words">{error}</span>
        </div>
      ) : null}

      <div className="relative lg:shrink-0">
        <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={t('config.searchPlaceholder')}
          className="pl-9"
        />
      </div>

      {loading && !data ? (
        <div className="grid gap-5 lg:grid-cols-[13rem_1fr]">
          <Skeleton className="hidden h-80 w-full rounded-[var(--radius-lg)] lg:block" />
          <SkeletonList count={5} />
        </div>
      ) : !data ? (
        <EmptyState title={t('config.loadFailed')} />
      ) : searching ? (
        // Search replaces the layout entirely: one flat list, group shown per row.
        <div className="space-y-3 lg:min-h-0 lg:flex-1 lg:overflow-y-auto lg:pr-1">
          <p className="text-xs text-muted-foreground">{t('config.matches', { n: results.length })}</p>
          {results.length === 0 ? (
            <EmptyState title={t('config.noMatch')} />
          ) : (
            renderRows(results, true)
          )}
        </div>
      ) : (
        <div className="grid gap-5 lg:min-h-0 lg:flex-1 lg:grid-cols-[13rem_1fr] lg:overflow-hidden">
          <SectionRail
            groups={groups}
            section={section}
            onSelect={setSection}
            dirtyPerGroup={dirtyPerGroup}
            dirtyEssentials={dirtyEssentials}
          />

          <div className="min-w-0 space-y-4 lg:h-full lg:overflow-y-auto lg:pb-6 lg:pr-1">
            {section === YAML ? (
              <Card>
                <CardHeader>
                  <CardTitle>{t('config.editDirect')}</CardTitle>
                  <CardDescription>{t('config.editDirectDesc')}</CardDescription>
                </CardHeader>
                <CardContent className="space-y-3">
                  {rawState.loading ? (
                    <Skeleton className="h-[55dvh] w-full" />
                  ) : (
                    <Textarea
                      value={yamlDraft ?? rawState.data?.yaml ?? ''}
                      onChange={(e) => setYamlDraft(e.target.value)}
                      spellCheck={false}
                      className="h-[55dvh] font-mono text-xs"
                    />
                  )}
                  <Button size="sm" onClick={saveYaml} loading={saving} disabled={yamlDraft === null}>
                    {t('config.saveYaml')}
                  </Button>
                </CardContent>
              </Card>
            ) : (
              <>
                {section === ESSENTIALS ? (
                  <p className="text-xs leading-relaxed text-muted-foreground sm:text-sm">
                    {t('config.essentialsHint')}
                  </p>
                ) : null}

                {section === 'osint' ? (
                  <GoogleOsintCard cookieEdited={edits['osint.google_cookie'] as string | undefined} />
                ) : null}

                {section === 'server' || section === ESSENTIALS ? (
                  <DashboardPasswordCard />
                ) : null}

                {visible.length === 0 ? (
                  <EmptyState title={t('config.nothingHere')} />
                ) : (
                  renderRows(visible, section === ESSENTIALS)
                )}

                {hiddenCount > 0 ? (
                  <button
                    onClick={() => setShowAdvanced(true)}
                    className="flex w-full items-center justify-center gap-1.5 rounded-[var(--radius-sm)] border border-dashed border-border py-2.5 text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-foreground"
                  >
                    <CaretDown className="size-3.5" />
                    {t('config.showAdvanced', { n: hiddenCount })}
                  </button>
                ) : showAdvanced && sectionFields.some((f) => f.tier === 'advanced') ? (
                  <button
                    onClick={() => setShowAdvanced(false)}
                    className="flex w-full items-center justify-center gap-1.5 rounded-[var(--radius-sm)] border border-dashed border-border py-2.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
                  >
                    <CaretDown className="size-3.5 rotate-180" />
                    {t('config.hideAdvanced')}
                  </button>
                ) : null}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function SectionRail({
  groups,
  section,
  onSelect,
  dirtyPerGroup,
  dirtyEssentials,
}: {
  groups: string[]
  section: string
  onSelect: (s: string) => void
  dirtyPerGroup: Record<string, number>
  dirtyEssentials: number
}) {
  const { t } = useI18n()

  const dot = (n: number) =>
    n > 0 ? <span className="size-1.5 shrink-0 rounded-full bg-primary" /> : null

  const item = (id: string, label: string, badge?: React.ReactNode, icon?: React.ReactNode) => (
    <button
      key={id}
      onClick={() => onSelect(id)}
      className={cn(
        'flex w-full items-center gap-2 rounded-[var(--radius-sm)] px-3 py-2 text-left text-sm transition-colors',
        section === id
          ? 'bg-primary/12 font-medium text-primary'
          : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
      )}
    >
      {icon}
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {badge}
    </button>
  )

  return (
    <>
      <nav className="hidden lg:block lg:h-full lg:overflow-y-auto lg:pb-6 lg:pr-1">
        <div className="space-y-0.5">
          {item(
            ESSENTIALS,
            t('config.essentials'),
            dot(dirtyEssentials),
            <Sparkle
              className="size-4 shrink-0"
              weight={section === ESSENTIALS ? 'fill' : 'regular'}
            />,
          )}
          <div className="my-1.5 h-px bg-border" />
          {groups.map((g) => item(g, humanizeGroup(g), dot(dirtyPerGroup[g] ?? 0)))}
          <div className="my-1.5 h-px bg-border" />
          {item(YAML, t('config.yamlSection'))}
        </div>
      </nav>

      <div className="lg:hidden">
        <select
          value={section}
          onChange={(e) => onSelect(e.target.value)}
          className="h-10 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm"
          aria-label={t('config.title')}
        >
          <option value={ESSENTIALS}>{t('config.essentials')}</option>
          {groups.map((g) => (
            <option key={g} value={g}>
              {humanizeGroup(g)}
              {dirtyPerGroup[g] ? ' •' : ''}
            </option>
          ))}
          <option value={YAML}>{t('config.yamlSection')}</option>
        </select>
      </div>
    </>
  )
}

function FieldRow({
  field,
  value,
  reasoningCapability,
  dirty,
  revealed,
  showGroup,
  onReveal,
  onChange,
}: {
  field: Field
  value: unknown
  reasoningCapability?: ReasoningCapability
  dirty: boolean
  revealed: boolean
  showGroup?: boolean
  onReveal: () => void
  onChange: (v: unknown) => void
}) {
  const { t } = useI18n()

  // Booleans read best as one compact row with the switch on the right.
  if (field.type === 'boolean') {
    return (
      <div className="flex items-center gap-4 px-4 py-3 sm:px-5">
        <div className="min-w-0 flex-1">
          <FieldLabel field={field} dirty={dirty} showGroup={showGroup} />
          {field.help ? (
            <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">{field.help}</p>
          ) : null}
        </div>
        <Switch checked={!!value} onCheckedChange={onChange} />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-start sm:gap-4 sm:px-5">
      <div className="min-w-0 sm:w-[42%] sm:pt-1.5">
        <FieldLabel field={field} dirty={dirty} showGroup={showGroup} />
        {field.help ? (
          <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">{field.help}</p>
        ) : null}
      </div>

      <div className="min-w-0 sm:flex-1">
        {field.options_source === 'reasoning_capability' ? (
          <ReasoningSelect
            value={String(value ?? '')}
            capability={reasoningCapability}
            onChange={onChange}
          />
        ) : field.enum ? (
          <select
            value={String(value ?? '')}
            onChange={(e) => onChange(e.target.value)}
            className="h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm"
          >
            {field.enum.map((opt) => (
              <option key={opt} value={opt}>
                {opt}
              </option>
            ))}
          </select>
        ) : field.type === 'string[]' ? (
          <ListInput value={value} onChange={onChange} placeholder={t('config.commaSeparated')} />
        ) : field.secret ? (
          <div className="flex gap-2">
            <Input
              type={revealed ? 'text' : 'password'}
              value={String(value ?? '')}
              onChange={(e) => onChange(e.target.value)}
              placeholder={t('common.notSet')}
              autoComplete="off"
            />
            <Button variant="outline" size="icon" onClick={onReveal} aria-label={t('config.reveal')}>
              {revealed ? <EyeSlash className="size-4" /> : <Eye className="size-4" />}
            </Button>
          </div>
        ) : (
          <Input
            type={field.type === 'number' ? 'number' : 'text'}
            value={String(value ?? '')}
            step="any"
            onChange={(e) =>
              onChange(field.type === 'number' ? Number(e.target.value) : e.target.value)
            }
          />
        )}
      </div>
    </div>
  )
}

function ReasoningSelect({
  value,
  capability,
  onChange,
}: {
  value: string
  capability?: ReasoningCapability
  onChange: (value: string) => void
}) {
  const { t } = useI18n()
  const options = reasoningOptions(capability)
  const unsupported =
    value !== '' && !options.some((option) => option.value === value)
  const hint = unsupported
    ? t('reasoning.unsupported', { value })
    : capability?.mandatory
      ? t('reasoning.mandatory')
      : capability
        ? t('reasoning.autoHint')
        : t('reasoning.providerControlled')

  return (
    <div className="space-y-1">
      <select
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm"
      >
        {unsupported ? (
          <option value={value} disabled>
            {t('reasoning.unsupported', { value })}
          </option>
        ) : null}
        {options.map((option) => (
          <option key={option.value || 'auto'} value={option.value}>
            {option.value === '' ? t('reasoning.auto') : option.label}
          </option>
        ))}
      </select>
      <p className="text-[11px] leading-relaxed text-muted-foreground">{hint}</p>
    </div>
  )
}

/**
 * A comma-separated list editor. Keeps the RAW text you type as its own state
 * so typing is never interrupted — the previous version reparsed to an array on
 * every keystroke and re-joined it, which silently dropped commas, trailing
 * spaces, and in-progress entries (the "only the first letter saves" bug).
 * Parsing to string[] happens on change (for the draft) but the field shows
 * exactly what you typed; a blur normalises the display.
 */
function ListInput({
  value,
  onChange,
  placeholder,
}: {
  value: unknown
  onChange: (v: string[]) => void
  placeholder?: string
}) {
  const joined = Array.isArray(value) ? (value as string[]).join(', ') : ''
  const [text, setText] = useState(joined)

  // Resync from outside only when the committed value truly differs from what
  // the raw text parses to (e.g. after Save/reload), not on every keystroke.
  useEffect(() => {
    const parsed = text.split(',').map((s) => s.trim()).filter(Boolean)
    if (parsed.join(' ') !== (Array.isArray(value) ? (value as string[]).join(' ') : '')) {
      setText(joined)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [joined])

  return (
    <Input
      value={text}
      placeholder={placeholder}
      onChange={(e) => {
        setText(e.target.value)
        onChange(e.target.value.split(',').map((s) => s.trim()).filter(Boolean))
      }}
      onBlur={() => setText((t) => t.split(',').map((s) => s.trim()).filter(Boolean).join(', '))}
    />
  )
}

function FieldLabel({
  field,
  dirty,
  showGroup,
}: {
  field: Field
  dirty: boolean
  showGroup?: boolean
}) {
  const { t } = useI18n()
  return (
    <>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <Label className={cn('text-sm', dirty && 'text-primary')}>{field.label}</Label>
        {showGroup ? <Badge variant="outline">{humanizeGroup(field.group)}</Badge> : null}
        {dirty ? <Badge>{t('config.changed')}</Badge> : null}
      </div>
      <p className="truncate font-mono text-[10px] text-muted-foreground/70">{field.path}</p>
    </>
  )
}

interface GoogleAccount {
  authuser: number
  email: string
  name: string
}

/**
 * Google OSINT status + tutorial, shown atop the OSINT settings group. Lets the
 * user verify the pasted cookie really connects to a Google account (name +
 * email) or see that it's expired, and explains how to obtain the cookie with
 * the Cookie-Editor extension.
 */
function GoogleOsintCard({ cookieEdited }: { cookieEdited?: string }) {
  const { t } = useI18n()
  const [busy, setBusy] = useState(false)
  const [selecting, setSelecting] = useState<number | null>(null)
  const [selected, setSelected] = useState<number | null>(null)
  const [result, setResult] = useState<{
    connected: boolean
    accounts?: GoogleAccount[]
    selected?: number
    error?: string
  } | null>(null)

  const verify = async () => {
    setBusy(true)
    setResult(null)
    try {
      // Test the just-typed (unsaved) cookie when present, else the stored one.
      const r = await post<{
        connected: boolean
        accounts?: GoogleAccount[]
        selected?: number
        error?: string
      }>('/osint/google/verify', cookieEdited != null ? { cookie: cookieEdited } : {})
      setResult(r)
      if (r.selected != null) setSelected(r.selected)
    } catch (e) {
      setResult({ connected: false, error: (e as Error).message })
    } finally {
      setBusy(false)
    }
  }

  // Persist which account lookups act as (the /u/<N>/ index).
  const choose = async (authuser: number) => {
    setSelecting(authuser)
    try {
      await post('/osint/google/select', { authuser })
      setSelected(authuser)
    } catch {
      /* leave the previous selection intact on failure */
    } finally {
      setSelecting(null)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <GoogleLogo className="size-4 text-primary" weight="fill" />
          {t('osintg.title')}
        </CardTitle>
        <CardDescription>{t('osintg.desc')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="outline" onClick={verify} loading={busy} className="gap-1.5">
            <CheckCircle className="size-4" />
            {t('osintg.verify')}
          </Button>
          {result?.connected && result.accounts?.length ? (
            <Badge variant="success">
              <CheckCircle className="size-3" weight="fill" />
              {t('osintg.connected')}
            </Badge>
          ) : result && !result.connected ? (
            <Badge variant="destructive">
              <Warning className="size-3" weight="fill" />
              {t('osintg.notConnected')}
            </Badge>
          ) : null}
        </div>

        {result?.connected && result.accounts?.length ? (
          <div className="space-y-2">
            {result.accounts.length > 1 ? (
              <p className="text-[11px] text-muted-foreground">{t('osintg.pick')}</p>
            ) : null}
            <div className="space-y-1">
              {result.accounts.map((a) => {
                const active = selected === a.authuser
                return (
                  <button
                    key={a.authuser}
                    onClick={() => choose(a.authuser)}
                    disabled={selecting !== null}
                    className={cn(
                      'flex w-full items-center gap-2 rounded-[var(--radius-sm)] border px-3 py-2 text-left text-xs transition-colors',
                      active
                        ? 'border-[var(--success)]/50 bg-[color-mix(in_oklch,var(--success)_10%,transparent)]'
                        : 'border-border hover:border-primary/40 hover:bg-accent',
                    )}
                  >
                    <span
                      className={cn(
                        'flex size-4 shrink-0 items-center justify-center rounded-full border',
                        active ? 'border-[var(--success)] bg-[var(--success)] text-white' : 'border-muted-foreground/40',
                      )}
                    >
                      {active ? <CheckCircle className="size-3" weight="fill" /> : null}
                    </span>
                    <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2">
                      {a.name ? <span className="font-medium">{a.name}</span> : null}
                      <span className="truncate font-mono text-muted-foreground">{a.email}</span>
                    </div>
                    {active ? (
                      <Badge variant="success" className="shrink-0">
                        {t('osintg.active')}
                      </Badge>
                    ) : null}
                  </button>
                )
              })}
            </div>
          </div>
        ) : null}
        {result && !result.connected ? (
          <p className="text-xs text-destructive">{result.error || t('osintg.notConnected')}</p>
        ) : null}

        <div className="rounded-[var(--radius-sm)] bg-muted/40 p-3 text-[11px] leading-relaxed text-muted-foreground">
          <p className="mb-1 font-medium text-foreground">{t('osintg.howto')}</p>
          <ol className="list-decimal space-y-0.5 pl-4">
            <li>
              <a
                href="https://chromewebstore.google.com/detail/cookie-editor/hlkenndednhfkekhgcdicdfddnkalmdm"
                target="_blank"
                rel="noreferrer noopener"
                className="inline-flex items-center gap-1 text-primary underline underline-offset-2"
              >
                {t('osintg.step1')}
                <ArrowSquareOut className="size-3" />
              </a>
            </li>
            <li>{t('osintg.step2')}</li>
            <li>{t('osintg.step3')}</li>
            <li>{t('osintg.step4')}</li>
          </ol>
          <p className="mt-2">{t('osintg.note')}</p>
        </div>
      </CardContent>
    </Card>
  )
}

/**
 * Set, change, or remove the dashboard password from Config — the same lock that
 * onboarding offers, reachable later. The plaintext is hashed server-side; only
 * the hash is ever stored. Changing an existing password requires the current
 * one (unless a live login session already proves it), matching the backend.
 */
function DashboardPasswordCard() {
  const { t } = useI18n()
  const status = useApi<{ password_required?: boolean }>('/auth/status')
  const locked = !!status.data?.password_required

  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [show, setShow] = useState(false)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState<string>()
  const [error, setError] = useState<string>()

  const submit = async (clear: boolean) => {
    setError(undefined)
    setDone(undefined)
    if (!clear) {
      if (!next) return setError(t('dashpw.errEmpty'))
      if (next !== confirm) return setError(t('dashpw.errMismatch'))
    }
    setBusy(true)
    try {
      await post('/auth/password', { current, password: clear ? '' : next })
      setCurrent('')
      setNext('')
      setConfirm('')
      setDone(clear ? t('dashpw.cleared') : t('dashpw.saved'))
      status.reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Lock className="size-4 text-primary" weight="fill" />
          {t('dashpw.title')}
        </CardTitle>
        <CardDescription>
          {locked ? t('dashpw.descLocked') : t('dashpw.descOpen')}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {locked ? (
          <div className="space-y-1.5">
            <Label htmlFor="dashpw-current">{t('dashpw.current')}</Label>
            <Input
              id="dashpw-current"
              type={show ? 'text' : 'password'}
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
              autoComplete="current-password"
              placeholder="••••••••"
            />
          </div>
        ) : null}

        <div className="space-y-1.5">
          <Label htmlFor="dashpw-new">{locked ? t('dashpw.new') : t('dashpw.password')}</Label>
          <div className="relative">
            <Input
              id="dashpw-new"
              type={show ? 'text' : 'password'}
              value={next}
              onChange={(e) => setNext(e.target.value)}
              autoComplete="new-password"
              placeholder="••••••••"
            />
            <button
              type="button"
              onClick={() => setShow((v) => !v)}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              aria-label={t('config.reveal')}
            >
              {show ? <EyeSlash className="size-4" /> : <Eye className="size-4" />}
            </button>
          </div>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="dashpw-confirm">{t('dashpw.confirm')}</Label>
          <Input
            id="dashpw-confirm"
            type={show ? 'text' : 'password'}
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && submit(false)}
            autoComplete="new-password"
            placeholder="••••••••"
          />
        </div>

        {error ? <p className="text-xs text-[var(--destructive)]">{error}</p> : null}
        {done ? <p className="text-xs text-[var(--success)]">{done}</p> : null}

        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={() => submit(false)} loading={busy} className="gap-1.5">
            <FloppyDisk className="size-4" />
            {locked ? t('dashpw.change') : t('dashpw.set')}
          </Button>
          {locked ? (
            <Button size="sm" variant="outline" onClick={() => submit(true)} disabled={busy}>
              {t('dashpw.remove')}
            </Button>
          ) : null}
        </div>
        <p className="text-[11px] leading-relaxed text-muted-foreground">{t('dashpw.note')}</p>
      </CardContent>
    </Card>
  )
}
