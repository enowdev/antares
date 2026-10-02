import { useEffect, useState } from 'react'
import {
  ArrowsClockwise,
  CheckCircle,
  Cpu,
  HardDrives,
  Memory,
  Pencil,
  Plus,
  Trash,
  XCircle,
} from '@phosphor-icons/react'
import { del, get, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Badge, Card, EmptyState, Input, Label } from '@/components/ui/primitives'
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
import { Spoiler } from '@/components/ui/Spoiler'
import { SensitiveGate, useDashboardLocked } from '@/components/ui/SensitiveGate'

interface VPSHost {
  id: string
  label: string
  host: string
  port: number
  username: string
  auth_method: string
  has_password: boolean
  has_key: boolean
}

interface Metrics {
  reachable: boolean
  error?: string
  hostname?: string
  os?: string
  kernel?: string
  uptime?: string
  cpu_cores?: number
  cpu_percent?: number
  load1?: number
  load5?: number
  load15?: number
  mem_total_mb?: number
  mem_used_mb?: number
  mem_percent?: number
  swap_total_mb?: number
  swap_used_mb?: number
  swap_percent?: number
  disk_total_gb?: number
  disk_used_gb?: number
  disk_percent?: number
  processes?: number
  top_proc?: string[]
}

export default function VPSPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<{ hosts: VPSHost[] }>('/vps')
  const [editing, setEditing] = useState<VPSHost | null>(null)
  const [adding, setAdding] = useState(false)
  const [toRemove, setToRemove] = useState<VPSHost | null>(null)
  const [removing, setRemoving] = useState(false)
  const locked = useDashboardLocked()

  usePageActions(
    // Hide "Add" until a dashboard password protects these credentials.
    locked === false ? null : (
      <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
        <Plus className="size-4" />
        {t('vps.add')}
      </Button>
    ),
    [t, locked],
  )

  const confirmRemove = async () => {
    if (!toRemove) return
    setRemoving(true)
    try {
      await del(`/vps/${encodeURIComponent(toRemove.id)}`)
      reload()
    } finally {
      setRemoving(false)
      setToRemove(null)
    }
  }

  if (loading && !data) return <SkeletonList count={2} />

  const hosts = data?.hosts ?? []

  return (
    <PageLayout>
      <SensitiveGate>
        <VPSDialog open={adding} onOpenChange={setAdding} onSaved={reload} />
        <VPSDialog
          open={!!editing}
          host={editing ?? undefined}
          onOpenChange={(o) => !o && setEditing(null)}
          onSaved={reload}
        />
        <ConfirmDialog
          open={!!toRemove}
          onOpenChange={(o) => !o && setToRemove(null)}
          title={t('vps.removeTitle')}
          description={t('vps.removeDesc', { label: toRemove?.label ?? '' })}
          confirmLabel={t('common.remove')}
          loading={removing}
          onConfirm={() => void confirmRemove()}
        />

        {hosts.length === 0 ? (
          <EmptyState
            icon={<HardDrives className="size-6" />}
            title={t('vps.none')}
            description={t('vps.noneDesc')}
            action={
              <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
                <Plus className="size-4" />
                {t('vps.add')}
              </Button>
            }
          />
        ) : (
          <div className="grid gap-3 lg:grid-cols-2">
            {hosts.map((h) => (
              <VPSCard key={h.id} host={h} onEdit={() => setEditing(h)} onRemove={() => setToRemove(h)} />
            ))}
          </div>
        )}
      </SensitiveGate>
    </PageLayout>
  )
}

