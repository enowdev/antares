import { useCallback, useState, type ReactNode } from 'react'
import {
  ArrowSquareOut,
  ArrowsClockwise,
  Download,
  EnvelopeSimple,
  Globe,
  Info,
  Key,
  Play,
  Plus,
  ShieldCheck,
  Stop,
  Trash,
  X,
} from '@phosphor-icons/react'
import { PageLayout } from '@/components/layout/PageLayout'
import { usePageActions } from '@/components/layout/PageChrome'
import { Card, CardContent, CardDescription, CardHeader, CardTitle, Badge, Input, Label, Switch, EmptyState } from '@/components/ui/primitives'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { SensitiveGate } from '@/components/ui/SensitiveGate'
import { useApi } from '@/lib/hooks'
import { post, del } from '@/lib/api'
import { useI18n } from '@/lib/i18n'

interface SocialStatus {
  enabled: boolean
  encryption_ready: boolean
  imap_configured: boolean
  imap_host: string
  imap_port: number
  imap_username: string
  browser: { enabled: boolean; state: string; error: string }
  autopilot_enabled: boolean
  accounts: SocialAccount[]
}

interface SocialAccount {
  id: string
  platform: string
  display_name: string
  username: string
  profile_url: string
  status: string
  rag_namespace: string
  skill_name: string
  last_checked_at: string | null
  created_at: string
  updated_at: string
  has_password: boolean
  has_recovery: boolean
}

