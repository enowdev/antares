import { useCallback, useEffect, useState } from 'react'
import {
  ArrowSquareOut,
  CheckCircle,
  DownloadSimple,
  MagnifyingGlass,
  Warning,
} from '@phosphor-icons/react'
import { get, post } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge, Input } from '@/components/ui/primitives'
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SkeletonList } from '@/components/ui/skeleton'

export interface HubEntry {
  id: string
  kind: string
  name: string
  summary: string
  source: string
  tags?: string[]
  homepage?: string
  author?: string
  installed: boolean
  setup?: string
  needs_keys?: string[]
  // plugins only: the executable that will run, shown before install.
  command?: string
  args?: string[]
  hooks?: string[]
}

/**
 * Browse and install from the hub. Skills and MCP servers differ only in which
 * endpoints they hit and what a successful install has to say afterwards, so
 * one dialog serves both rather than two that drift apart.
 */
export function HubDialog({
  kind,
  open,
  onOpenChange,
  onInstalled,
}: {
  kind: 'skills' | 'mcp' | 'plugins'
  open: boolean
  onOpenChange: (v: boolean) => void
  onInstalled: () => void
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [entries, setEntries] = useState<HubEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState<string>()

  const search = useCallback(
    async (q: string) => {
      setLoading(true)
      setError(undefined)
      try {
        const path = kind === 'skills' ? '/hub/skills' : kind === 'mcp' ? '/hub/mcp' : '/hub/plugins'
        const r = await get<{
          skills?: HubEntry[]
          servers?: HubEntry[]
          plugins?: HubEntry[]
          error?: string
        }>(`${path}?q=${encodeURIComponent(q)}`)
        setEntries(r.skills ?? r.servers ?? r.plugins ?? [])
        setError(r.error)
      } catch (e) {
        setError((e as Error).message)
        setEntries([])
      } finally {
        setLoading(false)
      }
    },
    [kind],
  )

  useEffect(() => {
    if (!open) return
    setQuery('')
    setNote(undefined)
    void search('')
  }, [open, search])

  // Debounced: a repository lookup goes over the network, so firing per
  // keystroke would rate-limit the user out of their own search.
  useEffect(() => {
    if (!open) return
    const timer = setTimeout(() => void search(query), 400)
    return () => clearTimeout(timer)
  }, [query, open, search])

  const install = async (entry: HubEntry) => {
    setBusy(entry.id)
    setNote(undefined)
    setError(undefined)
    try {
      const path =
        kind === 'skills'
          ? '/hub/skills/install'
          : kind === 'mcp'
            ? '/hub/mcp/install'
            : '/hub/plugins/install'
      const r = await post<{ ok: boolean; error?: string; missing_keys?: string[] }>(path, {
        id: entry.id,
      })
      if (!r.ok) {
        setError(r.error ?? t('hub.installFailed'))
        return
      }
      if (r.missing_keys?.length) {
        setNote(t('hub.needsKeys', { keys: r.missing_keys.join(', ') }))
      } else {
        setNote(t('hub.installed', { name: entry.name }))
      }
      onInstalled()
      void search(query)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {kind === 'skills'
              ? t('hub.skillsTitle')
              : kind === 'mcp'
                ? t('hub.mcpTitle')
                : t('hub.pluginsTitle')}
          </DialogTitle>
          <DialogDescription>
            {kind === 'skills'
              ? t('hub.skillsDesc')
              : kind === 'mcp'
                ? t('hub.mcpDesc')
                : t('hub.pluginsDesc')}
          </DialogDescription>
        </DialogHeader>

        <DialogBody>
          <div className="relative">
            <MagnifyingGlass className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={
                kind === 'skills'
                  ? t('hub.searchSkills')
                  : kind === 'mcp'
                    ? t('hub.searchMcp')
                    : t('hub.searchPlugins')
              }
              className="pl-9"
            />
          </div>

          {note ? (
            <div className="m-rise flex items-start gap-2 border border-[color-mix(in_oklch,var(--success)_45%,var(--border))] bg-card px-4 py-3 text-xs">
              <CheckCircle className="mt-0.5 size-4 shrink-0 text-[var(--success)]" weight="fill" />
              <span className="min-w-0">{note}</span>
            </div>
          ) : null}

          {error ? (
            <div className="m-rise flex items-start gap-2 border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-4 py-3 text-xs text-destructive">
              <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
              <span className="min-w-0 break-words">{error}</span>
            </div>
          ) : null}

          {loading && entries.length === 0 ? (
            <SkeletonList count={4} />
          ) : entries.length === 0 ? (
            <p className="py-6 text-center text-xs text-muted-foreground">{t('hub.noMatches')}</p>
          ) : (
            <div className="space-y-2">
              {entries.map((e) => (
                <div
                  key={e.id}
                  className={cn(
                    'flex items-start gap-3 border border-border p-3.5 transition-[border-color,background-color] duration-200',
                    e.installed ? 'bg-raised/60' : 'hover:border-line hover:bg-raised',
                  )}
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="text-[13px] font-medium">{e.name}</span>
                      <Badge variant="outline">{e.source}</Badge>
                      {e.installed ? (
                        <Badge variant="success">
                          <CheckCircle className="size-3" weight="fill" />
                          {t('hub.alreadyInstalled')}
                        </Badge>
                      ) : null}
                      {e.needs_keys?.length ? (
                        <Badge variant="warning">{t('hub.keyRequired')}</Badge>
                      ) : null}
                    </div>
                    <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{e.summary}</p>
                    {kind === 'plugins' && e.command ? (
                      <p className="mt-1.5 break-all border border-border bg-background px-2 py-1 font-mono text-[11px] text-foreground">
                        $ {e.command}
                        {e.args?.length ? ' ' + e.args.join(' ') : ''}
                        {e.hooks?.length ? `  · ${e.hooks.join(', ')}` : ''}
                      </p>
                    ) : null}
                    <div className="mt-1 flex flex-wrap items-center gap-2">
                      <span className="break-all font-mono text-[11px] text-dim">
                        {e.id}
                      </span>
                      {e.homepage ? (
                        <a
                          href={e.homepage}
                          target="_blank"
                          rel="noreferrer noopener"
                          className="inline-flex items-center gap-1 font-mono text-[11px] text-foreground underline decoration-line underline-offset-4 transition-colors hover:decoration-foreground"
                        >
                          {t('hub.homepage')}
                          <ArrowSquareOut className="size-3" />
                        </a>
                      ) : null}
                    </div>
                  </div>
                  <Button
                    size="sm"
                    variant={e.installed ? 'outline' : 'default'}
                    disabled={e.installed}
                    loading={busy === e.id}
                    onClick={() => install(e)}
                    className="shrink-0 gap-1.5"
                  >
                    <DownloadSimple className="size-4" />
                    {e.installed ? t('hub.added') : t('hub.add')}
                  </Button>
                </div>
              ))}
            </div>
          )}

          {kind === 'skills' ? (
            <p className="text-xs leading-relaxed text-muted-foreground">{t('hub.sourceHint')}</p>
          ) : null}
          {kind === 'plugins' ? (
            <div className="flex items-start gap-2 border border-[color-mix(in_oklch,var(--warning)_45%,var(--border))] bg-card px-4 py-3 text-xs leading-relaxed">
              <Warning className="mt-0.5 size-4 shrink-0 text-[var(--warning)]" weight="fill" />
              <span className="min-w-0">{t('hub.pluginsWarn')}</span>
            </div>
          ) : null}
        </DialogBody>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" size="sm">
              {t('common.close')}
            </Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
