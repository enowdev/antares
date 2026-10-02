import { useState } from 'react'
import {
  ArrowSquareOut,
  CheckCircle,
  Eye,
  EyeSlash,
  FloppyDisk,
  GoogleLogo,
  Lock,
  Warning,
} from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
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
} from '@/components/ui/primitives'

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
export function GoogleOsintCard({ cookieEdited }: { cookieEdited?: string }) {
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
          <GoogleLogo className="size-4 text-muted-foreground" />
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
          <div className="m-rise space-y-2">
            {result.accounts.length > 1 ? (
              <p className="eyebrow">{t('osintg.pick')}</p>
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
                      'flex w-full items-center gap-2 rounded-[var(--radius-md)] border px-3 py-2 text-left text-xs transition-[border-color,background-color] duration-200',
                      active ? 'border-foreground' : 'border-border hover:border-line hover:bg-raised',
                    )}
                  >
                    <span
                      className={cn(
                        'flex size-4 shrink-0 items-center justify-center rounded-full border',
                        active ? 'border-[var(--success)] bg-[var(--success)] text-background' : 'border-line',
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
          <p className="m-rise rounded-[var(--radius-md)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] px-3 py-2 text-xs text-destructive">
            {result.error || t('osintg.notConnected')}
          </p>
        ) : null}

        <div className="rounded-[var(--radius-lg)] border border-border px-4 py-3 text-xs leading-relaxed text-muted-foreground">
          <p className="eyebrow mb-2">{t('osintg.howto')}</p>
          <ol className="list-decimal space-y-1 pl-4 marker:font-mono marker:text-dim">
            <li>
              <a
                href="https://chromewebstore.google.com/detail/cookie-editor/hlkenndednhfkekhgcdicdfddnkalmdm"
                target="_blank"
                rel="noreferrer noopener"
                className="inline-flex items-center gap-1 text-foreground underline decoration-line underline-offset-4 transition-colors hover:decoration-foreground"
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
export function DashboardPasswordCard() {
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
          <Lock className="size-4 text-muted-foreground" />
          {t('dashpw.title')}
        </CardTitle>
        <CardDescription>
          {locked ? t('dashpw.descLocked') : t('dashpw.descOpen')}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {locked ? (
          <div className="flex flex-col gap-2">
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

        <div className="flex flex-col gap-2">
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
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground"
              aria-label={t('config.reveal')}
            >
              {show ? <EyeSlash className="size-4" /> : <Eye className="size-4" />}
            </button>
          </div>
        </div>

        <div className="flex flex-col gap-2">
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

        {error ? (
          <p className="m-rise rounded-[var(--radius-md)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] px-3 py-2 text-xs text-destructive">
            {error}
          </p>
        ) : null}
        {done ? <p className="m-rise text-xs text-[var(--success)]">{done}</p> : null}

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
        <p className="text-xs leading-relaxed text-muted-foreground">{t('dashpw.note')}</p>
      </CardContent>
    </Card>
  )
}