export default function SocialMediaPage() {
  const { t } = useI18n()
  const { data: status, reload, loading } = useApi<SocialStatus>('/social/status')
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [showRecoveryKey, setShowRecoveryKey] = useState(false)
  const [recoveryKey, setRecoveryKey] = useState('')
  const [showImap, setShowImap] = useState(false)
  const [showAdd, setShowAdd] = useState(false)

  const handleAction = useCallback(async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key); setError('')
    try { await fn(); await reload() }
    catch (e) { setError((e as Error).message) }
    finally { setBusy('') }
  }, [reload])

  // Accounts are the page's main content, so adding one lives in the hub
  // header. Shown only once status loads: the endpoint is behind the
  // dashboard-password gate, and storing credentials needs the encryption key.
  const canAdd = !!status?.encryption_ready
  usePageActions(
    status ? (
      <Button size="sm" className="gap-1.5" onClick={() => setShowAdd(true)} disabled={!canAdd}>
        <Plus className="size-4" />
        {t('social.accounts.add')}
      </Button>
    ) : null,
    [t, !!status, canAdd],
  )

  if (loading && !status) return <PageLayout><Skeleton className="h-32 w-full" /></PageLayout>

  const s = status ?? null
  const accountCount = s?.accounts?.length ?? 0

  return (
    <PageLayout>
      {error && <Card className="border-destructive/50"><CardContent className="py-3 text-sm text-destructive">{error}</CardContent></Card>}

      {s && !s.encryption_ready && (
        <Card className="border-warning/50">
          <CardHeader>
            <CardTitle className="text-sm">{t('social.onboarding.encryptionRequired')}</CardTitle>
          </CardHeader>
          <CardContent className="flex items-center justify-between">
            <p className="text-sm text-muted-foreground">{t('social.onboarding.description')}</p>
            <Button size="sm" loading={busy === 'encryption'} onClick={() => handleAction('encryption', async () => {
              const res = await post<{ recovery_key: string }>('/social/encryption/setup', {})
              setRecoveryKey(res.recovery_key); setShowRecoveryKey(true)
            })}>{t('social.onboarding.generateKey')}</Button>
          </CardContent>
        </Card>
      )}

      {showRecoveryKey && (
        <Card className="border-success/50">
          <CardHeader>
            <CardTitle className="text-sm">{t('social.onboarding.recoveryKeyTitle')}</CardTitle>
            <CardDescription>{t('social.onboarding.recoveryKeyDesc')}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <code className="block break-all rounded-[var(--radius-sm)] bg-muted p-3 text-sm">{recoveryKey}</code>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => {
                const blob = new Blob([recoveryKey], { type: 'text/plain' })
                const url = URL.createObjectURL(blob)
                const a = document.createElement('a'); a.href = url; a.download = 'antares-recovery-key.txt'; a.click()
                URL.revokeObjectURL(url)
              }}><Download className="mr-1.5 size-4" />{t('social.onboarding.recoveryKeyDownload')}</Button>
              <Button size="sm" variant="ghost" onClick={() => setShowRecoveryKey(false)}><X className="mr-1.5 size-4" />{t('common.close')}</Button>
            </div>
            <p className="text-xs text-muted-foreground">{t('social.onboarding.restartRequired')}</p>
          </CardContent>
        </Card>
      )}

      <SensitiveGate>
        {/* The three things accounts depend on, side by side at one height so
            their state reads at a glance. Each column is status plus one action. */}
        <Card className="grid overflow-hidden lg:grid-cols-3">
          <SetupItem
            icon={<EnvelopeSimple className="size-4" />}
            title={t('social.gmail.title')}
            badge={s?.imap_configured
              ? <Badge>{t('social.gmail.configured')}</Badge>
              : <Badge variant="secondary">{t('social.gmail.notConfigured')}</Badge>}
            description={t('social.gmail.description')}
          >
            {s?.imap_configured ? (
              <>
                <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={`${s.imap_host}:${s.imap_port}`}>
                  {s.imap_username}
                </span>
                <Button size="sm" variant="outline" onClick={() => setShowImap(true)}>{t('social.gmail.edit')}</Button>
              </>
            ) : (
              <Button size="sm" onClick={() => setShowImap(true)}>{t('social.gmail.configure')}</Button>
            )}
          </SetupItem>

          <SetupItem
            icon={<Globe className="size-4" />}
            title={t('social.browser.title')}
            badge={<BrowserBadge state={s?.browser?.state ?? 'disabled'} />}
            description={t('social.browser.description')}
            error={s?.browser?.error}
          >
            {s?.browser?.state !== 'running' ? (
              <Button size="sm" loading={busy === 'browser-start'} onClick={() => handleAction('browser-start', () => post('/social/browser/start', {}))} disabled={!s?.encryption_ready}>
                <Play className="mr-1.5 size-4" />{t('social.browser.start')}
              </Button>
            ) : (
              <Button size="sm" variant="outline" loading={busy === 'browser-stop'} onClick={() => handleAction('browser-stop', () => post('/social/browser/stop', {}))}>
                <Stop className="mr-1.5 size-4" />{t('social.browser.stop')}
              </Button>
            )}
          </SetupItem>

          <SetupItem
            icon={<ArrowsClockwise className="size-4" />}
            title={t('social.autopilot.title')}
            description={t('social.autopilot.description')}
          >
            <label className="flex cursor-pointer items-center gap-2.5 text-xs font-medium text-muted-foreground">
              <Switch checked={s?.autopilot_enabled ?? false} disabled={busy === 'autopilot'} onCheckedChange={(v) => handleAction('autopilot', () => post('/social/autopilot', { enabled: v }))} />
              {t('social.autopilot.toggle')}
            </label>
          </SetupItem>
        </Card>

        <section className="space-y-3">
          <h2 className="flex items-baseline gap-2 text-sm font-semibold">
            {t('social.accounts.title')}
            <span className="text-xs font-normal text-muted-foreground">{accountCount}</span>
          </h2>
          {accountCount > 0 ? (
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {s!.accounts.map((a) => <AccountCard key={a.id} acct={a} onRemoved={reload} />)}
            </div>
          ) : (
            <EmptyState
              title={t('social.accounts.empty')}
              description={t('social.accounts.emptyDesc')}
              action={canAdd ? (
                <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setShowAdd(true)}>
                  <Plus className="size-4" />
                  {t('social.accounts.add')}
                </Button>
              ) : undefined}
            />
          )}
        </section>

        {/* Mounted only while open so its fields start from the current
            settings rather than from whatever had loaded at first render. */}
        {showImap ? (
          <IMAPDialog
            open
            defaults={s}
            onOpenChange={setShowImap}
            onDone={() => { setShowImap(false); reload() }}
          />
        ) : null}
        <AddAccountDialog
          open={showAdd}
          onOpenChange={setShowAdd}
          onDone={() => { setShowAdd(false); reload() }}
        />
      </SensitiveGate>
    </PageLayout>
  )
}

