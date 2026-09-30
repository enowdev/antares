import { useEffect, useMemo, useState } from 'react'
import { CaretDown, CheckCircle, Eye, EyeSlash, FloppyDisk, Warning } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { humanizeGroup } from '@/lib/configGroups'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge, Card, CardContent, EmptyState, Input, Label, Switch } from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'
import { DashboardPasswordCard, GoogleOsintCard } from '@/components/settings/ConfigCards'

export type Tier = 'essential' | 'common' | 'advanced'

export interface Field {
  path: string
  label: string
  group: string
  type: 'string' | 'number' | 'boolean' | 'string[]' | 'object'
  tier: Tier
  default: unknown
  secret: boolean
  enum?: string[]
  help?: string
  reload: 'live' | 'reconciled' | 'restart_required'
}

export interface ConfigResponse {
  values: Record<string, unknown>
  schema: Field[]
  restart_fields?: string[]
}

function readPath(obj: Record<string, unknown>, path: string): unknown {
  return path.split('.').reduce<unknown>((acc, key) => {
    if (acc && typeof acc === 'object') return (acc as Record<string, unknown>)[key]
    return undefined
  }, obj)
}


/**
 * Loads `GET /api/config` and holds unsaved edits. One editor backs the whole
 * Settings page (so edits survive switching sections and one Save writes them
 * all); a page's settings sheet makes its own.
 */
export function useConfigEditor() {
  const { data, loading, reload } = useApi<ConfigResponse>('/config')
  const [edits, setEdits] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string>()
  const [restartFields, setRestartFields] = useState<string[]>([])
  const [revealed, setRevealed] = useState<Record<string, boolean>>({})

  const fields = useMemo(() => (data?.schema ?? []).filter((f) => f.type !== 'object'), [data])
  const dirty = Object.keys(edits).length

  useEffect(() => setRestartFields(data?.restart_fields ?? []), [data])

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
      const result = await post<{ restart_fields?: string[] }>('/config', { updates: edits })
      // The server omits restart_fields when nothing needs a restart; storing
      // undefined crashed the notice's `.length` read after every save.
      setRestartFields(result.restart_fields ?? [])
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

  return {
    data,
    loading,
    reload,
    fields,
    edits,
    dirty,
    saving,
    setSaving,
    saved,
    setSaved,
    error,
    setError,
    restartFields,
    setRestartFields,
    revealed,
    toggleReveal: (path: string) => setRevealed((r) => ({ ...r, [path]: !r[path] })),
    valueOf,
    setValue,
    save,
  }
}

export type ConfigEditor = ReturnType<typeof useConfigEditor>

/** Save error and the "restart to apply" notice. */
export function ConfigNotices({ editor }: { editor: ConfigEditor }) {
  const { t } = useI18n()
  return (
    <>
      {editor.error ? (
        <div
          role="alert"
          className="m-rise flex items-start gap-2 border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-4 py-3 text-sm text-destructive"
        >
          <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
          <span className="min-w-0 break-words">{editor.error}</span>
        </div>
      ) : null}
      {editor.restartFields.length > 0 ? (
        <p role="status" className="m-rise flex items-start gap-2.5 break-words border border-border bg-card px-4 py-3 text-sm">
          <span className="mt-[7px] size-1.5 shrink-0 rounded-full bg-[var(--warning)]" aria-hidden />
          {t('config.restartPending', { fields: editor.restartFields.join(', ') })}
        </p>
      ) : null}
    </>
  )
}

/** Save button label and icon, shared by the Settings header and the sheet. */
export function ConfigSaveButton({ editor, className }: { editor: ConfigEditor; className?: string }) {
  const { t } = useI18n()
  const { saved, dirty, saving } = editor
  return (
    <Button size="sm" onClick={editor.save} loading={saving} disabled={!dirty} className={cn('gap-1.5', className)}>
      {saved ? <CheckCircle className="size-4" weight="fill" /> : <FloppyDisk className="size-4" />}
      {saved ? t('common.saved') : dirty ? t('config.saveN', { n: dirty }) : t('common.save')}
    </Button>
  )
}

/** One card of editable rows. */
export function ConfigFieldRows({
  editor,
  fields,
  showGroup = false,
}: {
  editor: ConfigEditor
  fields: Field[]
  showGroup?: boolean
}) {
  return (
    <Card>
      <CardContent className="divide-y divide-border p-0">
        {fields.map((f) => (
          <FieldRow
            key={f.path}
            field={f}
            showGroup={showGroup}
            value={editor.valueOf(f)}
            dirty={f.path in editor.edits}
            revealed={!!editor.revealed[f.path]}
            onReveal={() => editor.toggleReveal(f.path)}
            onChange={(v) => editor.setValue(f.path, v)}
          />
        ))}
      </CardContent>
    </Card>
  )
}

/**
 * The fields of one or more config groups: group-specific cards, the common
 * and essential rows, and advanced rows behind a disclosure.
 */
