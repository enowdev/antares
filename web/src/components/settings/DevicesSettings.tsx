import { useState } from 'react'
import { DeviceMobile, Desktop, Laptop, TerminalWindow } from '@phosphor-icons/react'
import { del } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n, type MessageKey } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { Badge, Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'

export interface Device {
  id: string
  name: string
  platform: string
  created_at: string
  last_seen_at: string | null
  revoked_at: string | null
  current: boolean
}

/** Whether a Settings search should surface the Devices card. */
export function devicesMatchQuery(t: (key: MessageKey) => string, query: string): boolean {
  return ['devices', 'device', 'token', t('devices.title')].some((s) => s.toLowerCase().includes(query))
}

const PLATFORM_KEYS: Record<string, MessageKey> = {
  'desktop-macos': 'devices.platform.macos',
  'desktop-windows': 'devices.platform.windows',
  'desktop-linux': 'devices.platform.linux',
  cli: 'devices.platform.cli',
  other: 'devices.platform.other',
}

function PlatformIcon({ platform }: { platform: string }) {
  const cls = 'size-4 shrink-0 text-muted-foreground'
  if (platform === 'desktop-macos') return <Laptop className={cls} />
  if (platform.startsWith('desktop-')) return <Desktop className={cls} />
  if (platform === 'cli') return <TerminalWindow className={cls} />
  return <DeviceMobile className={cls} />
}

/**
 * Settings → Devices: clients paired with a device token (the desktop app,
 * `antares device pair`). Lists them and revokes one. Pairing itself happens
 * from the app or the CLI, never here.
 */
export function DevicesSettings() {
  const { t, lang } = useI18n()
  const state = useApi<{ devices: Device[] }>('/devices')
  const [pending, setPending] = useState<Device | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const fmt = (iso: string) =>
    new Date(iso).toLocaleString(lang, { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' })

  const revoke = async () => {
    if (!pending) return
    setBusy(true)
    setError(undefined)
    try {
      await del(`/devices/${encodeURIComponent(pending.id)}`)
      setPending(null)
      state.reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setPending(null)
    } finally {
      setBusy(false)
    }
  }

  const devices = state.data?.devices ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Laptop className="size-4 text-muted-foreground" />
          {t('devices.title')}
        </CardTitle>
        <CardDescription>{t('devices.desc')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {state.loading && !state.data ? (
          <SkeletonList count={2} />
        ) : state.error && !state.data ? (
          <p className="text-xs text-destructive">{state.error.message}</p>
        ) : devices.length === 0 ? (
          <div className="rounded-[var(--radius-lg)] border border-dashed border-border px-4 py-5 text-sm text-muted-foreground">
            <p>{t('devices.empty')}</p>
            <p className="mt-1.5 font-mono text-[11px] text-dim">antares device pair</p>
          </div>
        ) : (
          <ul className="divide-y divide-border overflow-hidden rounded-[var(--radius-lg)] border border-border">
            {devices.map((d) => {
              const revoked = d.revoked_at !== null
              return (
                <li
                  key={d.id}
                  data-reveal
                  className={cn(
                    'flex flex-col gap-2 px-3.5 py-3 transition-colors duration-200 hover:bg-raised sm:flex-row sm:items-center sm:gap-3',
                    revoked && 'opacity-60',
                  )}
                >
                  <div className="flex min-w-0 flex-1 items-start gap-2.5">
                    <PlatformIcon platform={d.platform} />
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className={cn('truncate text-[13px] font-medium', revoked && 'line-through')}>{d.name}</span>
                        {d.current ? <Badge>{t('devices.thisDevice')}</Badge> : null}
                        {revoked ? <Badge variant="outline">{t('devices.revoked')}</Badge> : null}
                      </div>
                      <p className="mt-0.5 text-xs text-muted-foreground">
                        {t(PLATFORM_KEYS[d.platform] ?? 'devices.platform.other')}
                        {' · '}
                        {t('devices.created', { when: fmt(d.created_at) })}
                        {' · '}
                        {revoked
                          ? t('devices.revokedAt', { when: fmt(d.revoked_at as string) })
                          : d.last_seen_at
                            ? t('devices.lastSeen', { when: fmt(d.last_seen_at) })
                            : t('devices.neverSeen')}
                      </p>
                    </div>
                  </div>
                  {revoked ? null : (
                    <Button
                      size="sm"
                      variant="outline"
                      className="self-start sm:self-center"
                      onClick={() => setPending(d)}
                    >
                      {t('common.revoke')}
                    </Button>
                  )}
                </li>
              )
            })}
          </ul>
        )}

        {error ? (
          <p className="m-rise rounded-[var(--radius-md)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] px-3 py-2 text-xs text-destructive">
            {error}
          </p>
        ) : null}
      </CardContent>

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setPending(null)
        }}
        title={t('devices.revokeTitle', { name: pending?.name ?? '' })}
        description={pending?.current ? t('devices.revokeSelf') : t('devices.revokeDesc')}
        confirmLabel={t('common.revoke')}
        loading={busy}
        onConfirm={() => void revoke()}
      />
    </Card>
  )
}
