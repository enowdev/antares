import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Eye, EyeSlash, Warning } from '@phosphor-icons/react'
import { get, post, ApiError } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { useTheme } from '@/lib/theme'
import { Button } from '@/components/ui/button'
import { Input, Label } from '@/components/ui/primitives'
import { Brand } from '@/components/brand/BrandMark'
import { AgentField } from '@/components/brand/AgentField'

interface AuthStatus {
  password_required?: boolean
  authenticated?: boolean
}

/**
 * The dashboard login. Shown only when a dashboard password is configured. On
 * success the server sets an HTTP-only session cookie and we return to the app.
 */
export default function LoginPage() {
  const { t } = useI18n()
  const navigate = useNavigate()
  // Outside the app shell, so apply the saved theme here too.
  useTheme()
  const [password, setPassword] = useState('')
  const [show, setShow] = useState(false)
  // A desktop app's sign-in link that expired or was already used lands here.
  const [error, setError] = useState<string | undefined>(() =>
    new URLSearchParams(window.location.search).get('handoff') === 'expired'
      ? t('login.handoffExpired')
      : undefined,
  )
  const [busy, setBusy] = useState(false)

  // If a login is not required (or already done), do not linger on this page.
  useEffect(() => {
    let alive = true
    get<AuthStatus>('/auth/status')
      .then((s) => {
        if (!alive) return
        if (!s.password_required || s.authenticated) navigate('/', { replace: true })
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [navigate])

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!password || busy) return
    setBusy(true)
    setError(undefined)
    try {
      await post('/auth/login', { password })
      navigate('/', { replace: true })
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t('login.failed'))
      setBusy(false)
    }
  }

  return (
    <div className="relative flex min-h-dvh flex-col items-center justify-center overflow-hidden px-4 py-10 sm:px-6">
      <AgentField />
      <div className="relative flex w-full max-w-sm flex-col items-center text-center">
        <Brand className="m-rise" />
        <p className="eyebrow m-rise mt-8" style={{ animationDelay: '80ms' }}>
          {t('login.eyebrow')}
        </p>
        <h1 className="m-rise mt-3 text-[clamp(20px,2vw,26px)] font-medium leading-tight tracking-[-0.5px]"
          style={{ animationDelay: '140ms' }}
        >
          {t('login.title')}
        </h1>
        <p className="m-rise mt-2 max-w-[420px] text-sm leading-relaxed text-muted-foreground"
          style={{ animationDelay: '200ms' }}
        >
          {t('login.desc')}
        </p>

        <form
          onSubmit={submit}
          className="tp-panel m-rise mt-8 w-full space-y-4 rounded-[var(--radius-lg)] border border-border bg-card p-5 text-left"
          style={{ animationDelay: '280ms' }}
        >
          <div className="flex flex-col gap-2">
            <Label htmlFor="dash-password">{t('login.password')}</Label>
            <div className="relative">
              <Input
                id="dash-password"
                type={show ? 'text' : 'password'}
                value={password}
                autoFocus
                autoComplete="current-password"
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                className="pr-9"
              />
              <button
                type="button"
                onClick={() => setShow((v) => !v)}
                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground"
                aria-label={show ? t('login.hide') : t('login.show')}
              >
                {show ? <EyeSlash size={16} /> : <Eye size={16} />}
              </button>
            </div>
          </div>
          {error ? (
            <p
              role="alert"
              className="m-rise flex items-start gap-2 rounded-[var(--radius-md)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] px-3 py-2 text-xs text-destructive"
            >
              <Warning className="mt-px size-3.5 shrink-0" weight="fill" />
              <span className="min-w-0 break-words">{error}</span>
            </p>
          ) : null}
          <Button type="submit" className="w-full" disabled={!password || busy}>
            {busy ? t('login.signingIn') : t('login.signIn')}
          </Button>
        </form>
      </div>
    </div>
  )
}
