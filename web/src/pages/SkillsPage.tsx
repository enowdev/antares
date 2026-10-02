import { useEffect, useState } from 'react'
import {
  MagnifyingGlass,
  PencilSimple,
  Plus,
  ShieldCheck,
  Sparkle,
  Storefront,
  TrashSimple,
} from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { usePoll } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { usePageActions } from '@/components/layout/PageChrome'
import { Pagination } from '@/components/ui/Pagination'
import { Button } from '@/components/ui/button'
import {
  Badge,
  EmptyState,
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
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'
import { HubDialog } from '@/components/hub/HubDialog'

interface Skill {
  name: string
  description: string
  path: string
  enabled: boolean
  source: string
  read_only: boolean
  tags?: string[]
  triggers?: string[]
  updated_at: string
  usage_count: number
}

export default function SkillsPage() {
  const { t } = useI18n()
  const [filter, setFilter] = useState('')
  const [query, setQuery] = useState('')
  const endpoint = query ? `/skills?q=${encodeURIComponent(query)}` : '/skills'
  const { data, loading, reload, setData } = usePoll<{ skills: Skill[]; library?: number }>(endpoint, 5000)
  const [busy, setBusy] = useState('')
  const [toggleError, setToggleError] = useState('')
  const [browsing, setBrowsing] = useState(false)
  const [editing, setEditing] = useState<Skill | null>(null)
  const [creating, setCreating] = useState(false)
  const [tab, setTab] = useState<'everyday' | 'library'>('everyday')

  usePageActions(
    <>
      <Button size="sm" variant="outline" onClick={() => setBrowsing(true)} className="gap-1.5">
        <Storefront className="size-4" />
        {t('hub.browse')}
      </Button>
      <Button size="sm" onClick={() => setCreating(true)} className="gap-1.5">
        <Plus className="size-4" />
        {t('common.new')}
      </Button>
    </>,
    [t],
  )

  useEffect(() => {
    const id = setTimeout(() => setQuery(filter.trim()), 300)
    return () => clearTimeout(id)
  }, [filter])

  const toggle = async (name: string, enabled: boolean) => {
    setBusy(name)
    setToggleError('')
    try {
      await post('/skills/toggle', { name, enabled })
      if (data) setData({ ...data, skills: data.skills.map((s) => s.name === name ? { ...s, enabled } : s) })
      reload()
    } catch (e) {
      setToggleError(`${name}: ${(e as Error).message}`)
      reload()
    } finally {
      setBusy('')
    }
  }

  const skills = data?.skills ?? []
  const library = data?.library ?? 0

  const header = (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <Tabs value={tab} onValueChange={(v) => setTab(v as 'everyday' | 'library')}>
          <TabsList>
            <TabsTrigger value="everyday" className="gap-1.5">
              <Sparkle className="size-3.5" /> {t('skills.tabEveryday')}
            </TabsTrigger>
            {library > 0 ? (
              <TabsTrigger value="library" className="gap-1.5">
                <ShieldCheck className="size-3.5" /> {t('skills.tabLibrary', { n: library })}
              </TabsTrigger>
            ) : null}
          </TabsList>
        </Tabs>
      </div>
      {tab === 'everyday' ? (
        <div className="relative">
          <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t('skills.searchPlaceholder')}
            className="pl-9"
          />
        </div>
      ) : null}
      {toggleError ? (
        <p
          role="alert"
          className="m-rise rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-4 py-3 text-sm text-destructive"
        >
          {toggleError}
        </p>
      ) : null}
    </div>
  )

  return (
    <PageLayout header={header}>
      <HubDialog kind="skills" open={browsing} onOpenChange={setBrowsing} onInstalled={reload} />
      {(editing || creating) && (
        <SkillEditor
          skill={editing}
          onClose={() => {
            setEditing(null)
            setCreating(false)
          }}
          onSaved={() => {
            setEditing(null)
            setCreating(false)
            reload()
          }}
        />
      )}

      {tab === 'library' && library > 0 ? (
        <SecurityLibrary />
      ) : loading && !data ? (
        <SkeletonList count={5} />
      ) : skills.length === 0 ? (
        <EmptyState
          icon={<Sparkle className="size-8" />}
          title={t('skills.none')}
          description={t('skills.noneDesc')}
          action={
            <div className="flex flex-wrap justify-center gap-2">
              <Button size="sm" onClick={() => setBrowsing(true)} className="gap-1.5">
                <Storefront className="size-4" />
                {t('hub.browse')}
              </Button>
              <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
                {t('skills.compose')}
              </Button>
            </div>
          }
        />
      ) : (
        <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
          {skills.map((s) => (
            <div
              key={s.name}
              data-reveal
              className="group flex flex-col rounded-[var(--radius-lg)] border border-border bg-card p-4 transition-[border-color,background-color] duration-200 hover:border-line hover:bg-raised"
            >
              <button
                onClick={() => setEditing(s)}
                className={cn('min-w-0 flex-1 text-left transition-opacity duration-200', !s.enabled && 'opacity-55')}
              >
                <div className="flex items-start justify-between gap-2">
                  <span className="min-w-0 truncate text-[15px] font-medium tracking-[-0.2px]">{s.name}</span>
                  {!s.read_only ? (
                    <PencilSimple className="size-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
                  ) : null}
                </div>
                <p className="mt-1.5 line-clamp-2 text-xs leading-relaxed text-muted-foreground">{s.description}</p>
                <div className="mt-3 flex flex-wrap gap-1.5">
                  <Badge variant="outline" className="font-mono font-normal">{s.source}</Badge>
                  {s.read_only ? <Badge variant="secondary">{t('skills.readOnly')}</Badge> : null}
                  {s.usage_count > 0 ? (
                    <Badge variant="secondary">{t('skills.used', { n: s.usage_count })}</Badge>
                  ) : null}
                </div>
              </button>
              <div className="-mx-4 mt-4 flex items-center justify-between border-t border-border px-4 pt-3">
                <label className="flex items-center gap-2.5 font-mono text-[11px] lowercase text-muted-foreground">
                  <Switch
                    checked={s.enabled}
                    disabled={busy !== ''}
                    onCheckedChange={(v) => toggle(s.name, v)}
                    aria-label={`${t('common.enable')} ${s.name}`}
                  />
                  {s.enabled ? t('skills.on') : t('skills.off')}
                </label>
                {!s.read_only ? (
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    disabled={busy === s.name}
                    onClick={() => del(`/skills/${encodeURIComponent(s.name)}`).then(reload)}
                    aria-label={t('common.delete')}
                    className="text-muted-foreground hover:text-destructive"
                  >
                    <TrashSimple className="size-4" />
                  </Button>
                ) : null}
              </div>
            </div>
          ))}
        </div>
      )}
    </PageLayout>
  )
}

