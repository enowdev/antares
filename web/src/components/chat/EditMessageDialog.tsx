import { useEffect, useState } from 'react'
import { ArrowUp, FileX, Warning } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
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
import { Textarea } from '@/components/ui/primitives'

interface Change {
  path: string
  externally_changed: boolean
  will_delete: boolean
}

/**
 * Edit a past user message: change the text and re-send the conversation from
 * there. Everything after the message is dropped. If the turns since that
 * message changed files, offer to revert them to how they were just before it
 * (files edited outside the session are skipped and flagged).
 */
export function EditMessageDialog({
  open,
  onOpenChange,
  sessionId,
  messageId,
  initialText,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  sessionId?: string
  messageId: string
  initialText: string
  // onSubmit(newText, revert) applies the edit and re-sends.
  onSubmit: (text: string, revert: boolean) => void
}) {
  const { t } = useI18n()
  const [text, setText] = useState(initialText)
  const [revert, setRevert] = useState(true)
  const [changes, setChanges] = useState<Change[]>([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!open) return
    setText(initialText)
    setRevert(true)
    setChanges([])
    if (!sessionId || !messageId) return
    setLoading(true)
    get<{ changes: Change[] }>(
      `/sessions/${sessionId}/edit-preview?message_id=${encodeURIComponent(messageId)}`,
    )
      .then((d) => setChanges(d.changes ?? []))
      .catch(() => setChanges([]))
      .finally(() => setLoading(false))
  }, [open, sessionId, messageId, initialText])

  const revertable = changes.filter((c) => !c.externally_changed)
  const external = changes.filter((c) => c.externally_changed)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('edit.title')}</DialogTitle>
          <DialogDescription>{t('edit.desc')}</DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-3">
          <Textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={3}
            autoFocus
            className="max-h-60 min-h-16 w-full resize-none text-sm"
          />

          {loading ? (
            <p className="text-[11px] text-muted-foreground">{t('edit.checking')}</p>
          ) : changes.length ? (
            <div className="m-rise space-y-2 border border-border p-3">
              <label className="flex items-start gap-2">
                <input
                  type="checkbox"
                  checked={revert}
                  onChange={(e) => setRevert(e.target.checked)}
                  className="mt-0.5"
                />
                <span className="text-xs">
                  <span className="font-medium">{t('edit.revertLabel', { n: revertable.length })}</span>
                  <span className="block text-[11px] text-muted-foreground">
                    {t('edit.revertHint')}
                  </span>
                </span>
              </label>

              <div className="max-h-40 space-y-1 overflow-y-auto">
                {revertable.map((c) => (
                  <div key={c.path} className="flex items-center gap-1.5 font-mono text-[11px]">
                    <FileX className="size-3 shrink-0 text-[var(--warning)]" />
                    <span className="truncate" title={c.path}>
                      {c.path}
                    </span>
                    {c.will_delete ? (
                      <span className="shrink-0 text-[9px] uppercase text-muted-foreground">
                        {t('edit.willDelete')}
                      </span>
                    ) : null}
                  </div>
                ))}
                {external.map((c) => (
                  <div
                    key={c.path}
                    className="flex items-center gap-1.5 font-mono text-[11px] text-muted-foreground"
                    title={t('edit.externalTip')}
                  >
                    <Warning className="size-3 shrink-0" />
                    <span className="truncate">{c.path}</span>
                    <span className="shrink-0 text-[9px] uppercase">{t('edit.skipped')}</span>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <p className="text-[11px] text-muted-foreground">{t('edit.noChanges')}</p>
          )}
        </DialogBody>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            {t('common.close')}
          </Button>
          <Button
            size="sm"
            disabled={!text.trim()}
            onClick={() => onSubmit(text.trim(), revert && revertable.length > 0)}
            className={cn('gap-1.5')}
          >
            <ArrowUp className="size-4" weight="bold" />
            {t('edit.resend')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
