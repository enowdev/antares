import { useRef, useState } from 'react'
import { CheckCircle, FileArrowUp, GlobeHemisphereWest, Plus, Trash, XCircle } from '@phosphor-icons/react'
import { del, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { PageLayout } from '@/components/layout/PageLayout'
import { Badge, Card, EmptyState, Label } from '@/components/ui/primitives'
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
import { Button } from '@/components/ui/button'
import { usePageActions } from '@/components/layout/PageChrome'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { SensitiveGate, useDashboardLocked } from '@/components/ui/SensitiveGate'

interface ProxyEntry {
  id: string
  label: string
  scheme: string
  host: string
  port: number
  username: string
  password: string // masked
  url: string // masked
}

interface ProxyList {
  entries: ProxyEntry[]
}

export default function ProxiesPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<ProxyList>('/proxies')
  const [adding, setAdding] = useState(false)
  const [toRemove, setToRemove] = useState<ProxyEntry | null>(null)
  const [removing, setRemoving] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<Record<string, { ok: boolean; ip?: string; error?: string }>>({})
  const locked = useDashboardLocked()

  usePageActions(
    locked === false ? null : (
      <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
        <Plus className="size-4" />
        {t('proxies.add')}
      </Button>
    ),
    [t, locked],
  )

  const test = async (e: ProxyEntry) => {
    setBusyId(e.id)
    setTestResult((r) => ({ ...r, [e.id]: { ok: false, error: '…' } }))
    try {
      const res = await post<{ ok: boolean; ip?: string; error?: string }>('/proxies/test', { id: e.id })
      setTestResult((r) => ({ ...r, [e.id]: res }))
    } catch (err) {
      setTestResult((r) => ({ ...r, [e.id]: { ok: false, error: (err as Error).message } }))
    } finally {
      setBusyId(null)
    }
  }

  const confirmRemove = async () => {
    if (!toRemove) return
    setRemoving(true)
    try {
      await del(`/proxies/${encodeURIComponent(toRemove.id)}`)
      reload()
    } finally {
      setRemoving(false)
      setToRemove(null)
    }
  }

  if (loading && !data) return <SkeletonList count={3} />

  const entries = data?.entries ?? []

  return (
    <PageLayout>
      <SensitiveGate>
      <AddProxiesDialog open={adding} onOpenChange={setAdding} onSaved={reload} />
      <ConfirmDialog
        open={!!toRemove}
        onOpenChange={(o) => !o && setToRemove(null)}
        title={t('proxies.removeTitle')}
        description={t('proxies.removeDesc', { label: toRemove?.label ?? '' })}
        confirmLabel={t('common.remove')}
        loading={removing}
        onConfirm={() => void confirmRemove()}
      />

      <p className="text-xs leading-relaxed text-muted-foreground sm:text-sm">{t('proxies.storeHint')}</p>

      {entries.length === 0 ? (
        <EmptyState
          icon={<GlobeHemisphereWest className="size-6" />}
          title={t('proxies.none')}
          description={t('proxies.noneDesc')}
          action={
            <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
              <Plus className="size-4" />
              {t('proxies.add')}
            </Button>
          }
        />
      ) : (
        <Card className="overflow-hidden p-0">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[42rem] text-sm">
              <thead>
                <tr className="border-b border-border text-left">
                  <th className="px-4 py-3 font-mono text-[11px] font-normal lowercase tracking-[0.04em] text-dim">{t('proxies.label')}</th>
                  <th className="px-4 py-3 font-mono text-[11px] font-normal lowercase tracking-[0.04em] text-dim">{t('proxies.colEndpoint')}</th>
                  <th className="w-28 px-4 py-3 font-mono text-[11px] font-normal lowercase tracking-[0.04em] text-dim">{t('proxies.colTest')}</th>
                  <th className="w-12 px-4 py-3" />
                </tr>
              </thead>
              <tbody>
                {entries.map((e) => {
                  const tr = testResult[e.id]
                  return (
                    <tr
                      key={e.id}
                      data-reveal
                      className="border-t border-border transition-colors duration-200 first:border-t-0 hover:bg-raised"
                    >
                      <td className="px-4 py-3.5">
                        <div className="flex items-center gap-2">
                          <span className="font-medium">{e.label}</span>
                          {e.scheme ? (
                            <Badge variant="outline" className="shrink-0 font-mono text-[10px] lowercase">
                              {e.scheme}
                            </Badge>
                          ) : null}
                        </div>
                      </td>
                      <td className="max-w-[20rem] px-3 py-2.5">
                        <span className="block truncate font-mono text-xs text-muted-foreground">{e.url}</span>
                      </td>
                      <td className="px-4 py-3.5">
                        {tr ? (
                          tr.error === '…' ? (
                            <span className="text-xs text-muted-foreground">{t('proxies.testing')}</span>
                          ) : tr.ok ? (
                            <span className="inline-flex items-center gap-1 font-mono text-xs text-[var(--success)]">
                              <CheckCircle className="size-3.5" weight="fill" />
                              {tr.ip}
                            </span>
                          ) : (
                            <span
                              className="inline-flex items-center gap-1 text-xs text-destructive"
                              title={tr.error}
                            >
                              <XCircle className="size-3.5" weight="fill" />
                              {t('proxies.failedShort')}
                            </span>
                          )
                        ) : (
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-7 px-2 text-xs"
                            loading={busyId === e.id}
                            onClick={() => void test(e)}
                          >
                            {t('proxies.test')}
                          </Button>
                        )}
                      </td>
                      <td className="px-4 py-3.5 text-right">
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          onClick={() => setToRemove(e)}
                          aria-label={t('common.remove')}
                        >
                          <Trash className="size-4" />
                        </Button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </Card>
      )}
      </SensitiveGate>
    </PageLayout>
  )
}

function AddProxiesDialog({
  open,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<{ added: number; failed: { line: number; text: string; error: string }[] } | null>(
    null,
  )
  const [error, setError] = useState<string>()
  const fileRef = useRef<HTMLInputElement>(null)

  const loadFile = async (f: File) => {
    const content = await f.text()
    // Append to whatever's already there, keeping a newline between.
    setText((prev) => (prev.trim() ? prev.replace(/\n*$/, '\n') + content : content))
  }

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    setResult(null)
    try {
      const r = await post<{ added: number; failed: { line: number; text: string; error: string }[] }>(
        '/proxies/batch',
        { text },
      )
      setResult({ added: r.added, failed: r.failed ?? [] })
      onSaved()
      // Keep only the failed lines so they can be corrected; clear the rest.
      if (r.failed?.length) setText(r.failed.map((f) => f.text).join('\n'))
      else setText('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const lineCount = text.split('\n').filter((l) => l.trim() && !l.trim().startsWith('#')).length

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('proxies.add')}</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-3">
          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <Label>{t('proxies.pasteLabel')}</Label>
              <input
                ref={fileRef}
                type="file"
                accept=".txt,.csv,.list,text/plain"
                className="hidden"
                onChange={(e) => {
                  const f = e.target.files?.[0]
                  if (f) void loadFile(f)
                  e.target.value = ''
                }}
              />
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-7 gap-1.5 px-2 text-xs"
                onClick={() => fileRef.current?.click()}
              >
                <FileArrowUp className="size-3.5" />
                {t('proxies.fromFile')}
              </Button>
            </div>
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              spellCheck={false}
              rows={9}
              placeholder={
                'host:port:user:pass\n' +
                'http://user:pass@host:port\n' +
                'socks5://host:port\n' +
                'user:pass@host:port\n' +
                'host:port'
              }
              className="w-full resize-y border border-input bg-transparent px-3 py-2 font-mono text-xs leading-relaxed focus-visible:border-foreground focus-visible:outline-none"
            />
            <p className="text-[11px] leading-relaxed text-muted-foreground">{t('proxies.pasteHint')}</p>
          </div>

          {result ? (
            <div className="m-rise space-y-1.5 border border-border bg-card px-4 py-3 text-xs">
              <p className="inline-flex items-center gap-1.5 text-[var(--success)]">
                <CheckCircle className="size-4" weight="fill" />
                {t('proxies.addedN', { n: result.added })}
              </p>
              {result.failed.length > 0 ? (
                <div className="space-y-0.5">
                  <p className="text-destructive">{t('proxies.failedN', { n: result.failed.length })}</p>
                  {result.failed.slice(0, 6).map((f) => (
                    <p key={f.line} className="truncate font-mono text-[10px] text-muted-foreground">
                      L{f.line}: {f.text} — {f.error}
                    </p>
                  ))}
                </div>
              ) : null}
            </div>
          ) : null}

          {error ? (
            <p className="m-rise border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-4 py-3 text-sm text-destructive">
              {error}
            </p>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="ghost">{t('common.close')}</Button>
          </DialogClose>
          <Button onClick={() => void submit()} loading={busy} disabled={lineCount === 0}>
            {lineCount > 0 ? t('proxies.addN', { n: lineCount }) : t('proxies.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