// ---- everyday skill editor (create + edit) ----------------------------------

function SkillEditor({
  skill,
  onClose,
  onSaved,
}: {
  skill: Skill | null
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const isNew = !skill
  const readOnly = !!skill?.read_only
  const [draft, setDraft] = useState({
    name: skill?.name ?? '',
    description: skill?.description ?? '',
    body: '',
  })
  const [fullSkill, setFullSkill] = useState<Skill | null>(skill)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()

  // The list omits the body. Imported skills also use the fetched metadata so
  // the viewer reflects the source as it existed when it was opened.
  useEffect(() => {
    if (!skill) return
    let cancelled = false
    get<{ skill: Skill; body: string }>(`/skills/${encodeURIComponent(skill.name)}`)
      .then((r) => {
        if (cancelled) return
        if (readOnly) {
          setFullSkill(r.skill)
          setDraft({
            name: r.skill.name,
            description: r.skill.description,
            body: r.body,
          })
        } else {
          setDraft((d) => ({ ...d, body: r.body }))
        }
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [readOnly, skill])

  const save = async () => {
    if (!draft.name.trim() || !draft.body.trim()) return
    setSaving(true)
    setError(undefined)
    try {
      await post('/skills', draft)
      onSaved()
    } catch (e) {
      setError((e as Error).message)
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : null)}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{isNew ? t('skills.compose') : skill!.name}</DialogTitle>
        </DialogHeader>

        <DialogBody className="space-y-3.5">
          {readOnly ? (
            <>
              <div className="rounded-[var(--radius-lg)] border border-border bg-raised px-3 py-2 text-xs text-muted-foreground">
                {t('skills.discoveredReadOnly')}
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="skill-name">{t('skills.name')}</Label>
                  <Input id="skill-name" readOnly value={fullSkill?.name ?? draft.name} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="skill-desc">{t('skills.whenToUse')}</Label>
                  <Input id="skill-desc" readOnly value={fullSkill?.description ?? draft.description} />
                </div>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="skill-path">{t('memory.path')}</Label>
                <Input
                  id="skill-path"
                  readOnly
                  value={fullSkill?.path ?? skill?.path ?? ''}
                  className="font-mono text-xs"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="skill-body">{t('skills.procedure')}</Label>
                <Textarea
                  id="skill-body"
                  readOnly
                  value={draft.body}
                  className="h-64 font-mono text-xs leading-relaxed"
                />
              </div>
            </>
          ) : (
            <>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="skill-name">{t('skills.name')}</Label>
                  <Input
                    id="skill-name"
                    autoFocus={isNew}
                    disabled={!isNew}
                    value={draft.name}
                    onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
                    placeholder="deploy-homeserver"
                  />
                  {!isNew ? <p className="text-[11px] text-muted-foreground">{t('skills.nameLocked')}</p> : null}
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="skill-desc">{t('skills.whenToUse')}</Label>
                  <Input
                    id="skill-desc"
                    value={draft.description}
                    onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))}
                    placeholder={t('skills.whenToUsePlaceholder')}
                  />
                </div>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="skill-body">{t('skills.procedure')}</Label>
                <Textarea
                  id="skill-body"
                  value={draft.body}
                  onChange={(e) => setDraft((d) => ({ ...d, body: e.target.value }))}
                  placeholder={t('skills.procedurePlaceholder')}
                  className="h-64 font-mono text-xs leading-relaxed"
                />
              </div>
            </>
          )}
          {error ? <p className="m-rise text-xs text-destructive">{error}</p> : null}
        </DialogBody>

        <DialogFooter className="flex items-center">
          {!readOnly && !isNew ? (
            <Button
              variant="ghost"
              size="sm"
              disabled={saving}
              onClick={() => del(`/skills/${encodeURIComponent(skill!.name)}`).then(onSaved)}
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
            <Button
              size="sm"
              onClick={save}
              loading={saving}
              disabled={!draft.name.trim() || !draft.body.trim()}
            >
              {t('common.save')}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---- security library (read-only, browse by category) -----------------------

interface LibSkill {
  name: string
  description: string
  category?: string
}

const LIB_LIMIT = 40

/**
 * The bundled security library is thousands of skills. Browse them by category,
 * a page at a time, reading each in place. Read-only; loads only when opened so
 * the everyday tab stays fast.
 */
function SecurityLibrary() {
  const { t } = useI18n()
  const [category, setCategory] = useState('')
  const [offset, setOffset] = useState(0)
  const [reading, setReading] = useState<string | null>(null)
  const [body, setBody] = useState('')

  const { data, loading } = usePoll<{
    skills: LibSkill[]
    total: number
    categories: Record<string, number>
  }>(`/skills/library?category=${encodeURIComponent(category)}&offset=${offset}&limit=${LIB_LIMIT}`, 5000)

  const read = async (name: string) => {
    if (reading === name) {
      setReading(null)
      return
    }
    setReading(name)
    setBody('')
    try {
      const r = await get<{ body: string }>(`/skills/${encodeURIComponent(name)}`)
      setBody(r.body)
    } catch (e) {
      setBody((e as Error).message)
    }
  }

  const categories = Object.entries(data?.categories ?? {}).sort((a, b) => b[1] - a[1])
  const total = data?.total ?? 0

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-1.5">
        <CategoryChip active={category === ''} onClick={() => { setCategory(''); setOffset(0) }}>
          {t('skills.allCategories')}
        </CategoryChip>
        {categories.map(([cat, n]) => (
          <CategoryChip
            key={cat}
            active={category === cat}
            onClick={() => { setCategory(cat); setOffset(0) }}
          >
            {cat} <span className="font-mono tabular-nums opacity-60">{n}</span>
          </CategoryChip>
        ))}
      </div>

      {loading && !data ? (
        <SkeletonList count={6} />
      ) : (
        <div className="space-y-1.5">
          {(data?.skills ?? []).map((s) => (
            <div
              key={s.name}
              data-reveal
              className={cn(
                'rounded-[var(--radius-md)] overflow-hidden border transition-[border-color,background-color] duration-200',
                reading === s.name ? 'border-line' : 'border-border hover:border-line hover:bg-raised',
              )}
            >
              <button onClick={() => read(s.name)} className="w-full px-3.5 py-3 text-left">
                <div className="flex items-center gap-2">
                  <span className="min-w-0 truncate font-mono text-xs text-foreground">{s.name}</span>
                  {s.category ? <Badge variant="outline">{s.category}</Badge> : null}
                </div>
                <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{s.description}</p>
              </button>
              {reading === s.name ? (
                <div className="m-open border-t border-border px-3.5 py-3">
                  {body === '' ? (
                    <Skeleton className="h-24 w-full" />
                  ) : (
                    <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-relaxed text-muted-foreground">
                      {body}
                    </pre>
                  )}
                </div>
              ) : null}
            </div>
          ))}
        </div>
      )}

      {total > LIB_LIMIT ? (
        <Pagination offset={offset} limit={LIB_LIMIT} total={total} onChange={setOffset} />
      ) : null}
    </div>
  )
}

function CategoryChip({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        'rounded-full border px-3 py-1.5 text-xs transition-colors duration-200',
        active
          ? 'border-transparent bg-nav-active text-foreground'
          : 'border-border text-muted-foreground hover:text-foreground',
      )}
    >
      {children}
    </button>
  )
}
