import { useEffect, useMemo, useState } from 'react'
import { Lightning, PencilSimple, Plus, Spinner, TrashSimple, UsersThree, Warning } from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import {
  resolveReasoningControl,
  resolveReasoningModelTarget,
} from '@/lib/reasoning'
import { useReasoningCapability } from '@/lib/reasoningCapability'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
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
  Tabs,
  TabsList,
  TabsTrigger,
  Textarea,
} from '@/components/ui/primitives'
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SkeletonList } from '@/components/ui/skeleton'

interface Role {
  name: string
  title: string
  summary: string
  category: string
  toolset: string
  model: string
  effort?: string
  max_turns?: number
  tags?: string[]
  danger?: boolean
  subrole?: boolean
  parent?: string
  source: string
}

interface ActiveAgent {
  id: string
  role: string
  task: string
  started_at: string
}

interface Perf {
  role: string
  score: number
  missions: number
  successes: number
  kept: number
}

interface RolesResponse {
  roles: Role[]
  scope?: string[]
  active?: ActiveAgent[]
  performance?: Perf[]
  toolsets?: string[]
  categories?: string[]
}

interface ModelResolutionConfig {
  values?: {
    model?: { default?: string; provider?: string }
    providers?: Record<string, unknown>
  }
}

const CATEGORY_LABEL: Record<string, string> = {
  general: 'General',
  engineering: 'Engineering',
  research: 'Research',
  writing: 'Writing',
  security: 'Security — authorized testing',
}

// orderWithSubroles keeps the server order for top-level roles but pulls each
// subrole up directly beneath its master, so the nesting reads top-down.
function orderWithSubroles(roles: Role[]): Role[] {
  const subs = new Map<string, Role[]>()
  for (const r of roles) {
    if (r.subrole && r.parent) {
      const list = subs.get(r.parent) ?? []
      list.push(r)
      subs.set(r.parent, list)
    }
  }
  const out: Role[] = []
  for (const r of roles) {
    if (r.subrole) continue
    out.push(r)
    for (const s of subs.get(r.name) ?? []) out.push(s)
  }
  for (const r of roles) {
    if (r.subrole && !out.includes(r)) out.push(r)
  }
  return out
}

