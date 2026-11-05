import { useEffect, useState } from 'react'
import { ArrowCounterClockwise, FloppyDisk, ListChecks, Sparkle, User } from '@phosphor-icons/react'
import { get, post } from '@/lib/api'
import { useI18n, type MessageKey } from '@/lib/i18n'
import { PageLayout } from '@/components/layout/PageLayout'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
} from '@/components/ui/primitives'

type PersonaFile = 'soul' | 'agents' | 'user'

interface PersonaResp {
  content: string
  path: string
  unset?: boolean
}

interface FileState {
  text: string
  saved: string
  path: string
  unset: boolean
  loading: boolean
  busy: boolean
  msg?: string
  error?: string
}

const FILES: {
  id: PersonaFile
  icon: typeof Sparkle
  tab: MessageKey
  title: MessageKey
  desc: MessageKey
  placeholder: MessageKey
  note: MessageKey
  reset: MessageKey
  resetHint: MessageKey
  saved: MessageKey
}[] = [
  {
    id: 'soul',
    icon: Sparkle,
    tab: 'persona.tab.soul',
    title: 'persona.soul.title',
    desc: 'soul.desc',
    placeholder: 'soul.placeholder',
    note: 'soul.note',
    reset: 'soul.reset',
    resetHint: 'soul.resetHint',
    saved: 'soul.saved',
  },
  {
    id: 'agents',
    icon: ListChecks,
    tab: 'persona.tab.agents',
    title: 'persona.agents.title',
    desc: 'persona.agents.desc',
    placeholder: 'persona.agents.placeholder',
    note: 'persona.agents.note',
    reset: 'persona.clear',
    resetHint: 'persona.clearHint',
    saved: 'persona.saved',
  },
  {
    id: 'user',
    icon: User,
    tab: 'persona.tab.user',
    title: 'persona.user.title',
    desc: 'persona.user.desc',
    placeholder: 'persona.user.placeholder',
    note: 'persona.user.note',
    reset: 'persona.clear',
    resetHint: 'persona.clearHint',
    saved: 'persona.saved',
  },
]

const blank: FileState = { text: '', saved: '', path: '', unset: false, loading: true, busy: false }

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

/**
 * The Persona page: the three global files that shape the agent. SOUL.md is
 * who it is (the agent usually writes this during its first conversation),
 * AGENTS.md its standing instructions, and USER.md what it knows about the
 * user. Each tab is a plain editor; edits survive switching tabs.
 */
export default function SoulPage() {
  const { t } = useI18n()
  const [tab, setTab] = useState<PersonaFile>('soul')
  const [files, setFiles] = useState<Record<PersonaFile, FileState>>({ soul: blank, agents: blank, user: blank })

  const patch = (id: PersonaFile, p: Partial<FileState>) => setFiles((prev) => ({ ...prev, [id]: { ...prev[id], ...p } }))

  useEffect(() => {
    for (const { id } of FILES) {
      get<PersonaResp>(`/persona/${id}`)
        .then((d) => patch(id, { text: d.content, saved: d.content, path: d.path, unset: !!d.unset, loading: false }))
        .catch((e: unknown) => patch(id, { error: errText(e), loading: false }))
    }
  }, [])

  const save = async (id: PersonaFile, content: string, savedKey: MessageKey) => {
    patch(id, { busy: true, msg: undefined, error: undefined })
    try {
      const d = await post<PersonaResp>(`/persona/${id}`, { content })
      patch(id, { text: d.content, saved: d.content, path: d.path, unset: !!d.unset, busy: false, msg: t(savedKey) })
    } catch (e) {
      patch(id, { busy: false, error: errText(e) })
    }
  }

  return (
    <PageLayout>
      <div className="mx-auto w-full max-w-2xl space-y-4">
        <Tabs value={tab} onValueChange={(v) => setTab(v as PersonaFile)}>
          <TabsList aria-label={t('soul.title')}>
            {FILES.map((f) => {
              const s = files[f.id]
              return (
                <TabsTrigger key={f.id} value={f.id}>
                  <f.icon className="size-3.5" />
                  {t(f.tab)}
                  {s.text !== s.saved ? (
                    <span aria-label={t('persona.unsaved')} className="size-1.5 rounded-full bg-foreground/60" />
                  ) : null}
                </TabsTrigger>
              )
            })}
          </TabsList>

          {FILES.map((f) => {
            const s = files[f.id]
            const dirty = s.text !== s.saved
            return (
              <TabsContent key={f.id} value={f.id}>
                <Card>
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                      <f.icon className="size-4 text-muted-foreground" />
                      {t(f.title)}
                    </CardTitle>
                    <CardDescription>{t(f.desc)}</CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-3">
                    {f.id === 'soul' && s.unset ? (
                      <div className="m-rise rounded-[var(--radius-lg)] border border-border bg-raised px-3 py-2 text-xs text-muted-foreground">
                        {t('soul.unsetHint')}
                      </div>
                    ) : null}

                    <Textarea
                      value={s.text}
                      onChange={(e) => patch(f.id, { text: e.target.value, msg: undefined })}
                      rows={16}
                      spellCheck={false}
                      disabled={s.loading}
                      placeholder={t(f.placeholder)}
                      aria-label={t(f.title)}
                      className="w-full resize-y font-mono text-[13px] leading-relaxed"
                    />

                    {s.error ? <p className="m-rise text-xs text-[var(--destructive)]">{s.error}</p> : null}
                    {s.msg ? <p className="m-rise text-xs text-[var(--success)]">{s.msg}</p> : null}

                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        size="sm"
                        onClick={() => save(f.id, s.text, f.saved)}
                        loading={s.busy}
                        disabled={!dirty || s.loading}
                        className="gap-1.5"
                      >
                        <FloppyDisk className="size-4" />
                        {t('common.save')}
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => save(f.id, '', f.saved)}
                        disabled={s.busy || s.loading || (f.id !== 'soul' && !s.saved)}
                        className="gap-1.5"
                        title={t(f.resetHint)}
                      >
                        <ArrowCounterClockwise className="size-4" />
                        {t(f.reset)}
                      </Button>
                      {s.path ? (
                        <span className="ml-auto truncate font-mono text-[11px] text-muted-foreground" title={s.path}>
                          {s.path}
                        </span>
                      ) : null}
                    </div>
                    <p className="text-[11px] leading-relaxed text-muted-foreground">{t(f.note)}</p>
                  </CardContent>
                </Card>
              </TabsContent>
            )
          })}
        </Tabs>
      </div>
    </PageLayout>
  )
}
