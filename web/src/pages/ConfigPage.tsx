import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowRight, MagnifyingGlass, PaintBrush, Sparkle } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import {
  humanizeGroup,
  movedLabelKeys,
  partitionSearchResults,
  settingsHref,
  splitConfigGroups,
  type MovedField,
} from '@/lib/configGroups'
import { usePageActions } from '@/components/layout/PageChrome'
import { ModulesSettings, modulesMatchQuery } from '@/components/settings/ModulesSettings'
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
  Textarea,
} from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'
import { AppearanceCard } from '@/components/settings/Appearance'
import { DashboardPasswordCard } from '@/components/settings/ConfigCards'
import {
  ConfigFieldRows,
  ConfigGroupPanel,
  ConfigNotices,
  ConfigSaveButton,
  useConfigEditor,
  type Field,
} from '@/components/settings/ConfigGroupPanel'

const ESSENTIALS = '__essentials'
const APPEARANCE = '__appearance'
const YAML = '__yaml'

export default function ConfigPage() {
  const { t } = useI18n()
  const editor = useConfigEditor()
  const { data, loading, reload, fields, edits, dirty, saving, saved } = editor
  const rawState = useApi<{ yaml: string }>('/config/raw')

  const [filter, setFilter] = useState('')
  const [yamlDraft, setYamlDraft] = useState<string | null>(null)
  const [section, setSection] = useState<string>(ESSENTIALS)

  const query = filter.trim().toLowerCase()
  const searching = query.length > 0

  // Groups a page's settings sheet now edits drop out of the section list.
  const groups = useMemo(() => {
    const seen: string[] = []
    for (const f of fields) if (!seen.includes(f.group)) seen.push(f.group)
    return splitConfigGroups(seen).stays
  }, [fields])

  // Searching spans every group and tier so nothing hides behind disclosure;
  // matches in moved groups link to the page that edits them.
  const results = useMemo(
    () =>
      partitionSearchResults(
        fields.filter(
          (f) =>
            !query || f.path.toLowerCase().includes(query) || f.label.toLowerCase().includes(query),
        ),
      ),
    [fields, query],
  )
  const appearanceMatch =
    searching &&
    [t('settings.appearance'), t('settings.language'), t('settings.theme'), 'appearance', 'language', 'theme']
      .some((s) => s.toLowerCase().includes(query))
  const modulesMatch = searching && modulesMatchQuery(t, query)
  const matchCount =
    results.local.length + results.moved.length + (appearanceMatch ? 1 : 0) + (modulesMatch ? 1 : 0)

  const essentialFields = useMemo(() => fields.filter((f) => f.tier === 'essential'), [fields])

  const dirtyPerGroup = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const f of fields) if (f.path in edits) counts[f.group] = (counts[f.group] ?? 0) + 1
    return counts
  }, [fields, edits])

  const dirtyEssentials = useMemo(
    () => fields.filter((f) => f.tier === 'essential' && f.path in edits).length,
    [fields, edits],
  )

  const saveYaml = async () => {
    if (yamlDraft === null) return
    editor.setSaving(true)
    editor.setError(undefined)
    try {
      const result = await post<{ restart_fields?: string[] }>('/config/raw', { yaml: yamlDraft })
      editor.setRestartFields(result.restart_fields ?? [])
      setYamlDraft(null)
      reload()
      rawState.reload()
      editor.setSaved(true)
      setTimeout(() => editor.setSaved(false), 2500)
    } catch (e) {
      editor.setError((e as Error).message)
    } finally {
      editor.setSaving(false)
    }
  }

  usePageActions(
    <ConfigSaveButton editor={editor} />,
    // `edits` (not just its count `dirty`) must be a dependency: `save` closes
    // over the edits map, so without this the header button keeps a stale
    // closure while you edit ONE field — dirty stays 1, the button is never
    // re-registered, and Save keeps posting only the first keystroke. That was
    // the "only one character saves per save" bug.
    [edits, dirty, saving, saved, t],
  )

  return (
    <div className="flex flex-col gap-5 lg:min-h-0 lg:flex-1">
      <ConfigNotices editor={editor} />

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
          <p className="text-xs text-muted-foreground">{t('config.matches', { n: matchCount })}</p>
          {matchCount === 0 ? <EmptyState title={t('config.noMatch')} /> : null}
          {modulesMatch ? <ModulesSettings /> : null}
          {appearanceMatch ? <AppearanceCard /> : null}
          {results.local.length > 0 ? (
            <ConfigFieldRows editor={editor} fields={results.local} showGroup />
          ) : null}
          {results.moved.length > 0 ? <MovedResults moved={results.moved} /> : null}
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
            ) : section === APPEARANCE ? (
              <AppearanceCard />
            ) : (
              <>
                {section === ESSENTIALS ? (
                  <p className="text-xs leading-relaxed text-muted-foreground sm:text-sm">
                    {t('config.essentialsHint')}
                  </p>
                ) : null}

                {section === ESSENTIALS ? (
                  <>
                    <ModulesSettings />
                    <DashboardPasswordCard />
                    {essentialFields.length === 0 ? (
                      <EmptyState title={t('config.nothingHere')} />
                    ) : (
                      <ConfigFieldRows editor={editor} fields={essentialFields} showGroup />
                    )}
                  </>
                ) : (
                  <ConfigGroupPanel editor={editor} groups={[section]} />
                )}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

/** Search matches in groups that moved: each links to the page that edits it. */
function MovedResults({ moved }: { moved: MovedField<Field>[] }) {
  const { t } = useI18n()
  return (
    <Card>
      <CardContent className="divide-y divide-border p-0">
        {moved.map(({ field, route }) => {
          const { hubKey, tabKey } = movedLabelKeys(route)
          return (
            <Link
              key={field.path}
              to={settingsHref(route)}
              className="flex flex-col gap-1 px-4 py-3 transition-colors hover:bg-accent/50 sm:flex-row sm:items-center sm:gap-4 sm:px-5"
            >
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="text-sm font-medium">{field.label}</span>
                  <Badge variant="outline">{humanizeGroup(field.group)}</Badge>
                </div>
                <p className="truncate font-mono text-[10px] text-muted-foreground/70">{field.path}</p>
              </div>
              <span className="flex items-center gap-1 text-xs text-primary sm:shrink-0">
                {t('settings.movedTo', { place: `${t(hubKey)} › ${t(tabKey)}` })}
                <ArrowRight className="size-3.5 shrink-0" />
              </span>
            </Link>
          )
        })}
      </CardContent>
    </Card>
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
          {item(
            APPEARANCE,
            t('settings.appearance'),
            undefined,
            <PaintBrush
              className="size-4 shrink-0"
              weight={section === APPEARANCE ? 'fill' : 'regular'}
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
          <option value={APPEARANCE}>{t('settings.appearance')}</option>
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