function VPSCard({ host, onEdit, onRemove }: { host: VPSHost; onEdit: () => void; onRemove: () => void }) {
  const { t } = useI18n()
  const [m, setM] = useState<Metrics | null>(null)
  const [loading, setLoading] = useState(true)
  const [showProc, setShowProc] = useState(false)

  const refresh = () => {
    setLoading(true)
    get<Metrics>(`/vps/${encodeURIComponent(host.id)}/metrics`)
      .then((d) => setM(d))
      .catch((e) => setM({ reachable: false, error: (e as Error).message }))
      .finally(() => setLoading(false))
  }
  // Load once on mount; refresh is manual (each poll is a live SSH round trip).
  useEffect(refresh, [host.id])

  return (
    <Card className="p-5">
      <div className="mb-4 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[15px] font-medium">{host.label}</span>
            {m ? (
              m.reachable ? (
                <Badge variant="success">
                  <CheckCircle className="size-3" weight="fill" />
                  {t('vps.online')}
                </Badge>
              ) : (
                <Badge variant="destructive">
                  <XCircle className="size-3" weight="fill" />
                  {t('vps.offline')}
                </Badge>
              )
            ) : null}
          </div>
          <p className="mt-0.5 truncate font-mono text-xs text-muted-foreground">
            {host.username}@<Spoiler>{host.host}</Spoiler>:{host.port}
          </p>
        </div>
        <div className="flex shrink-0 gap-1">
          <Button variant="ghost" size="icon-sm" onClick={refresh} aria-label={t('vps.refresh')} loading={loading}>
            <ArrowsClockwise className="size-4" />
          </Button>
          <Button variant="ghost" size="icon-sm" onClick={onEdit} aria-label={t('vps.edit')}>
            <Pencil className="size-4" />
          </Button>
          <Button variant="ghost" size="icon-sm" onClick={onRemove} aria-label={t('common.remove')}>
            <Trash className="size-4" />
          </Button>
        </div>
      </div>

      {loading && !m ? (
        <div className="h-24 animate-pulse rounded-[var(--radius-lg)] bg-raised" />
      ) : m && !m.reachable ? (
        <p className="m-rise rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] px-4 py-3 text-sm text-destructive">
          {m.error || t('vps.unreachable')}
        </p>
      ) : m ? (
        <div className="space-y-3">
          {/* identity line */}
          <p className="font-mono text-[11px] text-muted-foreground">
            {[m.os, m.kernel && `kernel ${m.kernel}`, m.uptime && `up ${m.uptime}`]
              .filter(Boolean)
              .join(' · ')}
          </p>

          <div className="grid grid-cols-[repeat(auto-fit,minmax(9.5rem,1fr))] gap-3">
          <Gauge
            icon={<Cpu className="size-3.5" />}
            label={t('vps.cpu')}
            percent={m.cpu_percent ?? 0}
            detail={`${m.load1 ?? 0} / ${m.load5 ?? 0} / ${m.load15 ?? 0} · ${m.cpu_cores ?? 0} ${t('vps.cores')}`}
          />
          <Gauge
            icon={<Memory className="size-3.5" />}
            label={t('vps.memory')}
            percent={m.mem_percent ?? 0}
            detail={`${fmtMB(m.mem_used_mb)} / ${fmtMB(m.mem_total_mb)}`}
          />
          <Gauge
            icon={<HardDrives className="size-3.5" />}
            label={t('vps.disk')}
            percent={m.disk_percent ?? 0}
            detail={`${m.disk_used_gb ?? 0}G / ${m.disk_total_gb ?? 0}G`}
          />
          {m.swap_total_mb ? (
            <Gauge label={t('vps.swap')} percent={m.swap_percent ?? 0} detail={`${fmtMB(m.swap_used_mb)} / ${fmtMB(m.swap_total_mb)}`} />
          ) : null}
          </div>

          <div className="flex items-center justify-between pt-1">
            <span className="font-mono text-[11px] tabular-nums text-muted-foreground">
              {m.processes} {t('vps.procs')}
            </span>
            <Button variant="outline" size="sm" className="h-7 px-3 text-xs" onClick={() => setShowProc(true)}>
              {t('vps.showProc')}
            </Button>
          </div>
        </div>
      ) : null}

      <ProcessModal host={host} open={showProc} onOpenChange={setShowProc} />
    </Card>
  )
}

interface Process {
  pid: number
  user: string
  cpu: number
  mem: number
  command: string
}

