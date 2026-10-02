import { useState } from 'react'
import { ArrowsLeftRight, ClockCounterClockwise } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useI18n, type MessageKey } from '@/lib/i18n'
import { backupName } from '@/lib/migrate'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { Badge, Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/primitives'
import { MigrateFlow, type AppliedMigration } from './MigrateFlow'

/** Whether a Settings search should surface the Migrate section. */
export function migrateMatchQuery(t: (key: MessageKey) => string, query: string): boolean {
  return ['migrate', 'import', 'hermes', 'openclaw', t('migrate.nav'), t('migrate.title')].some((s) =>
    s.toLowerCase().includes(query),
  )
}

interface PastMigration {
  source: string
  name: string
  backup: string
  at: string
  applied: number
  undone?: boolean
}

// The API has no list of past migrations yet, so this keeps the ones applied
// in this browser session. Storage can be unavailable; the list is a
// convenience and the page works without it.
const HISTORY_KEY = 'antares.migrate.history'

function readHistory(): PastMigration[] {
  try {
    const raw = sessionStorage.getItem(HISTORY_KEY)
    const list = raw ? (JSON.parse(raw) as PastMigration[]) : []
    return Array.isArray(list) ? list : []
  } catch {
    return []
  }
}

function writeHistory(list: PastMigration[]) {
  try {
    sessionStorage.setItem(HISTORY_KEY, JSON.stringify(list))
  } catch {
    /* storage unavailable */
  }
}

/** Settings › Migrate: the migrate flow any time, plus this session's imports. */
export function MigrateSettings() {
  const { t, locale } = useI18n()
  const [history, setHistory] = useState<PastMigration[]>(readHistory)

  const save = (list: PastMigration[]) => {
    setHistory(list)
    writeHistory(list)
  }

  const record = ({ report, target }: AppliedMigration) => {
    if (!report.backup) return
    save([
      {
        source: target.source,
        name: target.name,
        backup: report.backup,
        at: new Date().toISOString(),
        applied: report.applied?.length ?? 0,
      },
      ...history.filter((h) => h.backup !== report.backup),
    ])
  }

  const markUndone = (backup: string) =>
    save(history.map((h) => (h.backup === backup ? { ...h, undone: true } : h)))

  const [pending, setPending] = useState<PastMigration | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const undo = async () => {
    if (!pending) return
    setBusy(true)
    setError(undefined)
    try {
      await post('/migrate/undo', { backup: backupName(pending.backup) })
      markUndone(pending.backup)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
      setPending(null)
    }
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ArrowsLeftRight className="size-4 text-muted-foreground" />
            {t('migrate.title')}
          </CardTitle>
          <CardDescription>{t('migrate.desc')}</CardDescription>
        </CardHeader>
        <CardContent>
          <MigrateFlow onApplied={record} onUndone={({ report }) => markUndone(report.backup)} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ClockCounterClockwise className="size-4 text-muted-foreground" />
            {t('migrate.history')}
          </CardTitle>
          <CardDescription>{t('migrate.historyDesc')}</CardDescription>
        </CardHeader>
        <CardContent>
          {history.length === 0 ? (
            <p className="rounded-[var(--radius-lg)] border border-dashed border-border px-4 py-4 text-sm text-muted-foreground">
              {t('migrate.historyEmpty')}
            </p>
          ) : (
            <ul className="divide-y divide-border overflow-hidden rounded-[var(--radius-lg)] border border-border">
              {history.map((h) => (
                <li
                  key={h.backup}
                  data-reveal
                  className="flex flex-col gap-2 px-3.5 py-3 sm:flex-row sm:items-center sm:gap-3"
                >
                  <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="text-[13px] font-medium">{h.name}</span>
                    <span className="text-xs text-muted-foreground">
                      {new Date(h.at).toLocaleString(locale, {
                        day: 'numeric',
                        month: 'short',
                        hour: '2-digit',
                        minute: '2-digit',
                      })}
                      {' · '}
                      {t('migrate.historyApplied', { n: h.applied })}
                    </span>
                    {h.undone ? <Badge variant="outline">{t('migrate.undoneBadge')}</Badge> : null}
                  </div>
                  <p className="break-all font-mono text-[11px] text-dim">{h.backup}</p>
                  </div>
                  {h.undone ? null : (
                    <Button variant="outline" size="sm" className="self-start sm:self-center" onClick={() => setPending(h)}>
                      {t('migrate.undo')}
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          )}
          {error ? <p role="alert" className="mt-3 text-xs text-destructive">{error}</p> : null}
          <p className="mt-3 font-mono text-[11px] text-dim">antares migrate undo &lt;backup-dir&gt;</p>
        </CardContent>
      </Card>

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setPending(null)
        }}
        title={t('migrate.undoTitle')}
        description={t('migrate.undoDesc')}
        confirmLabel={t('migrate.undo')}
        loading={busy}
        onConfirm={() => void undo()}
      />
    </div>
  )
}