function SetupItem({
  icon,
  title,
  badge,
  description,
  error,
  children,
}: {
  icon: ReactNode
  title: string
  badge?: ReactNode
  description: string
  error?: string
  children: ReactNode
}) {
  return (
    <div className="flex min-w-0 flex-col gap-2 border-border p-4 [&:not(:first-child)]:border-t lg:[&:not(:first-child)]:border-l lg:[&:not(:first-child)]:border-t-0">
      {/* Fixed height: a column without a badge keeps its title level with the others. */}
      <div className="flex h-6 min-w-0 items-center gap-2">
        <span className="shrink-0 text-muted-foreground">{icon}</span>
        <h3 className="min-w-0 truncate text-sm font-medium">{title}</h3>
        {badge ? <span className="ml-auto shrink-0">{badge}</span> : null}
      </div>
      <p className="text-xs leading-relaxed text-muted-foreground">{description}</p>
      {error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
      <div className="mt-auto flex min-h-8 min-w-0 items-center gap-2 pt-1">{children}</div>
    </div>
  )
}

function BrowserBadge({ state }: { state: string }) {
  const { t } = useI18n()
  const variant = state === 'running' ? 'default' : state === 'error' ? 'destructive' : 'secondary'
  return <Badge variant={variant as never}>{t(`social.browser.state.${state}` as never) ?? state}</Badge>
}

function IMAPDialog({ open, defaults, onOpenChange, onDone }: { open: boolean; defaults: SocialStatus | null; onOpenChange: (open: boolean) => void; onDone: () => void }) {
  const { t } = useI18n()
  const [host, setHost] = useState(defaults?.imap_host || 'imap.gmail.com')
  const [port, setPort] = useState(String(defaults?.imap_port || 993))
  const [username, setUsername] = useState(defaults?.imap_username ?? '')
  const [password, setPassword] = useState('')
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState('')
  const [saving, setSaving] = useState(false)

  const test = async () => {
    setTesting(true); setResult('')
    try {
      const res = await post<{ ok: boolean; error?: string; inbox_count?: number }>('/social/imap/test', { host, port: Number(port), username, password })
      setResult(res.ok ? t('social.gmail.testSuccess').replace('{count}', String(res.inbox_count ?? 0)) : t('social.gmail.testFail').replace('{error}', res.error ?? ''))
    } catch (e) { setResult((e as Error).message) } finally { setTesting(false) }
  }

  const save = async () => {
    setSaving(true)
    try { await post('/social/imap/save', { host, port: Number(port), username, password }); setPassword(''); onDone() }
    catch (e) { setResult((e as Error).message) } finally { setSaving(false) }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!saving) onOpenChange(next) }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('social.gmail.title')}</DialogTitle>
          <p className="text-xs text-muted-foreground">{t('social.gmail.description')}</p>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <div className="grid grid-cols-3 gap-3">
            <div className="col-span-2 space-y-1.5"><Label>{t('social.gmail.host')}</Label><Input value={host} onChange={(e) => setHost(e.target.value)} /></div>
            <div className="space-y-1.5"><Label>{t('social.gmail.port')}</Label><Input value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" /></div>
          </div>
          <div className="space-y-1.5"><Label>{t('social.gmail.username')}</Label><Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="email@example.com" autoComplete="off" /></div>
          <div className="space-y-1.5"><Label>{t('social.gmail.password')}</Label><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" /></div>
          <details className="rounded-[var(--radius-sm)] border border-border bg-muted/50 p-3 text-xs">
            <summary className="flex cursor-pointer items-center gap-1.5 font-medium text-muted-foreground">
              <Info className="size-3.5" />
              {t('social.gmail.appPasswordTutorial')}
            </summary>
            <ol className="mt-2 list-decimal space-y-1 pl-4 text-muted-foreground">
              <li>{t('social.gmail.tutorialStep1')}</li>
              <li>{t('social.gmail.tutorialStep2')}</li>
              <li>{t('social.gmail.tutorialStep3')}</li>
              <li>{t('social.gmail.tutorialStep4')}</li>
              <li>{t('social.gmail.tutorialStep5')}</li>
            </ol>
          </details>
          {result && <p role="status" className="text-xs text-muted-foreground">{result}</p>}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={saving}>{t('common.cancel')}</Button>
          <Button variant="outline" loading={testing} onClick={test} disabled={!username || !password}>{t('social.gmail.test')}</Button>
          <Button loading={saving} onClick={save} disabled={!username}>{t('social.gmail.save')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function AccountCard({ acct, onRemoved }: { acct: SocialAccount; onRemoved: () => void }) {
  const { t } = useI18n()
  const [removing, setRemoving] = useState(false)
  const remove = async () => { setRemoving(true); try { await del(`/social/accounts/${acct.id}`); onRemoved() } catch { setRemoving(false) } }
  const name = acct.display_name || acct.username
  const initial = name.trim().charAt(0).toUpperCase() || '?'
  const connected = acct.status === 'connected'
  return (
    <Card className="flex flex-col">
      <CardContent className="flex flex-1 flex-col gap-3 p-4 sm:p-4">
        <div className="flex items-start gap-3">
          <div className="grid size-9 shrink-0 place-items-center rounded-[var(--radius-md)] bg-primary/10 text-sm font-semibold text-primary">
            {initial}
          </div>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium leading-5">{name}</p>
            <p className="truncate text-xs text-muted-foreground">
              {acct.platform.trim().toLowerCase()} · @{acct.username}
            </p>
          </div>
          <Badge variant={connected ? 'default' : 'secondary'} className="shrink-0">
            {t(`social.accounts.status.${acct.status}` as never) ?? acct.status}
          </Badge>
        </div>

        {acct.has_password || acct.has_recovery ? (
          <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
            {acct.has_password && <span className="inline-flex items-center gap-1"><Key className="size-3.5" />{t('social.accounts.hasPassword')}</span>}
            {acct.has_recovery && <span className="inline-flex items-center gap-1"><ShieldCheck className="size-3.5" />{t('social.accounts.hasRecovery')}</span>}
          </div>
        ) : null}

        <div className="mt-auto flex items-center justify-between gap-2 border-t border-border pt-3">
          {acct.profile_url ? (
            <a
              href={acct.profile_url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground transition-colors hover:text-primary"
              title={acct.profile_url}
            >
              <span className="truncate">{t('social.accounts.openProfile')}</span>
              <ArrowSquareOut className="size-3.5 shrink-0" />
            </a>
          ) : (
            <span className="text-xs text-muted-foreground">{t('social.accounts.noProfile')}</span>
          )}
          <Button size="sm" variant="ghost" className="h-8 shrink-0 px-2 text-muted-foreground hover:text-destructive" loading={removing} onClick={remove}>
            <Trash className="mr-1.5 size-3.5" />{t('social.accounts.remove')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function AddAccountDialog({ open, onOpenChange, onDone }: { open: boolean; onOpenChange: (open: boolean) => void; onDone: () => void }) {
  const { t } = useI18n()
  const [platform, setPlatform] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [recovery, setRecovery] = useState('')
  const [profileUrl, setProfileUrl] = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  const save = async () => {
    setSaving(true); setErr('')
    try { await post('/social/accounts', { platform, username, password, recovery_codes: recovery, profile_url: profileUrl, status: 'connected' }); onDone() }
    catch (e) { setErr((e as Error).message) } finally { setSaving(false) }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!saving) onOpenChange(next) }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('social.accounts.add')}</DialogTitle>
          <p className="text-xs text-muted-foreground">Store an account for agents to manage. Credentials are encrypted locally.</p>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5"><Label>{t('social.accounts.platform')}</Label><Input autoFocus value={platform} onChange={(e) => setPlatform(e.target.value)} placeholder="instagram" /></div>
            <div className="space-y-1.5"><Label>{t('social.accounts.username')}</Label><Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="username" /></div>
          </div>
          <div className="space-y-1.5"><Label>{t('social.accounts.password')}</Label><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" /></div>
          <div className="space-y-1.5"><Label>{t('social.accounts.recovery')}</Label><Input value={recovery} onChange={(e) => setRecovery(e.target.value)} placeholder="Optional recovery codes" /></div>
          <div className="space-y-1.5"><Label>{t('social.accounts.profileUrl')}</Label><Input type="url" value={profileUrl} onChange={(e) => setProfileUrl(e.target.value)} placeholder="https://..." /></div>
          {err && <p role="alert" className="rounded-[var(--radius-sm)] border border-destructive/30 bg-destructive/5 p-2.5 text-xs text-destructive">{err}</p>}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={saving}>{t('common.cancel')}</Button>
          <Button loading={saving} onClick={save} disabled={!platform.trim() || !username.trim()}>{t('social.accounts.save')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
