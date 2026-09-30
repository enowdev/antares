import { useEffect, useState } from 'react'
import { ArrowCounterClockwise, FloppyDisk, Sparkle } from '@phosphor-icons/react'
import { get, post } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle, Textarea } from '@/components/ui/primitives'

interface SoulResp {
  soul: string
  unset: boolean
}

/**
 * The Soul page: view and hand-edit the agent's identity file (SOUL.md). The
 * agent usually fills this in itself during its first conversation; this lets
 * the user shape it directly, or reset it to re-run that interview.
 */
export default function SoulPage() {
  const { t } = useI18n()
  const [text, setText] = useState('')
  const [saved, setSaved] = useState('')
  const [unset, setUnset] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<string>()
  const [error, setError] = useState<string>()

  const load = () => {
    get<SoulResp>('/soul')
      .then((d) => {
        setText(d.soul)
        setSaved(d.soul)
        setUnset(d.unset)
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false))
  }
  useEffect(load, [])

  const save = async (content: string) => {
    setBusy(true)
    setMsg(undefined)
    setError(undefined)
    try {
      const d = await post<SoulResp & { ok: boolean }>('/soul', { soul: content })
      setSaved(content)
      setUnset(!!d.unset)
      setMsg(t('soul.saved'))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const dirty = text !== saved

  return (
    <PageLayout>
      <div className="mx-auto w-full max-w-2xl space-y-4">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Sparkle className="size-4 text-muted-foreground" />
              {t('soul.title')}
            </CardTitle>
            <CardDescription>{t('soul.desc')}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {unset ? (
              <div className="m-rise border border-border bg-raised px-3 py-2 text-xs text-muted-foreground">
                {t('soul.unsetHint')}
              </div>
            ) : null}

            <Textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={16}
              spellCheck={false}
              disabled={loading}
              placeholder={t('soul.placeholder')}
              className="w-full resize-y font-mono text-[13px] leading-relaxed"
            />

            {error ? <p className="m-rise text-xs text-[var(--destructive)]">{error}</p> : null}
            {msg ? <p className="m-rise text-xs text-[var(--success)]">{msg}</p> : null}

            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" onClick={() => save(text)} loading={busy} disabled={!dirty} className="gap-1.5">
                <FloppyDisk className="size-4" />
                {t('common.save')}
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => save('')}
                disabled={busy}
                className="gap-1.5"
                title={t('soul.resetHint')}
              >
                <ArrowCounterClockwise className="size-4" />
                {t('soul.reset')}
              </Button>
            </div>
            <p className="text-[11px] leading-relaxed text-muted-foreground">{t('soul.note')}</p>
          </CardContent>
        </Card>
      </div>
    </PageLayout>
  )
}