function ProcessModal({
  host,
  open,
  onOpenChange,
}: {
  host: VPSHost
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const { t } = useI18n()
  const [procs, setProcs] = useState<Process[] | null>(null)
  const [err, setErr] = useState<string>()
  const [q, setQ] = useState('')

  useEffect(() => {
    if (!open) return
    setProcs(null)
    setErr(undefined)
    setQ('')
    get<{ processes: Process[]; error?: string }>(`/vps/${encodeURIComponent(host.id)}/processes`)
      .then((d) => {
        if (d.error) setErr(d.error)
        setProcs(d.processes ?? [])
      })
      .catch((e) => setErr((e as Error).message))
  }, [open, host.id])

  const shown = (procs ?? []).filter((p) => {
    const s = q.trim().toLowerCase()
    return !s || p.command.toLowerCase().includes(s) || p.user.toLowerCase().includes(s) || String(p.pid).includes(s)
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {t('vps.procTitle')} — {host.label}
          </DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-2">
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('vps.procSearch')} />
          {err ? (
            <p className="m-rise rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] px-4 py-3 text-sm text-destructive">{err}</p>
          ) : procs === null ? (
            <div className="h-64 animate-pulse rounded-[var(--radius-lg)] bg-raised" />
          ) : shown.length === 0 ? (
            <p className="py-8 text-center text-xs text-muted-foreground">{t('vps.procNone')}</p>
          ) : (
            <div className="max-h-[55vh] overflow-y-auto rounded-[var(--radius-lg)] border border-border bg-card">
              <table className="w-full text-left text-xs">
                <thead className="sticky top-0 bg-card">
                  <tr className="border-b border-border">
                    <th className="px-3 py-2.5 font-mono text-[11px] font-normal lowercase tracking-[0.04em] text-dim">{t('vps.procPid')}</th>
                    <th className="px-3 py-2.5 font-mono text-[11px] font-normal lowercase tracking-[0.04em] text-dim">{t('vps.procUser')}</th>
                    <th className="px-3 py-2.5 font-mono text-[11px] font-normal lowercase tracking-[0.04em] text-dim text-right">{t('vps.cpu')}</th>
                    <th className="px-3 py-2.5 font-mono text-[11px] font-normal lowercase tracking-[0.04em] text-dim text-right">{t('vps.memory')}</th>
                    <th className="px-3 py-2.5 font-mono text-[11px] font-normal lowercase tracking-[0.04em] text-dim">{t('vps.procCmd')}</th>
                  </tr>
                </thead>
                <tbody className="font-mono">
                  {shown.map((p) => (
                    <tr key={p.pid} className="border-t border-border first:border-t-0 hover:bg-raised">
                      <td className="px-3 py-2 tabular-nums text-muted-foreground">{p.pid}</td>
                      <td className="px-3 py-2 text-muted-foreground">{p.user}</td>
                      <td className={cn('px-3 py-2 text-right tabular-nums', p.cpu >= 50 && 'text-[var(--warning)]')}>
                        {p.cpu.toFixed(1)}
                      </td>
                      <td className="px-3 py-2 text-right tabular-nums text-muted-foreground">{p.mem.toFixed(1)}</td>
                      <td className="max-w-0 truncate px-3 py-2">{p.command}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </DialogBody>
      </DialogContent>
    </Dialog>
  )
}

function Gauge({
  icon,
  label,
  percent,
  detail,
}: {
  icon?: React.ReactNode
  label: string
  percent: number
  detail: string
}) {
  const p = Math.max(0, Math.min(100, percent))
  const tone =
    p >= 90 ? 'bg-destructive' : p >= 70 ? 'bg-[var(--warning)]' : 'bg-foreground'
  return (
    <div data-reveal className="rounded-[var(--radius-lg)] border border-border px-4 py-3.5">
      <p className="eyebrow flex items-center gap-1.5">
        {icon}
        {label}
      </p>
      <p className="mt-2 text-2xl font-medium tabular-nums">
        {p}
        <span className="text-sm text-muted-foreground">%</span>
      </p>
      <div className="mt-2 h-1 overflow-hidden rounded-full bg-border">
        <div className={cn('h-full rounded-full transition-[width] duration-500', tone)} style={{ width: `${p}%` }} />
      </div>
      <p className="mt-2 truncate font-mono text-[11px] tabular-nums text-muted-foreground" title={detail}>
        {detail}
      </p>
    </div>
  )
}

function fmtMB(mb?: number): string {
  if (!mb) return '0'
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)}G`
  return `${mb}M`
}

const SCHEMES = ['password', 'key']

function VPSDialog({
  open,
  host,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  host?: VPSHost
  onOpenChange: (o: boolean) => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const isEdit = !!host
  const [label, setLabel] = useState('')
  const [hostname, setHostname] = useState('')
  const [port, setPort] = useState('22')
  const [username, setUsername] = useState('root')
  const [authMethod, setAuthMethod] = useState('password')
  const [password, setPassword] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [passphrase, setPassphrase] = useState('')
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; as?: string; error?: string } | null>(null)
  const [error, setError] = useState<string>()

  // Reset the form each time the dialog opens for a specific host (or a new one).
  useEffect(() => {
    if (!open) return
    setLabel(host?.label ?? '')
    setHostname(host?.host ?? '')
    setPort(host?.port ? String(host.port) : '22')
    setUsername(host?.username ?? 'root')
    setAuthMethod(host?.auth_method || 'password')
    setPassword('')
    setPrivateKey('')
    setPassphrase('')
    setTestResult(null)
    setError(undefined)
  }, [open, host])

  const body = () => ({
    id: host?.id,
    label: label.trim(),
    host: hostname.trim(),
    port: port ? Number(port) : 22,
    username: username.trim(),
    auth_method: authMethod,
    // Blank on edit = keep stored secret.
    password: authMethod === 'password' ? password : '',
    private_key: authMethod === 'key' ? privateKey : '',
    passphrase: authMethod === 'key' ? passphrase : '',
  })

  const valid = hostname.trim() !== ''

  const test = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const r = await post<{ ok: boolean; as?: string; error?: string }>('/vps/test', body())
      setTestResult(r)
    } catch (e) {
      setTestResult({ ok: false, error: (e as Error).message })
    } finally {
      setTesting(false)
    }
  }

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post('/vps', body())
      onSaved()
      onOpenChange(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{isEdit ? t('vps.edit') : t('vps.add')}</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-3">
          <div className="space-y-1.5">
            <Label>{t('vps.label')}</Label>
            <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder={t('vps.labelPh')} />
          </div>
          <div className="grid grid-cols-[1fr_6rem] gap-2">
            <div className="space-y-1.5">
              <Label>{t('vps.host')}</Label>
              <Input value={hostname} onChange={(e) => setHostname(e.target.value)} placeholder="1.2.3.4 / vps.example.com" />
            </div>
            <div className="space-y-1.5">
              <Label>{t('vps.port')}</Label>
              <Input type="number" value={port} onChange={(e) => setPort(e.target.value)} placeholder="22" />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1.5">
              <Label>{t('vps.username')}</Label>
              <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />
            </div>
            <div className="space-y-1.5">
              <Label>{t('vps.authMethod')}</Label>
              <select
                value={authMethod}
                onChange={(e) => setAuthMethod(e.target.value)}
                className="h-9 w-full rounded-full border border-input bg-transparent px-4 text-sm focus-visible:border-foreground focus-visible:outline-none"
              >
                {SCHEMES.map((s) => (
                  <option key={s} value={s}>
                    {s === 'password' ? t('vps.authPassword') : t('vps.authKey')}
                  </option>
                ))}
              </select>
            </div>
          </div>

          {authMethod === 'password' ? (
            <div className="space-y-1.5">
              <Label>{t('vps.password')}</Label>
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={isEdit && host?.has_password ? '••••••••' : ''}
                autoComplete="off"
              />
            </div>
          ) : (
            <>
              <div className="space-y-1.5">
                <Label>{t('vps.privateKey')}</Label>
                <textarea
                  value={privateKey}
                  onChange={(e) => setPrivateKey(e.target.value)}
                  spellCheck={false}
                  rows={5}
                  placeholder={
                    isEdit && host?.has_key
                      ? '•••••• (stored — leave blank to keep)'
                      : '-----BEGIN OPENSSH PRIVATE KEY-----'
                  }
                  className="w-full resize-y rounded-[var(--radius-md)] border border-input bg-transparent px-3.5 py-2.5 font-mono text-[11px] focus-visible:border-foreground focus-visible:outline-none"
                />
              </div>
              <div className="space-y-1.5">
                <Label>{t('vps.passphrase')}</Label>
                <Input
                  type="password"
                  value={passphrase}
                  onChange={(e) => setPassphrase(e.target.value)}
                  placeholder={t('vps.passphrasePh')}
                  autoComplete="off"
                />
              </div>
            </>
          )}

          {testResult ? (
            <div
              className={cn(
                'm-rise flex items-center gap-2 rounded-[var(--radius-lg)] border bg-card px-4 py-3 text-xs',
                testResult.ok
                  ? 'border-border text-foreground'
                  : 'border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] text-destructive',
              )}
            >
              {testResult.ok ? (
                <CheckCircle className="size-4 text-[var(--success)]" weight="fill" />
              ) : (
                <XCircle className="size-4" weight="fill" />
              )}
              <span className="min-w-0 break-words">
                {testResult.ok ? t('vps.testOk', { as: testResult.as ?? '' }) : t('vps.testFail', { error: testResult.error ?? '' })}
              </span>
            </div>
          ) : null}
          {error ? (
            <p className="m-rise rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-4 py-3 text-sm text-destructive">
              {error}
            </p>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <Button variant="outline" onClick={() => void test()} loading={testing} disabled={!valid}>
            {testing ? t('vps.testing') : t('vps.test')}
          </Button>
          <DialogClose asChild>
            <Button variant="ghost">{t('common.close')}</Button>
          </DialogClose>
          <Button onClick={() => void submit()} loading={busy} disabled={!valid}>
            {t('vps.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