function ConfigGroupFields({ editor, groups }: { editor: ConfigEditor; groups: string[] }) {
  const { t } = useI18n()
  const [showAdvanced, setShowAdvanced] = useState(false)
  const key = groups.join(',')

  // Reset disclosure when moving between sections.
  useEffect(() => setShowAdvanced(false), [key])

  const groupFields = useMemo(
    () => editor.fields.filter((f) => groups.includes(f.group)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [editor.fields, key],
  )
  const visible = groupFields.filter((f) => showAdvanced || f.tier !== 'advanced')
  const hiddenCount = groupFields.length - visible.length
  const many = groups.length > 1

  return (
    <>
      {groups.includes('osint') ? (
        <GoogleOsintCard cookieEdited={editor.edits['osint.google_cookie'] as string | undefined} />
      ) : null}

      {groups.includes('server') ? <DashboardPasswordCard /> : null}

      {visible.length === 0 ? (
        <EmptyState title={t('config.nothingHere')} />
      ) : (
        <ConfigFieldRows editor={editor} fields={visible} showGroup={many} />
      )}

      {hiddenCount > 0 ? (
        <button
          onClick={() => setShowAdvanced(true)}
          className="flex w-full items-center justify-center gap-1.5 border border-dashed border-border py-2.5 font-mono text-[11px] lowercase text-muted-foreground transition-[border-color,background-color,color] duration-200 hover:border-line hover:bg-raised hover:text-foreground"
        >
          <CaretDown className="size-3.5" />
          {t('config.showAdvanced', { n: hiddenCount })}
        </button>
      ) : showAdvanced && groupFields.some((f) => f.tier === 'advanced') ? (
        <button
          onClick={() => setShowAdvanced(false)}
          className="flex w-full items-center justify-center gap-1.5 border border-dashed border-border py-2.5 font-mono text-[11px] lowercase text-muted-foreground transition-[border-color,background-color,color] duration-200 hover:border-line hover:bg-raised hover:text-foreground"
        >
          <CaretDown className="size-3.5 rotate-180" />
          {t('config.hideAdvanced')}
        </button>
      ) : null}
    </>
  )
}

/**
 * Edit the fields of the given config groups. With an `editor` it renders the
 * fields only and the caller owns loading, notices, and Save (the Settings
 * page). Without one it is self-contained: it loads the config, shows notices,
 * and has its own Save bar (a page's settings sheet).
 */
export function ConfigGroupPanel({ groups, editor }: { groups: string[]; editor?: ConfigEditor }) {
  if (editor) return <ConfigGroupFields editor={editor} groups={groups} />
  return <StandaloneConfigGroupPanel groups={groups} />
}

function StandaloneConfigGroupPanel({ groups }: { groups: string[] }) {
  const { t } = useI18n()
  const editor = useConfigEditor()

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4 sm:p-5">
        <ConfigNotices editor={editor} />
        {editor.loading && !editor.data ? (
          <SkeletonList count={4} />
        ) : !editor.data ? (
          <EmptyState title={t('config.loadFailed')} />
        ) : (
          <ConfigGroupFields editor={editor} groups={groups} />
        )}
      </div>
      <div className="flex shrink-0 items-center justify-end gap-2 border-t border-border p-4 sm:p-5">
        <ConfigSaveButton editor={editor} />
      </div>
    </div>
  )
}

function FieldRow({
  field,
  value,
  dirty,
  revealed,
  showGroup,
  onReveal,
  onChange,
}: {
  field: Field
  value: unknown
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
      <div className="flex items-center gap-4 px-4 py-3.5 sm:px-5">
        <div className="min-w-0 flex-1">
          <FieldLabel field={field} dirty={dirty} showGroup={showGroup} />
          {field.help ? (
            <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{field.help}</p>
          ) : null}
        </div>
        <Switch checked={!!value} onCheckedChange={onChange} />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2 px-4 py-3.5 sm:flex-row sm:items-start sm:gap-4 sm:px-5">
      <div className="min-w-0 sm:w-[42%] sm:pt-1.5">
        <FieldLabel field={field} dirty={dirty} showGroup={showGroup} />
        {field.help ? (
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{field.help}</p>
        ) : null}
      </div>

      <div className="min-w-0 sm:flex-1">
        {field.enum ? (
          <select
            value={String(value ?? '')}
            onChange={(e) => onChange(e.target.value)}
            className="h-9 w-full cursor-pointer border border-input bg-transparent px-3 font-mono text-base transition-[border-color] duration-200 hover:border-line focus-visible:border-ring focus-visible:outline-none sm:text-xs"
          >
            {field.enum.map((opt) => (
              <option key={opt} value={opt} className="bg-popover text-popover-foreground">
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
    if (parsed.join('\x00') !== (Array.isArray(value) ? (value as string[]).join('\x00') : '')) {
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
        <Label>{field.label}</Label>
        {showGroup ? <Badge variant="outline">{humanizeGroup(field.group)}</Badge> : null}
        {dirty ? <Badge>{t('config.changed')}</Badge> : null}
        <span className="font-mono text-[11px] text-dim">
          {t(field.reload === 'restart_required' ? 'config.reloadRestart' : field.reload === 'reconciled' ? 'config.reloadReconciled' : 'config.reloadLive')}
        </span>
      </div>
      <p className="mt-0.5 truncate font-mono text-[11px] text-dim">{field.path}</p>
    </>
  )
}
