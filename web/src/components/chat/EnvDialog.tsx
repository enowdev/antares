import { useEffect, useState } from 'react'
import { Eye, EyeSlash, FloppyDisk, Plus, Trash } from '@phosphor-icons/react'
import { get, post } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface Entry {
  key: string
  value: string
}
interface EnvFile {
  name: string
  entries: Entry[]
  raw: string
}

// A key looks sensitive when it mentions a secret-ish word; its value is masked
// until revealed, matching how the rest of the dashboard treats credentials.
function isSecretKey(k: string): boolean {
  return /key|token|secret|password|passwd|pwd|api|dsn|auth|credential|private/i.test(k)
}

/**
 * The project Environment viewer/editor, opened from the sidebar's Settings
 * button. It lists the project's dotenv files, shows each as an editable table
 * or raw text, and saves edits back to the file. Sensitive values are masked
 * until revealed. Reading .env exposes credentials, so the endpoints behind it
 * are gated by the dashboard password.
 */
export function EnvDialog({
  open,
  onOpenChange,
  projectDir,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectDir: string
}) {
  const { t } = useI18n()
  const [files, setFiles] = useState<EnvFile[]>([])
  const [active, setActive] = useState(0)
  const [view, setView] = useState<'table' | 'raw'>('table')
  const [reveal, setReveal] = useState<Record<string, boolean>>({})
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState<string>()
  const [error, setError] = useState<string>()

  useEffect(() => {
    if (!open) return
    setLoading(true)
    setMsg(undefined)
    setError(undefined)
    get<{ files: EnvFile[] }>(`/project/env?dir=${encodeURIComponent(projectDir)}`)
      .then((d) => {
        setFiles(d.files ?? [])
        setActive(0)
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false))
  }, [open, projectDir])

  const file = files[active]

  const setEntry = (i: number, patch: Partial<Entry>) => {
    setFiles((prev) =>
      prev.map((f, fi) =>
        fi === active
          ? { ...f, entries: f.entries.map((e, ei) => (ei === i ? { ...e, ...patch } : e)) }
          : f,
      ),
    )
  }
  const addEntry = () =>
    setFiles((prev) =>
      prev.map((f, fi) => (fi === active ? { ...f, entries: [...f.entries, { key: '', value: '' }] } : f)),
    )
  const removeEntry = (i: number) =>
    setFiles((prev) =>
      prev.map((f, fi) =>
        fi === active ? { ...f, entries: f.entries.filter((_, ei) => ei !== i) } : f,
      ),
    )

  const save = async () => {
    if (!file) return
    setSaving(true)
    setMsg(undefined)
    setError(undefined)
    try {
      await post('/project/env', {
        dir: projectDir,
        file: file.name,
        entries: file.entries.filter((e) => e.key.trim()),
      })
      setMsg(t('env.saved'))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('env.title')}</DialogTitle>
          <DialogDescription>{t('env.desc')}</DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-3">
          {loading ? (
            <p className="py-6 text-center text-xs text-muted-foreground">{t('env.loading')}</p>
          ) : files.length === 0 ? (
            <p className="py-6 text-center text-xs text-muted-foreground">{t('env.none')}</p>
          ) : (
            <>
              {/* File tabs + view toggle */}
              <div className="flex flex-wrap items-center gap-2">
                <div className="flex flex-wrap gap-1">
                  {files.map((f, i) => (
                    <button
                      key={f.name}
                      onClick={() => setActive(i)}
                      className={cn(
                        'rounded-full border px-3 py-1 font-mono text-[11px] transition-colors',
                        i === active
                          ? 'border-transparent bg-nav-active text-foreground'
                          : 'border-border text-muted-foreground hover:text-foreground',
                      )}
                    >
                      {f.name}
                    </button>
                  ))}
                </div>
                <div className="ml-auto flex overflow-hidden rounded-full border border-border p-0.5">
                  {(['table', 'raw'] as const).map((v) => (
                    <button
                      key={v}
                      onClick={() => setView(v)}
                      className={cn(
                        'rounded-full px-3 py-1 text-[11px] transition-colors',
                        view === v ? 'bg-nav-active text-foreground' : 'text-muted-foreground hover:text-foreground',
                      )}
                    >
                      {v === 'table' ? t('env.table') : t('env.raw')}
                    </button>
                  ))}
                </div>
              </div>

              {view === 'table' ? (
                <div className="max-h-[50vh] space-y-1.5 overflow-y-auto">
                  {file?.entries.map((e, i) => {
                    const secret = isSecretKey(e.key)
                    const rk = `${active}:${i}`
                    const shown = reveal[rk] || !secret
                    return (
                      <div key={i} className="flex items-center gap-1.5">
                        <input
                          value={e.key}
                          onChange={(ev) => setEntry(i, { key: ev.target.value })}
                          spellCheck={false}
                          className="h-8 w-2/5 rounded-full border border-border bg-transparent px-3.5 font-mono text-[11px] outline-none transition-colors focus:border-foreground/60"
                          placeholder="KEY"
                        />
                        <div className="relative flex-1">
                          <input
                            value={e.value}
                            onChange={(ev) => setEntry(i, { value: ev.target.value })}
                            type={shown ? 'text' : 'password'}
                            spellCheck={false}
                            className="h-8 w-full rounded-full border border-border bg-transparent pl-3.5 pr-9 font-mono text-[11px] outline-none transition-colors focus:border-foreground/60"
                            placeholder="value"
                          />
                          {secret ? (
                            <button
                              type="button"
                              onClick={() => setReveal((r) => ({ ...r, [rk]: !r[rk] }))}
                              className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                            >
                              {shown ? <EyeSlash className="size-3.5" /> : <Eye className="size-3.5" />}
                            </button>
                          ) : null}
                        </div>
                        <button
                          onClick={() => removeEntry(i)}
                          className="grid size-7 shrink-0 place-items-center text-muted-foreground hover:text-[var(--destructive)]"
                          aria-label={t('common.delete')}
                        >
                          <Trash className="size-3.5" />
                        </button>
                      </div>
                    )
                  })}
                  <button
                    onClick={addEntry}
                    className="flex items-center gap-1.5 rounded-full border border-dashed border-border px-3.5 py-1.5 font-mono text-[11px] lowercase text-muted-foreground transition-colors hover:border-line hover:text-foreground"
                  >
                    <Plus className="size-3.5" />
                    {t('env.add')}
                  </button>
                </div>
              ) : (
                <pre className="max-h-[50vh] overflow-auto rounded-[var(--radius-lg)] border border-border bg-background/40 p-3 font-mono text-[11px] leading-relaxed">
                  {file?.raw || '(empty)'}
                </pre>
              )}

              {error ? <p className="m-rise text-xs text-[var(--destructive)]">{error}</p> : null}
              {msg ? <p className="m-rise text-xs text-[var(--success)]">{msg}</p> : null}

              {view === 'table' ? (
                <div className="flex justify-end">
                  <Button size="sm" onClick={save} loading={saving} className="gap-1.5">
                    <FloppyDisk className="size-4" />
                    {t('env.save', { file: file?.name ?? '' })}
                  </Button>
                </div>
              ) : null}
            </>
          )}
        </DialogBody>
      </DialogContent>
    </Dialog>
  )
}