export default function RolesPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<RolesResponse>('/roles')
  const modelConfigState = useApi<ModelResolutionConfig>('/config')
  const [editing, setEditing] = useState<Role | null>(null)
  const [creating, setCreating] = useState(false)
  const [tab, setTab] = useState<'roles' | 'performance'>('roles')

  // While sub-agents are running, refresh so the panel stays live.
  useEffect(() => {
    if (!data?.active?.length) return
    const id = setInterval(reload, 2000)
    return () => clearInterval(id)
  }, [data?.active?.length, reload])

  if (loading && !data) return <SkeletonList count={4} />

  const roles = data?.roles ?? []
  const groups: { category: string; roles: Role[] }[] = []
  const index = new Map<string, number>()
  for (const r of roles) {
    if (!index.has(r.category)) {
      index.set(r.category, groups.length)
      groups.push({ category: r.category, roles: [] })
    }
    groups[index.get(r.category)!].roles.push(r)
  }

  const closeEditor = () => {
    setEditing(null)
    setCreating(false)
  }
  const onSaved = () => {
    closeEditor()
    reload()
  }

  // Sticky tab bar mirroring the Providers page: [Roles] and [Performance].
  // The tab strip + (on the Roles tab) the New-role button stay pinned; only
  // the content below scrolls.
  const header = (
    <div className="flex items-center gap-2">
      <Tabs value={tab} onValueChange={(v) => setTab(v as 'roles' | 'performance')}>
        <TabsList>
          <TabsTrigger value="roles" className="gap-1.5">
            <UsersThree className="size-3.5" /> {t('roles.title')}
          </TabsTrigger>
          <TabsTrigger value="performance" className="gap-1.5">
            <Lightning className="size-3.5" /> {t('roles.performance')}
          </TabsTrigger>
        </TabsList>
      </Tabs>
      {tab === 'roles' ? (
        <Button size="sm" className="ml-auto shrink-0 gap-1.5" onClick={() => setCreating(true)}>
          <Plus className="size-4" />
          {t('roles.new')}
        </Button>
      ) : null}
    </div>
  )

  return (
    <PageLayout header={header}>
      {tab === 'performance' ? (
        <PerformanceView performance={data?.performance ?? []} />
      ) : (
        <>
          {data?.active?.length ? (
            <Card className="border-primary/40">
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <Spinner className="size-4 animate-spin text-primary" />
                  {t('roles.working', { n: data.active.length })}
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                {data.active.map((a) => (
                  <div key={a.id} className="flex items-start gap-2 text-xs">
                    <Badge variant="secondary" className="shrink-0">
                      {a.role}
                    </Badge>
                    <span className="min-w-0 flex-1 truncate text-muted-foreground">{a.task}</span>
                  </div>
                ))}
              </CardContent>
            </Card>
          ) : null}

          {/* Role grid, grouped by category, subroles nested under their master. */}
          {groups.map((g) => (
            <div key={g.category} className="space-y-2">
              <h2 className="text-sm font-semibold">{CATEGORY_LABEL[g.category] ?? g.category}</h2>
              <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
                {orderWithSubroles(g.roles).map((r) => {
                  const editable = r.source === 'local'
                  return (
                    <button
                      key={r.name}
                      type="button"
                      onClick={() => setEditing(r)}
                      className={cn(
                        'group rounded-[var(--radius-lg)] border border-border bg-card p-3.5 text-left transition-colors hover:border-primary/40',
                        r.subrole && 'border-l-2 border-l-border bg-muted/20',
                      )}
                    >
                      <div className="flex items-start justify-between gap-2">
                        <span className="min-w-0 truncate text-sm font-medium">{r.title}</span>
                        {editable ? (
                          <PencilSimple className="size-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
                        ) : null}
                      </div>
                      <code className="mt-0.5 inline-block rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                        {r.name}
                      </code>
                      <p className="mt-1.5 line-clamp-2 text-xs text-muted-foreground">{r.summary}</p>
                      <div className="mt-2 flex flex-wrap gap-1.5">
                        {editable ? (
                          <Badge variant="secondary">{t('roles.custom')}</Badge>
                        ) : (
                          <Badge variant="outline">{t('roles.builtin')}</Badge>
                        )}
                        {r.subrole ? (
                          <Badge variant="secondary">
                            {t('roles.specialistOf', { master: r.parent ?? '' })}
                          </Badge>
                        ) : null}
                        {r.danger ? (
                          <Badge variant="warning">
                            <Warning className="size-3" weight="fill" />
                            {t('roles.authorized')}
                          </Badge>
                        ) : null}
                        {r.toolset ? (
                          <Badge variant="outline">{t('roles.tools', { set: r.toolset })}</Badge>
                        ) : null}
                        {r.model ? <Badge variant="outline">{r.model}</Badge> : null}
                      </div>
                    </button>
                  )
                })}
              </div>
            </div>
          ))}
        </>
      )}

      {(editing || creating) && (
        <RoleEditor
          role={editing}
          toolsets={data?.toolsets ?? []}
          categories={data?.categories ?? Object.keys(CATEGORY_LABEL)}
          modelConfig={modelConfigState.data}
          onClose={closeEditor}
          onSaved={onSaved}
        />
      )}
    </PageLayout>
  )
}

