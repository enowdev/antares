import { useState } from 'react'
import {
  CaretDown,
  CheckCircle,
  PlugsConnected,
  Plus,
  Storefront,
  Trash,
  XCircle,
} from '@phosphor-icons/react'
import { del, post } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { mcpToolsOrEmpty } from '@/lib/mcpPayload'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
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
import { SkeletonList } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { usePageActions } from '@/components/layout/PageChrome'
import { HubDialog } from '@/components/hub/HubDialog'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'

interface McpTool {
  name: string
  description: string
}

interface McpServer {
  name: string
  connected: boolean
  error?: string
  tools: McpTool[] | null
}

export default function McpPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<{ enabled: boolean; servers: McpServer[] }>('/mcp')
  const [open, setOpen] = useState<string | null>(null)
  const [browsing, setBrowsing] = useState(false)
  const [adding, setAdding] = useState(false)
  const [removing, setRemoving] = useState('')
  const [toRemove, setToRemove] = useState<string | null>(null)
  const [tab, setTab] = useState<'servers' | 'docs'>('servers')

  usePageActions(
    <>
      <Button size="sm" variant="outline" onClick={() => setAdding(true)} className="gap-1.5">
        <Plus className="size-4" />
        {t('mcp.add')}
      </Button>
      <Button size="sm" onClick={() => setBrowsing(true)} className="gap-1.5">
        <Storefront className="size-4" />
        {t('hub.browse')}
      </Button>
    </>,
    [t],
  )

  const confirmRemove = async () => {
    if (!toRemove) return
    const name = toRemove
    setRemoving(name)
    try {
      await del(`/mcp/servers/${encodeURIComponent(name)}`)
      reload()
    } finally {
      setRemoving('')
      setToRemove(null)
    }
  }

  if (loading && !data) return <SkeletonList count={3} />

  const servers = (data?.servers ?? []).map((server) => ({
    ...server,
    tools: mcpToolsOrEmpty(server.tools),
  }))

  const header = (
    <Tabs value={tab} onValueChange={(v) => setTab(v as 'servers' | 'docs')}>
      <TabsList>
        <TabsTrigger value="servers">{t('mcp.tabServers')}</TabsTrigger>
        <TabsTrigger value="docs">{t('mcp.tabDocs')}</TabsTrigger>
      </TabsList>
    </Tabs>
  )

  return (
    <PageLayout header={header}>
      <HubDialog kind="mcp" open={browsing} onOpenChange={setBrowsing} onInstalled={reload} />
      <AddMcpDialog open={adding} onOpenChange={setAdding} onAdded={reload} />
      <ConfirmDialog
        open={!!toRemove}
        onOpenChange={(open) => !open && setToRemove(null)}
        title={t('mcp.removeConfirm', { name: toRemove ?? '' })}
        confirmLabel={t('common.remove')}
        loading={removing !== ''}
        onConfirm={() => void confirmRemove()}
      />

      {tab === 'docs' ? (
        <McpDocs />
      ) : (
        <>
          {!data?.enabled ? (
            <Card className="border-[var(--warning)]/40 bg-[color-mix(in_oklch,var(--warning)_10%,transparent)]">
              <CardContent className="pt-4 text-xs sm:text-sm">{t('mcp.disabled')}</CardContent>
            </Card>
          ) : null}

          {servers.length === 0 ? (
            <EmptyState
              icon={<PlugsConnected className="size-8" />}
              title={t('mcp.none')}
              description={t('mcp.noneDesc')}
              action={
                <Button size="sm" onClick={() => setBrowsing(true)} className="gap-1.5">
                  <Storefront className="size-4" />
                  {t('hub.browse')}
                </Button>
              }
            />
          ) : (
            <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
              {servers.map((s) => (
                <div
                  key={s.name}
                  className="flex flex-col rounded-[var(--radius-lg)] border border-border bg-card p-3.5"
                >
                  <div className="flex items-start gap-2">
                    {s.connected ? (
                      <CheckCircle className="mt-0.5 size-4 shrink-0 text-[var(--success)]" weight="fill" />
                    ) : (
                      <XCircle className="mt-0.5 size-4 shrink-0 text-destructive" weight="fill" />
                    )}
                    <span className="min-w-0 flex-1 truncate text-sm font-medium">{s.name}</span>
                    <button
                      onClick={() => setToRemove(s.name)}
                      disabled={removing === s.name}
                      aria-label={t('common.delete')}
                      className="shrink-0 text-muted-foreground transition-colors hover:text-destructive disabled:opacity-50"
                    >
                      <Trash className="size-4" />
                    </button>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    <Badge variant={s.connected ? 'success' : 'destructive'}>
                      {s.connected ? t('mcp.connected') : t('mcp.failed')}
                    </Badge>
                    {s.connected ? (
                      <Badge variant="outline">{t('mcp.toolCount', { n: s.tools.length })}</Badge>
                    ) : null}
                  </div>
                  {s.error ? (
                    <p className="mt-1.5 break-words text-[11px] text-destructive">{s.error}</p>
                  ) : null}
                  {s.tools.length > 0 ? (
                    <div className="mt-2.5 border-t border-border pt-2">
                      <button
                        onClick={() => setOpen(open === s.name ? null : s.name)}
                        className="flex w-full items-center gap-1.5 text-xs font-medium text-muted-foreground"
                      >
                        {t('mcp.showTools')}
                        <CaretDown
                          className={cn('size-3 transition-transform', open === s.name && 'rotate-180')}
                        />
                      </button>
                      {open === s.name ? (
                        <div className="mt-2 space-y-1.5">
                          {s.tools.map((tool) => (
                            <div key={tool.name} className="rounded-[var(--radius-sm)] border border-border p-2">
                              <p className="break-all font-mono text-[11px] font-medium">{tool.name}</p>
                              {tool.description ? (
                                <p className="mt-0.5 text-[11px] text-muted-foreground">{tool.description}</p>
                              ) : null}
                            </div>
                          ))}
                        </div>
                      ) : null}
                    </div>
                  ) : null}
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </PageLayout>
  )
}

function McpDocs() {
  const { t } = useI18n()
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('mcp.howto')}</CardTitle>
        <CardDescription>{t('mcp.howtoDesc')}</CardDescription>
      </CardHeader>
      <CardContent>
        <pre className="overflow-x-auto rounded-[var(--radius-sm)] bg-muted/50 p-3 font-mono text-[11px] leading-relaxed">
{`mcp:
  enabled: true
  servers:
    filesystem:
      enabled: true
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/you/data"]
    remote:
      enabled: true
      transport: http
      url: https://example.com/mcp
      headers:
        Authorization: "Bearer …"`}
        </pre>
        <p className="mt-3 text-xs text-muted-foreground">{t('mcp.docsHint')}</p>
      </CardContent>
    </Card>
  )
}

function AddMcpDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onAdded: () => void
}) {
  const { t } = useI18n()
  const [transport, setTransport] = useState<'stdio' | 'http'>('stdio')
  const [name, setName] = useState('')
  const [command, setCommand] = useState('')
  const [args, setArgs] = useState('')
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const reset = () => {
    setTransport('stdio')
    setName('')
    setCommand('')
    setArgs('')
    setUrl('')
    setError(undefined)
  }

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const body =
        transport === 'stdio'
          ? {
              name,
              transport,
              command,
              args: args.split(/\s+/).filter(Boolean),
            }
          : { name, transport, url }
      const r = await post<{ ok: boolean; error?: string }>('/mcp/servers', body)
      if (!r.ok) {
        setError(r.error ?? t('mcp.addFailed'))
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

  const valid = name.trim() && (transport === 'stdio' ? command.trim() : url.trim())

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
          <DialogTitle>{t('mcp.addTitle')}</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <div className="grid gap-1.5">
            <Label htmlFor="mcp-name">{t('mcp.fieldName')}</Label>
            <Input
              id="mcp-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-server"
              autoFocus
            />
          </div>

          <div className="grid gap-1.5">
            <Label>{t('mcp.fieldTransport')}</Label>
            <div className="inline-flex w-fit rounded-[var(--radius-sm)] border border-border p-0.5">
              {(['stdio', 'http'] as const).map((tp) => (
                <button
                  key={tp}
                  onClick={() => setTransport(tp)}
                  className={cn(
                    'rounded-[calc(var(--radius-sm)-2px)] px-3 py-1 text-xs font-medium transition-colors',
                    transport === tp
                      ? 'bg-primary text-primary-foreground'
                      : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  {tp}
                </button>
              ))}
            </div>
          </div>

          {transport === 'stdio' ? (
            <>
              <div className="grid gap-1.5">
                <Label htmlFor="mcp-cmd">{t('mcp.fieldCommand')}</Label>
                <Input
                  id="mcp-cmd"
                  value={command}
                  onChange={(e) => setCommand(e.target.value)}
                  placeholder="npx"
                  className="font-mono text-xs"
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="mcp-args">{t('mcp.fieldArgs')}</Label>
                <Input
                  id="mcp-args"
                  value={args}
                  onChange={(e) => setArgs(e.target.value)}
                  placeholder="-y @modelcontextprotocol/server-filesystem /home/you"
                  className="font-mono text-xs"
                />
              </div>
            </>
          ) : (
            <div className="grid gap-1.5">
              <Label htmlFor="mcp-url">{t('mcp.fieldUrl')}</Label>
              <Input
                id="mcp-url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://example.com/mcp"
                className="font-mono text-xs"
              />
            </div>
          )}

          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
          <Button size="sm" disabled={!valid} loading={busy} onClick={() => void submit()}>
            {t('mcp.add')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
