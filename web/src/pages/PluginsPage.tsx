import { useState } from 'react'
import { ArrowClockwise, Plus, PuzzlePiece, Storefront, Warning } from '@phosphor-icons/react'
import { post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { usePageActions } from '@/components/layout/PageChrome'
import { Button } from '@/components/ui/button'
import {
  Badge,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  EmptyState,
  Input,
  Label,
  Switch,
  Tabs,
  TabsList,
  TabsTrigger,
} from '@/components/ui/primitives'
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { HubDialog } from '@/components/hub/HubDialog'
import { SkeletonList } from '@/components/ui/skeleton'

interface Plugin {
  name: string
  description: string
  version: string
  command: string
  hooks?: string[]
  dir: string
  enabled: boolean
  error?: string
}

export default function PluginsPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<{
    enabled: boolean
    plugins: Plugin[]
    dirs?: string[]
  }>('/plugins')
  const [busy, setBusy] = useState('')
  const [tab, setTab] = useState<'plugins' | 'docs'>('plugins')
  const [adding, setAdding] = useState(false)
  const [browsing, setBrowsing] = useState(false)

  const refresh = async () => {
    setBusy('*')
    try {
      await post('/plugins/reload')
      reload()
    } finally {
      setBusy('')
    }
  }

  usePageActions(
    <>
      <Button size="sm" variant="outline" onClick={() => void refresh()} className="gap-1.5">
        <ArrowClockwise className="size-4" />
        {t('plugins.rescan')}
      </Button>
      <Button size="sm" variant="outline" onClick={() => setBrowsing(true)} className="gap-1.5">
        <Storefront className="size-4" />
        {t('hub.browse')}
      </Button>
      <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
        <Plus className="size-4" />
        {t('plugins.add')}
      </Button>
    </>,
    [t],
  )

  const toggle = async (name: string, enabled: boolean) => {
    setBusy(name)
    try {
      await post(`/plugins/${encodeURIComponent(name)}/toggle`, { enabled })
      reload()
    } finally {
      setBusy('')
    }
  }

  if (loading && !data) return <SkeletonList count={3} />

  const plugins = data?.plugins ?? []

  const header = (
    <Tabs value={tab} onValueChange={(v) => setTab(v as 'plugins' | 'docs')}>
      <TabsList>
        <TabsTrigger value="plugins">{t('plugins.tabPlugins')}</TabsTrigger>
        <TabsTrigger value="docs">{t('plugins.tabDocs')}</TabsTrigger>
      </TabsList>
    </Tabs>
  )

  return (
    <PageLayout header={header}>
      <HubDialog kind="plugins" open={browsing} onOpenChange={setBrowsing} onInstalled={reload} />
      <AddPluginDialog open={adding} onOpenChange={setAdding} onAdded={reload} />
      {tab === 'docs' ? (
        <PluginDocs />
      ) : (
        <>
          {!data?.enabled ? (
            <p className="border border-[color-mix(in_oklch,var(--warning)_45%,var(--border))] bg-card px-4 py-3 text-sm text-muted-foreground">
              {t('plugins.disabled')}
            </p>
          ) : null}

          {plugins.length === 0 ? (
            <EmptyState
              icon={<PuzzlePiece className="size-8" />}
              title={t('plugins.none')}
              description={t('plugins.noneDesc', { dir: data?.dirs?.[0] ?? '~/.antares/plugins' })}
            />
          ) : (
            <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
              {plugins.map((p) => (
                <div
                  key={p.name}
                  data-reveal
                  className={cn(
                    'flex flex-col border bg-card p-4 transition-[border-color,background-color] duration-200',
                    p.error
                      ? 'border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))]'
                      : 'border-border hover:border-line',
                  )}
                >
                  <div className="flex items-start gap-2">
                    <PuzzlePiece
                      className={cn('mt-0.5 size-4 shrink-0', p.error ? 'text-destructive' : 'text-muted-foreground')}
                    />
                    <span className="min-w-0 flex-1 truncate text-[15px] font-medium tracking-[-0.2px]">{p.name}</span>
                    {!p.error ? (
                      <Switch
                        checked={p.enabled}
                        disabled={busy === p.name || busy === '*'}
                        onCheckedChange={(v) => toggle(p.name, v)}
                        aria-label={`${t('common.enable')} ${p.name}`}
                        className="shrink-0"
                      />
                    ) : null}
                  </div>
                  {p.description ? (
                    <p className="mt-1.5 line-clamp-2 text-xs leading-relaxed text-muted-foreground">{p.description}</p>
                  ) : null}
                  {p.error ? (
                    <p className="mt-2 break-words font-mono text-[11px] leading-relaxed text-destructive">{p.error}</p>
                  ) : null}
                  <div className="mt-3 flex flex-wrap gap-1.5">
                    {p.version ? <Badge variant="outline" className="font-mono font-normal">{p.version}</Badge> : null}
                    {p.error ? (
                      <Badge variant="destructive">
                        <Warning className="size-3" weight="fill" />
                        {t('plugins.broken')}
                      </Badge>
                    ) : null}
                    {p.hooks?.map((h) => (
                      <Badge key={h} variant="secondary" className="font-mono font-normal">
                        {h}
                      </Badge>
                    ))}
                  </div>
                  <p className="-mx-4 mt-3.5 break-all border-t border-border px-4 pt-2.5 font-mono text-[11px] text-dim">
                    {p.dir}
                    {p.command ? ` · ${p.command}` : ''}
                  </p>
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </PageLayout>
  )
}

function PluginDocs() {
  const { t } = useI18n()
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('plugins.howto')}</CardTitle>
        <CardDescription>{t('plugins.howtoDesc')}</CardDescription>
      </CardHeader>
      <CardContent>
        <pre className="overflow-x-auto border border-border bg-background p-3.5 font-mono text-[11px] leading-relaxed">
{`# ~/.antares/plugins/audit/plugin.yaml
name: audit
description: Log every terminal command
command: ./run.sh
hooks: [pre_tool_call]

# ~/.antares/plugins/audit/run.sh
#!/bin/sh
cat >> ~/antares-audit.log
echo '{}'`}
        </pre>
        <p className="mt-3 text-xs text-muted-foreground">{t('plugins.docsHint')}</p>
      </CardContent>
    </Card>
  )
}

const HOOKS = ['pre_tool_call', 'post_tool_call', 'session_start', 'session_end', 'turn_end'] as const

function AddPluginDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onAdded: () => void
}) {
  const { t } = useI18n()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [command, setCommand] = useState('')
  const [args, setArgs] = useState('')
  const [hooks, setHooks] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const reset = () => {
    setName('')
    setDescription('')
    setCommand('')
    setArgs('')
    setHooks([])
    setError(undefined)
  }

  const toggleHook = (h: string) =>
    setHooks((prev) => (prev.includes(h) ? prev.filter((x) => x !== h) : [...prev, h]))

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const r = await post<{ ok: boolean; error?: string }>('/plugins', {
        name,
        description,
        command,
        args: args.split(/\s+/).filter(Boolean),
        hooks,
      })
      if (!r.ok) {
        setError(r.error ?? t('plugins.addFailed'))
        return
      }
      onAdded()
      reset()
      onOpenChange(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const valid = name.trim() && command.trim() && hooks.length > 0

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) reset()
        onOpenChange(v)
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('plugins.addTitle')}</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <div className="grid gap-1.5">
            <Label htmlFor="pl-name">{t('plugins.fieldName')}</Label>
            <Input
              id="pl-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="audit"
              autoFocus
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="pl-desc">{t('plugins.fieldDesc')}</Label>
            <Input
              id="pl-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t('plugins.fieldDescHint')}
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="pl-cmd">{t('plugins.fieldCommand')}</Label>
            <Input
              id="pl-cmd"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="./run.sh"
              className="font-mono text-xs"
            />
            <p className="text-[11px] text-muted-foreground">{t('plugins.fieldCommandHint')}</p>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="pl-args">{t('plugins.fieldArgs')}</Label>
            <Input
              id="pl-args"
              value={args}
              onChange={(e) => setArgs(e.target.value)}
              placeholder="--verbose"
              className="font-mono text-xs"
            />
          </div>
          <div className="grid gap-1.5">
            <Label>{t('plugins.fieldHooks')}</Label>
            <div className="flex flex-wrap gap-1.5">
              {HOOKS.map((h) => (
                <button
                  key={h}
                  onClick={() => toggleHook(h)}
                  className={cn(
                    'rounded-full border px-3 py-1.5 font-mono text-[11px] transition-colors duration-200',
                    hooks.includes(h)
                      ? 'border-transparent bg-nav-active text-foreground'
                      : 'border-border text-muted-foreground hover:text-foreground',
                  )}
                >
                  {h}
                </button>
              ))}
            </div>
          </div>

          <div className="flex items-start gap-2 border border-border bg-raised p-3 text-[11px] leading-relaxed text-muted-foreground">
            <PuzzlePiece className="mt-0.5 size-4 shrink-0" />
            <span className="min-w-0">{t('plugins.addHint')}</span>
          </div>

          {error ? <p className="m-rise text-xs text-destructive">{error}</p> : null}
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          <Button size="sm" disabled={!valid} loading={busy} onClick={() => void submit()}>
            {t('plugins.add')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