// PerformanceView is the "how the team performs" panel, now its own tab.
function PerformanceView({ performance }: { performance: Perf[] }) {
  const { t } = useI18n()
  if (performance.length === 0) {
    return (
      <Card>
        <CardContent className="pt-4 text-xs text-muted-foreground">
          {t('roles.performanceDesc')}
        </CardContent>
      </Card>
    )
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Lightning className="size-4 text-primary" weight="fill" />
          {t('roles.performance')}
        </CardTitle>
        <CardDescription>{t('roles.performanceDesc')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-1.5">
        {performance.map((p) => (
          <div key={p.role} className="flex items-center gap-3 text-xs">
            <span className="w-40 shrink-0 truncate font-medium">{p.role}</span>
            <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary"
                style={{ width: `${Math.max(2, Math.min(100, p.score))}%` }}
              />
            </div>
            <span className="w-10 shrink-0 text-right tabular-nums text-muted-foreground">
              {p.score.toFixed(0)}
            </span>
            <span className="w-16 shrink-0 text-right text-[10px] text-muted-foreground">
              {t('roles.missions', { n: p.missions })}
            </span>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

// ---- editor -----------------------------------------------------------------

type Draft = {
  name: string
  title: string
  summary: string
  category: string
  toolset: string
  model: string
  effort: string
  danger: boolean
  subrole: boolean
  parent: string
  body: string
}

const EMPTY: Draft = {
  name: '',
  title: '',
  summary: '',
  category: 'general',
  toolset: '',
  model: '',
  effort: '',
  danger: false,
  subrole: false,
  parent: '',
  body: '',
}

/** Assemble the raw .md (front matter + body) from a draft, for the Raw tab. */
function toRaw(d: Draft): string {
  const fm: string[] = [`name: ${d.name}`, `title: ${d.title}`]
  if (d.summary) fm.push(`summary: ${d.summary}`)
  if (d.category) fm.push(`category: ${d.category}`)
  if (d.toolset) fm.push(`toolset: ${d.toolset}`)
  if (d.model) fm.push(`model: ${d.model}`)
  if (d.effort) fm.push(`effort: ${d.effort}`)
  if (d.danger) fm.push('danger: true')
  if (d.subrole) fm.push('subrole: true')
  if (d.parent) fm.push(`parent: ${d.parent}`)
  return `---\n${fm.join('\n')}\n---\n\n${d.body.trim()}\n`
}

function RoleEditor({
  role,
  toolsets,
  categories,
  modelConfig,
  onClose,
  onSaved,
}: {
  role: Role | null
  toolsets: string[]
  categories: string[]
  modelConfig?: ModelResolutionConfig
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const readOnly = !!role && role.source !== 'local'
  const isNew = !role

  const [d, setD] = useState<Draft>(() =>
    role
      ? {
          name: role.name,
          title: role.title,
          summary: role.summary,
          category: role.category || 'general',
          toolset: role.toolset || '',
          model: role.model || '',
          effort: role.effort || '',
          danger: !!role.danger,
          subrole: !!role.subrole,
          parent: role.parent || '',
          body: '', // fetched below (list endpoint omits the prompt body)
        }
      : EMPTY,
  )
  const [tab, setTab] = useState<'form' | 'raw'>('form')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((p) => ({ ...p, [k]: v }))

  // The list omits the prompt body; fetch the full role so editing preserves it.
  useEffect(() => {
    if (!role) return
    let cancelled = false
    get<{ body?: string }>(`/roles/${encodeURIComponent(role.name)}`)
      .then((full) => {
        if (!cancelled && typeof full.body === 'string') set('body', full.body)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [role])

  const raw = useMemo(() => toRaw(d), [d])
  const reasoningTarget = useMemo(
    () => {
      const values = modelConfig?.values
      if (!values) return undefined
      return resolveReasoningModelTarget(
        d.model,
        {
          provider: values.model?.provider ?? '',
          model: values.model?.default ?? '',
        },
        Object.keys(values.providers ?? {}),
      )
    },
    [d.model, modelConfig],
  )
  const reasoningState = useReasoningCapability(reasoningTarget)
  const effortControl = resolveReasoningControl(d.effort, reasoningState)
  const effortHint =
    effortControl.hint === 'unsupported'
      ? t('reasoning.unsupported', { value: d.effort })
      : effortControl.hint === 'loading'
        ? t('reasoning.loading')
        : effortControl.hint === 'unavailable'
          ? t('reasoning.unavailable')
          : effortControl.hint === 'mandatory'
            ? t('reasoning.mandatory')
            : effortControl.hint === 'provider-controlled'
              ? t('reasoning.providerControlled')
              : t('reasoning.autoHint')

  const save = async () => {
    setBusy(true)
    setErr(undefined)
    try {
      await post('/roles', {
        name: d.name,
        title: d.title,
        summary: d.summary,
        category: d.category,
        toolset: d.toolset,
        model: d.model,
        effort: d.effort,
        danger: d.danger,
        subrole: d.subrole,
        parent: d.subrole ? d.parent : '',
        body: d.body,
      })
      onSaved()
    } catch (e) {
      setErr((e as Error).message)
      setBusy(false)
    }
  }

  const remove = async () => {
    if (!role) return
    setBusy(true)
    setErr(undefined)
    try {
      await del(`/roles/${encodeURIComponent(role.name)}`)
      onSaved()
    } catch (e) {
      setErr((e as Error).message)
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : null)}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {isNew ? t('roles.new') : readOnly ? role!.title : t('roles.editRole', { name: role!.title })}
          </DialogTitle>
        </DialogHeader>

        {!readOnly ? (
          <div className="px-6 pt-1">
            <Tabs value={tab} onValueChange={(v) => setTab(v as 'form' | 'raw')}>
              <TabsList>
                <TabsTrigger value="form">{t('roles.tabForm')}</TabsTrigger>
                <TabsTrigger value="raw">{t('roles.tabRaw')}</TabsTrigger>
              </TabsList>
            </Tabs>
          </div>
        ) : null}

        <DialogBody className="space-y-3.5">
          {readOnly ? (
            <>
              <div className="rounded-[var(--radius-sm)] border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
                {t('roles.builtinReadonly')}
              </div>
              <ReadOnlyView role={role!} />
            </>
          ) : tab === 'raw' ? (
            <div className="space-y-1.5">
              <Label>{t('roles.rawLabel')}</Label>
              <Textarea
                readOnly
                value={raw}
                rows={16}
                className="font-mono text-[11px] leading-relaxed"
              />
              <p className="text-[11px] text-muted-foreground">{t('roles.rawHint')}</p>
            </div>
          ) : (
            <>
              <div className="grid grid-cols-2 gap-3">
                <Field label={t('roles.fName')} hint={isNew ? undefined : t('roles.nameLocked')}>
                  <Input
                    value={d.name}
                    disabled={!isNew}
                    placeholder="my-role"
                    onChange={(e) => set('name', e.target.value)}
                  />
                </Field>
                <Field label={t('roles.fTitle')}>
                  <Input value={d.title} onChange={(e) => set('title', e.target.value)} />
                </Field>
              </div>

              <Field label={t('roles.fSummary')}>
                <Input value={d.summary} onChange={(e) => set('summary', e.target.value)} />
              </Field>

              <div className="grid grid-cols-2 gap-3">
                <Field label={t('roles.fCategory')}>
                  <NativeSelect value={d.category} onChange={(v) => set('category', v)}>
                    {categories.map((c) => (
                      <option key={c} value={c}>
                        {CATEGORY_LABEL[c] ?? c}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label={t('roles.fToolset')}>
                  <NativeSelect value={d.toolset} onChange={(v) => set('toolset', v)}>
                    <option value="">{t('roles.toolsetInherit')}</option>
                    {toolsets.map((ts) => (
                      <option key={ts} value={ts}>
                        {ts}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <Field label={t('roles.fModel')} hint={t('roles.modelInherit')}>
                  <Input
                    value={d.model}
                    placeholder={t('roles.modelInherit')}
                    onChange={(e) => set('model', e.target.value)}
                  />
                </Field>
                <Field label={t('roles.fEffort')} hint={effortHint}>
                  <NativeSelect value={d.effort} onChange={(v) => set('effort', v)}>
                    {effortControl.unsupported ? (
                      <option value={d.effort} disabled>
                        {t('reasoning.unsupported', { value: d.effort })}
                      </option>
                    ) : null}
                    {effortControl.options.map((option) => (
                      <option key={option.value || 'auto'} value={option.value}>
                        {option.value === '' ? t('reasoning.auto') : option.label}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
              </div>

              <div className="flex items-center justify-between rounded-[var(--radius-sm)] border border-border px-3 py-2">
                <div>
                  <p className="text-xs font-medium">{t('roles.fDanger')}</p>
                  <p className="text-[11px] text-muted-foreground">{t('roles.dangerHint')}</p>
                </div>
                <Switch checked={d.danger} onCheckedChange={(v) => set('danger', v)} />
              </div>

              <div className="rounded-[var(--radius-sm)] border border-border px-3 py-2">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-xs font-medium">{t('roles.fSubrole')}</p>
                    <p className="text-[11px] text-muted-foreground">{t('roles.subroleHint')}</p>
                  </div>
                  <Switch checked={d.subrole} onCheckedChange={(v) => set('subrole', v)} />
                </div>
                {d.subrole ? (
                  <div className="mt-2">
                    <Input
                      value={d.parent}
                      placeholder={t('roles.parentPlaceholder')}
                      onChange={(e) => set('parent', e.target.value)}
                    />
                  </div>
                ) : null}
              </div>

              <Field label={t('roles.fPrompt')} hint={t('roles.promptHint')}>
                <Textarea
                  value={d.body}
                  rows={10}
                  placeholder={t('roles.promptPlaceholder')}
                  onChange={(e) => set('body', e.target.value)}
                  className="text-[13px] leading-relaxed"
                />
              </Field>
            </>
          )}

          {err ? <p className="text-xs text-destructive">{err}</p> : null}
        </DialogBody>

        <DialogFooter className="flex items-center">
          {!readOnly && !isNew ? (
            <Button
              variant="ghost"
              size="sm"
              disabled={busy}
              onClick={remove}
              className="mr-auto gap-1.5 text-destructive hover:text-destructive"
            >
              <TrashSimple className="size-4" />
              {t('common.delete')}
            </Button>
          ) : null}
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          {!readOnly ? (
            <Button size="sm" disabled={busy || !d.name.trim() || !d.title.trim()} onClick={save}>
              {t('common.save')}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// A builtin role can be inspected but not changed; show its facts plainly.
function ReadOnlyView({ role }: { role: Role }) {
  const { t } = useI18n()
  const row = (label: string, value?: string) =>
    value ? (
      <div className="flex gap-2 text-xs">
        <span className="w-24 shrink-0 text-muted-foreground">{label}</span>
        <span className="min-w-0 flex-1 break-words">{value}</span>
      </div>
    ) : null
  return (
    <div className="space-y-1.5">
      {row(t('roles.fName'), role.name)}
      {row(t('roles.fSummary'), role.summary)}
      {row(t('roles.fCategory'), CATEGORY_LABEL[role.category] ?? role.category)}
      {row(t('roles.fToolset'), role.toolset)}
      {row(t('roles.fModel'), role.model)}
      {role.subrole ? row(t('roles.fSubrole'), t('roles.specialistOf', { master: role.parent ?? '' })) : null}
    </div>
  )
}

function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      {children}
      {hint ? <p className="text-[11px] text-muted-foreground">{hint}</p> : null}
    </div>
  )
}

// A plain native <select> styled to match the Input, avoiding a new dependency.
function NativeSelect({
  value,
  onChange,
  children,
}: {
  value: string
  onChange: (v: string) => void
  children: React.ReactNode
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="h-9 w-full rounded-[var(--radius-sm)] border border-border bg-card px-2 text-sm outline-none transition-colors focus-visible:border-ring"
    >
      {children}
    </select>
  )
}
