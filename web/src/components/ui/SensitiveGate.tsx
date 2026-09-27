import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { LockKey } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { EmptyState } from '@/components/ui/primitives'
import { Button } from '@/components/ui/button'

/**
 * useDashboardLocked reports whether a dashboard password is set. null while
 * loading. On error it assumes locked (fail open on the UI so a status hiccup
 * doesn't wrongly block; the server still enforces the real gate).
 */
export function useDashboardLocked(): boolean | null {
  const [locked, setLocked] = useState<boolean | null>(null)
  useEffect(() => {
    get<{ password_required: boolean }>('/auth/status')
      .then((d) => setLocked(!!d.password_required))
      .catch(() => setLocked(true))
  }, [])
  return locked
}

/**
 * Blocks a page that stores or uses sensitive credentials (VPS, proxies, the
 * Google OSINT cookie) until a dashboard password is set. Mirrors the server's
 * 428 gate so the user is told up front rather than after filling a form.
 * Renders children only when a password is configured.
 */
export function SensitiveGate({ children }: { children: React.ReactNode }) {
  const { t } = useI18n()
  const locked = useDashboardLocked()

  if (locked === null) return null // brief; avoids a flash of the gate
  if (locked) return <>{children}</>

  return (
    <EmptyState
      icon={<LockKey className="size-6" />}
      title={t('sensitive.needPasswordTitle')}
      description={t('sensitive.needPasswordDesc')}
      action={
        <Button asChild size="sm">
          <Link to="/system/settings">{t('sensitive.setPassword')}</Link>
        </Button>
      }
    />
  )
}
