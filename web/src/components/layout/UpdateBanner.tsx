import { useEffect, useState } from 'react'
import { ArrowClockwise, CheckCircle, Copy, DownloadSimple, Warning } from '@phosphor-icons/react'
import { get, streamPost, type StreamEvent } from '@/lib/api'
import { copyText } from '@/lib/clipboard'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Markdown } from '@/components/chat/Markdown'

interface UpdateInfo {
  current: string
  latest: string
  available: boolean
  notes: string
  url: string
}

/**
 * A quiet "update available" row for the sidebar footer. It checks the latest
 * release when the dashboard opens; if a newer version exists it shows here with
 * a Changelog button that opens a dialog offering Cancel / Update now. Running
 * the update streams the installer's output, and falls back to showing the
 * manual command if the in-place upgrade can't run.
 */
export function UpdateBanner({ compact }: { compact?: boolean }) {
  const { t } = useI18n()
  const [info, setInfo] = useState<UpdateInfo | null>(null)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    let alive = true
    get<UpdateInfo>('/update/check')
      .then((d) => alive && d.available && setInfo(d))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])

  if (!info?.available) return null

  return (
    <>
      {compact ? (
        <button
          onClick={() => setOpen(true)}
          title={`${t('update.available')} · ${info.current} → ${info.latest}`}
          aria-label={t('update.available')}
          className="m-fade relative flex h-9 w-full items-center justify-center border border-border bg-transparent transition-[border-color,background-color] duration-200 hover:border-line hover:bg-raised"
        >
          <DownloadSimple className="size-4 text-foreground" />
          <span className="absolute right-1.5 top-1.5 size-1.5 rounded-full bg-[var(--success)]" aria-hidden />
        </button>
      ) : (
        <button
          onClick={() => setOpen(true)}
          className="m-fade flex w-full items-center gap-2.5 border border-border bg-transparent px-3 py-2 text-left transition-[border-color,background-color] duration-200 hover:border-line hover:bg-raised"
        >
          <DownloadSimple className="size-4 shrink-0 text-foreground" />
          <span className="min-w-0 flex-1">
            <span className="block text-xs font-medium text-foreground">{t('update.available')}</span>
            <span className="block truncate font-mono text-[11px] tabular-nums text-muted-foreground">
              {info.current} → {info.latest}
            </span>
          </span>
        </button>
      )}
      <UpdateDialog open={open} onOpenChange={setOpen} info={info} />
    </>
  )
}

function UpdateDialog({
  open,
  onOpenChange,
  info,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  info: UpdateInfo
}) {
  const { t } = useI18n()
  // idle → running (streaming installer) → ok | manual (show command) | error
  const [phase, setPhase] = useState<'idle' | 'running' | 'ok' | 'manual' | 'error'>('idle')
  const [log, setLog] = useState('')
  const [manualCmd, setManualCmd] = useState('')
  const [copied, setCopied] = useState(false)

  // Reset when the dialog reopens.
  useEffect(() => {
    if (open) {
      setPhase('idle')
      setLog('')
      setManualCmd('')
    }
  }, [open])

  const runUpdate = () => {
    setPhase('running')
    setLog('')
    streamPost(
      '/update/run',
      {},
      (e: StreamEvent) => {
        const msg = String((e as { message?: string }).message ?? '')
        switch (e.type) {
          case 'log':
            setLog((l) => (l + msg).slice(-8000))
            break
          case 'ok':
            setPhase('ok')
            setLog((l) => l + '\n' + msg)
            break
          case 'manual':
            setManualCmd(msg)
            setPhase((p) => (p === 'error' ? p : 'manual'))
            break
          case 'error':
            setPhase('error')
            setLog((l) => l + '\n' + msg)
            break
        }
      },
      () => setPhase((p) => (p === 'running' ? 'error' : p)),
    )
  }

  const copyCmd = async () => {
    if (await copyText(manualCmd)) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('update.changelogTitle', { version: info.latest })}</DialogTitle>
          <DialogDescription>
            {t('update.from', { current: info.current, latest: info.latest })}
          </DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-3">
          {phase === 'idle' ? (
            <div className="max-h-72 overflow-y-auto border border-border bg-transparent p-4 text-sm">
              {info.notes ? (
                <Markdown content={info.notes} />
              ) : (
                <p className="text-muted-foreground">{t('update.noNotes')}</p>
              )}
            </div>
          ) : null}

          {phase === 'running' || phase === 'ok' || phase === 'error' ? (
            <pre className="max-h-72 overflow-auto border border-border bg-background p-3 font-mono text-[11px] leading-relaxed">
              {log || t('update.starting')}
            </pre>
          ) : null}

          {phase === 'ok' ? (
            <p className="m-rise flex items-center gap-1.5 text-xs text-[var(--success)]">
              <CheckCircle className="size-4" weight="fill" />
              {t('update.done')}
            </p>
          ) : null}

          {phase === 'manual' || (phase === 'error' && manualCmd) ? (
            <div className="m-rise space-y-1.5">
              <p className="flex items-center gap-1.5 text-xs text-[var(--warning)]">
                <Warning className="size-4" />
                {t('update.manualHint')}
              </p>
              <div className="flex items-center gap-1.5">
                <code className="min-w-0 flex-1 truncate border border-border bg-background px-2.5 py-1.5 font-mono text-[11px]">
                  {manualCmd}
                </code>
                <Button size="icon-sm" variant="outline" onClick={copyCmd} aria-label={t('common.copy')}>
                  {copied ? <CheckCircle className="size-4 text-[var(--success)]" /> : <Copy className="size-4" />}
                </Button>
              </div>
            </div>
          ) : null}
        </DialogBody>
        <DialogFooter>
          {info.url ? (
            <a
              href={info.url}
              target="_blank"
              rel="noreferrer noopener"
              className="mr-auto self-center font-mono text-[11px] text-muted-foreground underline decoration-line underline-offset-4 transition-colors hover:text-foreground"
            >
              {t('update.viewOnGithub')}
            </a>
          ) : null}
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            {phase === 'ok' ? t('common.close') : t('update.cancel')}
          </Button>
          {phase === 'idle' ? (
            <Button size="sm" onClick={runUpdate} className="gap-1.5">
              <DownloadSimple className="size-4" />
              {t('update.now')}
            </Button>
          ) : phase === 'error' || phase === 'manual' ? (
            <Button size="sm" onClick={runUpdate} className="gap-1.5">
              <ArrowClockwise className="size-4" />
              {t('update.retry')}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
