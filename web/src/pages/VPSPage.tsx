import { useEffect, useState, type ReactNode } from 'react'
import {
  ArrowRight,
  ArrowsClockwise,
  CaretDown,
  CheckCircle,
  Cpu,
  DotsThreeVertical,
  Folder,
  FolderOpen,
  FolderPlus,
  HardDrives,
  Memory,
  Pencil,
  Plus,
  Trash,
  XCircle,
} from '@phosphor-icons/react'
import { del, get, post, put } from '@/lib/api'
import { useApi, useLocalStorage } from '@/lib/hooks'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Badge, Card, EmptyState, Input, Label } from '@/components/ui/primitives'
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
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { Spoiler } from '@/components/ui/Spoiler'
import { SensitiveGate } from '@/components/ui/SensitiveGate'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

interface VPSHost {
  id: string
  label: string
  host: string
  port: number
  username: string
  auth_method: string
  has_password: boolean
  has_key: boolean
  folder_id: string
}

interface VPSFolder {
  id: string
  name: string
  parent_id: string
}

interface Metrics {
  reachable: boolean
  error?: string
  hostname?: string
  os?: string
  kernel?: string
  uptime?: string
  cpu_cores?: number
  cpu_percent?: number
  load1?: number
  load5?: number
  load15?: number
  mem_total_mb?: number
  mem_used_mb?: number
  mem_percent?: number
  swap_total_mb?: number
  swap_used_mb?: number
  swap_percent?: number
  disk_total_gb?: number
  disk_used_gb?: number
  disk_percent?: number
  processes?: number
  top_proc?: string[]
}

type FolderDialogState = { mode: 'add'; parentId: string } | { mode: 'rename'; folder: VPSFolder }
type MoveItem = { type: 'folder'; item: VPSFolder } | { type: 'host'; item: VPSHost }

const byName = <T extends { id: string; name: string }>(a: T, b: T) =>
  a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }) || a.id.localeCompare(b.id)
const byLabel = (a: VPSHost, b: VPSHost) =>
  a.label.localeCompare(b.label, undefined, { sensitivity: 'base' }) || a.id.localeCompare(b.id)

function folderIsSelfOrDescendant(folders: VPSFolder[], folderId: string, destinationId: string): boolean {
  const byId = new Map(folders.map((folder) => [folder.id, folder]))
  const visited = new Set<string>()
  let current = destinationId
  while (current && !visited.has(current)) {
    if (current === folderId) return true
    visited.add(current)
    current = byId.get(current)?.parent_id ?? ''
  }
  return false
}

function folderContains(folders: VPSFolder[], parentId: string, folderId: string): boolean {
  if (!parentId) return true
  const byId = new Map(folders.map((folder) => [folder.id, folder]))
  const visited = new Set<string>()
  let current = folderId
  while (current && !visited.has(current)) {
    if (current === parentId) return true
    visited.add(current)
    current = byId.get(current)?.parent_id ?? ''
  }
  return false
}

function flattenFolders(folders: VPSFolder[]) {
  const result: Array<{ folder: VPSFolder; path: string }> = []
  const visit = (parentId: string, parentPath: string, ancestors: Set<string>) => {
    folders
      .filter((folder) => folder.parent_id === parentId && !ancestors.has(folder.id))
      .sort(byName)
      .forEach((folder) => {
        const path = parentPath ? `${parentPath} / ${folder.name}` : folder.name
        result.push({ folder, path })
        visit(folder.id, path, new Set([...ancestors, folder.id]))
      })
  }
  visit('', '', new Set())
  return result
}

// Walk upward from each server so a group card can show its complete server count.
function folderServerCounts(folders: VPSFolder[], hosts: VPSHost[]): Map<string, number> {
  const parents = new Map(folders.map((folder) => [folder.id, folder.parent_id]))
  const counts = new Map<string, number>()
  for (const host of hosts) {
    const seen = new Set<string>()
    let folderId = host.folder_id
    while (folderId && !seen.has(folderId)) {
      counts.set(folderId, (counts.get(folderId) ?? 0) + 1)
      seen.add(folderId)
      folderId = parents.get(folderId) ?? ''
    }
  }
  return counts
}

export default function VPSPage() {
  const { t } = useI18n()
  const { data, loading, reload } = useApi<{ hosts: VPSHost[]; folders: VPSFolder[] }>('/vps')
  const [selectedFolder, setSelectedFolder] = useLocalStorage('antares.vps.selectedFolder', '')
  const [editing, setEditing] = useState<VPSHost | null>(null)
  const [adding, setAdding] = useState(false)
  const [toRemove, setToRemove] = useState<VPSHost | null>(null)
  const [removing, setRemoving] = useState(false)
  const [folderDialog, setFolderDialog] = useState<FolderDialogState | null>(null)
  const [folderToRemove, setFolderToRemove] = useState<VPSFolder | null>(null)
  const [removingFolder, setRemovingFolder] = useState(false)
  const [moving, setMoving] = useState<MoveItem | null>(null)
  const [operationError, setOperationError] = useState<string>()

  const hosts = data?.hosts ?? []
  const folders = data?.folders ?? []
  const selected = folders.find((folder) => folder.id === selectedFolder)
  const serverCounts = folderServerCounts(folders, hosts)
  const childGroups = folders.filter((folder) => folder.parent_id === selectedFolder).sort(byName)
  const visibleHosts = hosts.filter((host) => folderContains(folders, selectedFolder, host.folder_id)).sort(byLabel)

  const breadcrumb: VPSFolder[] = []
  if (selected) {
    const byId = new Map(folders.map((folder) => [folder.id, folder]))
    const seen = new Set<string>()
    let current: VPSFolder | undefined = selected
    while (current && !seen.has(current.id)) {
      breadcrumb.unshift(current)
      seen.add(current.id)
      current = byId.get(current.parent_id)
    }
  }

  useEffect(() => {
    if (data && selectedFolder && !data.folders.some((folder) => folder.id === selectedFolder)) setSelectedFolder('')
  }, [data, selectedFolder, setSelectedFolder])

  const confirmRemove = async () => {
    if (!toRemove) return
    setRemoving(true)
    try {
      await del(`/vps/${encodeURIComponent(toRemove.id)}`)
      reload()
    } finally {
      setRemoving(false)
      setToRemove(null)
    }
  }

  const moveFolder = async (id: string, parentId: string, index: number) => {
    if (folderIsSelfOrDescendant(folders, id, parentId)) return false
    setOperationError(undefined)
    try {
      await put(`/vps/folders/${encodeURIComponent(id)}/move`, { parent_id: parentId, index })
      reload()
      return true
    } catch (error) {
      setOperationError((error as Error).message)
      return false
    }
  }

  const moveHost = async (id: string, folderId: string, index: number) => {
    setOperationError(undefined)
    try {
      await put(`/vps/hosts/${encodeURIComponent(id)}/move`, { folder_id: folderId, index })
      reload()
      return true
    } catch (error) {
      setOperationError((error as Error).message)
      return false
    }
  }

  const confirmFolderRemove = async () => {
    if (!folderToRemove) return
    setRemovingFolder(true)
    setOperationError(undefined)
    const parentId = folderToRemove.parent_id
    try {
      await del(`/vps/folders/${encodeURIComponent(folderToRemove.id)}`)
      if (selectedFolder === folderToRemove.id) setSelectedFolder(parentId)
      reload()
    } catch (error) {
      setOperationError((error as Error).message)
    } finally {
      setRemovingFolder(false)
      setFolderToRemove(null)
    }
  }

  if (loading && !data) return <SkeletonList count={2} />

  return (
    <PageLayout>
      <SensitiveGate>
        <VPSDialog open={adding} folderId={selectedFolder} onOpenChange={setAdding} onSaved={reload} />
        <VPSDialog
          open={!!editing}
          host={editing ?? undefined}
          onOpenChange={(open) => !open && setEditing(null)}
          onSaved={reload}
        />
        <FolderDialog
          state={folderDialog}
          folders={folders}
          onOpenChange={(open) => !open && setFolderDialog(null)}
          onSaved={reload}
        />
        <MoveDialog
          moving={moving}
          folders={folders}
          hosts={hosts}
          onOpenChange={(open) => !open && setMoving(null)}
          onMoveFolder={moveFolder}
          onMoveHost={moveHost}
        />
        <ConfirmDialog
          open={!!toRemove}
          onOpenChange={(open) => !open && setToRemove(null)}
          title={t('vps.removeTitle')}
          description={t('vps.removeDesc', { label: toRemove?.label ?? '' })}
          confirmLabel={t('common.remove')}
          loading={removing}
          onConfirm={() => void confirmRemove()}
        />
        <ConfirmDialog
          open={!!folderToRemove}
          onOpenChange={(open) => !open && setFolderToRemove(null)}
          title={t('vps.deleteGroupTitle')}
          description={t('vps.deleteGroupDesc', {
            name: folderToRemove?.name ?? '',
            parent: folders.find((folder) => folder.id === folderToRemove?.parent_id)?.name ?? t('vps.ungrouped'),
          })}
          confirmLabel={t('common.delete')}
          loading={removingFolder}
          onConfirm={() => void confirmFolderRemove()}
        />

        {operationError ? (
          <p className="mb-3 rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/5 p-2.5 text-xs text-destructive">
            {t('vps.operationFailed', { error: operationError })}
          </p>
        ) : null}

        <section className="min-w-0">
          <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0 space-y-2">
              <nav className="flex min-w-0 flex-wrap items-center gap-1 text-xs text-muted-foreground" aria-label={t('vps.breadcrumbs')}>
                <button type="button" className="truncate hover:text-foreground" onClick={() => setSelectedFolder('')}>
                  {t('vps.groups')}
                </button>
                {breadcrumb.map((folder, index) => (
                  <span key={folder.id} className="flex min-w-0 items-center gap-1">
                    <span aria-hidden="true">/</span>
                    {index === breadcrumb.length - 1 ? (
                      <span className="truncate font-medium text-foreground">{folder.name}</span>
                    ) : (
                      <button type="button" className="truncate hover:text-foreground" onClick={() => setSelectedFolder(folder.id)}>
                        {folder.name}
                      </button>
                    )}
                  </span>
                ))}
              </nav>
              <div>
                <h2 className="flex items-center gap-2 text-sm font-semibold">
                  {selected ? <FolderOpen className="size-4 text-primary" weight="fill" /> : <HardDrives className="size-4 text-primary" />}
                  {selected?.name ?? t('vps.groups')}
                </h2>
                <p className="text-[11px] text-muted-foreground">
                  {t('vps.currentSummary', { groups: childGroups.length, servers: visibleHosts.length })}
                </p>
              </div>
            </div>
            <AddSplitControl onAddServer={() => setAdding(true)} onAddGroup={() => setFolderDialog({ mode: 'add', parentId: selectedFolder })} />
          </div>

          {visibleHosts.length === 0 && childGroups.length === 0 ? (
            <EmptyState
              icon={<HardDrives className="size-6" />}
              title={hosts.length === 0 && folders.length === 0 ? t('vps.none') : t('vps.groupEmpty')}
              description={hosts.length === 0 && folders.length === 0 ? t('vps.noneDesc') : t('vps.groupEmptyDesc')}
              action={<Button size="sm" onClick={() => setAdding(true)} className="gap-1.5"><Plus className="size-4" />{t('vps.add')}</Button>}
            />
          ) : (
            <div className="space-y-6">
              {childGroups.length > 0 ? (
                <div className="space-y-3">
                  <h3 className="text-xs font-semibold text-muted-foreground">{t('vps.groups')}</h3>
                  <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                    {childGroups.map((folder) => (
                      <GroupCard
                        key={folder.id}
                        folder={folder}
                        totalServers={serverCounts.get(folder.id) ?? 0}
                        directGroups={folders.filter((candidate) => candidate.parent_id === folder.id).length}
                        onOpen={() => setSelectedFolder(folder.id)}
                        onRename={() => setFolderDialog({ mode: 'rename', folder })}
                        onMove={() => setMoving({ type: 'folder', item: folder })}
                        onRemove={() => setFolderToRemove(folder)}
                      />
                    ))}
                  </div>
                </div>
              ) : null}
              {visibleHosts.length > 0 ? (
                <div className="space-y-3">
                  <h3 className="text-xs font-semibold text-muted-foreground">{t('vps.servers')}</h3>
                  <div className="grid gap-4 lg:grid-cols-2">
                    {visibleHosts.map((host) => (
                      <VPSCard
                        key={host.id}
                        host={host}
                        onEdit={() => setEditing(host)}
                        onMove={() => setMoving({ type: 'host', item: host })}
                        onRemove={() => setToRemove(host)}
                      />
                    ))}
                  </div>
                </div>
              ) : null}
            </div>
          )}
        </section>
      </SensitiveGate>
    </PageLayout>
  )
}

function AddSplitControl({ onAddServer, onAddGroup }: { onAddServer: () => void; onAddGroup: () => void }) {
  const { t } = useI18n()
  return (
    <div className="flex shrink-0 items-center">
      <Button size="sm" className="rounded-r-none border-r border-primary-foreground/25" onClick={onAddServer}>
        <Plus className="size-4" />
        {t('vps.add')}
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="icon-sm" aria-label={t('vps.addGroup')} className="rounded-l-none">
            <CaretDown className="size-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" sideOffset={5}>
          <DropdownMenuItem onSelect={onAddGroup}>
            <FolderPlus className="size-4" />
            {t('vps.addGroup')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

function GroupCard({
  folder,
  totalServers,
  directGroups,
  onOpen,
  onRename,
  onMove,
  onRemove,
}: {
  folder: VPSFolder
  totalServers: number
  directGroups: number
  onOpen: () => void
  onRename: () => void
  onMove: () => void
  onRemove: () => void
}) {
  const { t } = useI18n()
  return (
    <Card className="relative overflow-hidden transition-colors hover:border-primary/40">
      <button type="button" className="flex min-h-28 w-full flex-col justify-between p-4 pr-12 text-left" onClick={onOpen}>
        <span className="flex min-w-0 items-center gap-2">
          <Folder className="size-5 shrink-0 text-primary" weight="fill" />
          <span className="truncate text-sm font-medium">{folder.name}</span>
        </span>
        <span className="flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
          <span>{t('vps.groupServerCount', { n: totalServers })}</span>
          <span>{t('vps.groupCount', { n: directGroups })}</span>
        </span>
      </button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon-sm" className="absolute right-2 top-2" aria-label={t('vps.groupMenu', { name: folder.name })}>
            <DotsThreeVertical className="size-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" sideOffset={5}>
          <DropdownMenuItem onSelect={onRename}><Pencil className="size-4" />{t('vps.rename')}</DropdownMenuItem>
          <DropdownMenuItem onSelect={onMove}><ArrowRight className="size-4" />{t('vps.moveTo')}</DropdownMenuItem>
          <DropdownMenuItem onSelect={onRemove} className="text-destructive focus:text-destructive"><Trash className="size-4" />{t('common.delete')}</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </Card>
  )
}

function FolderDialog({
  state,
  folders,
  onOpenChange,
  onSaved,
}: {
  state: FolderDialogState | null
  folders: VPSFolder[]
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  useEffect(() => {
    if (!state) return
    setName(state.mode === 'rename' ? state.folder.name : '')
    setError(undefined)
  }, [state])

  const submit = async () => {
    if (!state || !name.trim()) return
    setBusy(true)
    setError(undefined)
    try {
      if (state.mode === 'add') {
        await post('/vps/folders', { name: name.trim(), parent_id: state.parentId })
      } else {
        await put(`/vps/folders/${encodeURIComponent(state.folder.id)}`, { name: name.trim() })
      }
      onSaved()
      onOpenChange(false)
    } catch (submitError) {
      setError((submitError as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const parentName = state?.mode === 'add' ? folders.find((folder) => folder.id === state.parentId)?.name ?? t('vps.groups') : ''

  return (
    <Dialog open={!!state} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{state?.mode === 'rename' ? t('vps.renameGroupTitle') : t('vps.addGroupTitle', { parent: parentName })}</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <div className="space-y-1.5">
            <Label htmlFor="vps-group-name">{t('vps.groupName')}</Label>
            <Input
              id="vps-group-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              onKeyDown={(event) => { if (event.key === 'Enter') void submit() }}
              placeholder={t('vps.groupNamePh')}
              autoFocus
            />
          </div>
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild><Button variant="ghost">{t('common.close')}</Button></DialogClose>
          <Button onClick={() => void submit()} disabled={!name.trim()} loading={busy}>{t('common.save')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function MoveDialog({
  moving,
  folders,
  hosts,
  onOpenChange,
  onMoveFolder,
  onMoveHost,
}: {
  moving: MoveItem | null
  folders: VPSFolder[]
  hosts: VPSHost[]
  onOpenChange: (open: boolean) => void
  onMoveFolder: (id: string, parentId: string, index: number) => Promise<boolean>
  onMoveHost: (id: string, folderId: string, index: number) => Promise<boolean>
}) {
  const { t } = useI18n()
  const [destination, setDestination] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!moving) return
    setDestination(moving.type === 'folder' ? moving.item.parent_id : moving.item.folder_id)
  }, [moving])

  const options = flattenFolders(folders).filter(({ folder }) => moving?.type !== 'folder' || !folderIsSelfOrDescendant(folders, moving.item.id, folder.id))
  const currentParent = moving?.type === 'folder' ? moving.item.parent_id : moving?.item.folder_id

  const submit = async () => {
    if (!moving) return
    setBusy(true)
    try {
      if (moving.type === 'folder') {
        const index = folders.filter((folder) => folder.parent_id === destination && folder.id !== moving.item.id).length
        if (!(await onMoveFolder(moving.item.id, destination, index))) return
      } else {
        const index = hosts.filter((host) => host.folder_id === destination && host.id !== moving.item.id).length
        if (!(await onMoveHost(moving.item.id, destination, index))) return
      }
      onOpenChange(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={!!moving} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{t('vps.moveItemTitle', { name: moving ? moving.type === 'folder' ? moving.item.name : moving.item.label : '' })}</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <div className="space-y-1.5">
            <Label htmlFor="vps-move-destination">{t('vps.destination')}</Label>
            <select
              id="vps-move-destination"
              value={destination}
              onChange={(event) => setDestination(event.target.value)}
              className="h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm"
            >
              <option value="">{t('vps.ungrouped')}</option>
              {options.map(({ folder, path }) => <option key={folder.id} value={folder.id}>{path}</option>)}
            </select>
          </div>
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild><Button variant="ghost">{t('common.close')}</Button></DialogClose>
          <Button onClick={() => void submit()} loading={busy} disabled={destination === currentParent}>{t('vps.moveHere')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function VPSCard({
  host,
  onEdit,
  onMove,
  onRemove,
}: {
  host: VPSHost
  onEdit: () => void
  onMove: () => void
  onRemove: () => void
}) {
  const { t } = useI18n()
  const [m, setM] = useState<Metrics | null>(null)
  const [loading, setLoading] = useState(true)
  const [showProc, setShowProc] = useState(false)

  const refresh = () => {
    setLoading(true)
    get<Metrics>(`/vps/${encodeURIComponent(host.id)}/metrics`)
      .then((d) => setM(d))
      .catch((e) => setM({ reachable: false, error: (e as Error).message }))
      .finally(() => setLoading(false))
  }
  useEffect(refresh, [host.id])

  return (
    <Card className="p-4">
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{host.label}</span>
            {m ? m.reachable ? (
              <Badge variant="success"><CheckCircle className="size-3" weight="fill" />{t('vps.online')}</Badge>
            ) : (
              <Badge variant="destructive"><XCircle className="size-3" weight="fill" />{t('vps.offline')}</Badge>
            ) : null}
          </div>
          <p className="truncate font-mono text-[11px] text-muted-foreground">{host.username}@<Spoiler>{host.host}</Spoiler>:{host.port}</p>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Button variant="ghost" size="icon-sm" onClick={refresh} aria-label={t('vps.refresh')} loading={loading}>
            <ArrowsClockwise className="size-4" />
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label={t('vps.serverMenu', { name: host.label })}><DotsThreeVertical className="size-4" /></Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" sideOffset={5}>
              <DropdownMenuItem onSelect={onEdit}><Pencil className="size-4" />{t('vps.editAction')}</DropdownMenuItem>
              <DropdownMenuItem onSelect={onMove}><ArrowRight className="size-4" />{t('vps.moveTo')}</DropdownMenuItem>
              <DropdownMenuItem onSelect={onRemove} className="text-destructive focus:text-destructive"><Trash className="size-4" />{t('common.delete')}</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      {loading && !m ? (
        <div className="h-24 animate-pulse rounded-[var(--radius-sm)] bg-muted/40" />
      ) : m && !m.reachable ? (
        <p className="rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/5 p-2.5 text-xs text-destructive">{m.error || t('vps.unreachable')}</p>
      ) : m ? (
        <div className="space-y-3">
          <p className="text-[11px] text-muted-foreground">{[m.os, m.kernel && `kernel ${m.kernel}`, m.uptime && `up ${m.uptime}`].filter(Boolean).join(' · ')}</p>
          <Gauge icon={<Cpu className="size-3.5" />} label={t('vps.cpu')} percent={m.cpu_percent ?? 0} detail={`${m.load1 ?? 0} / ${m.load5 ?? 0} / ${m.load15 ?? 0} · ${m.cpu_cores ?? 0} ${t('vps.cores')}`} />
          <Gauge icon={<Memory className="size-3.5" />} label={t('vps.memory')} percent={m.mem_percent ?? 0} detail={`${fmtMB(m.mem_used_mb)} / ${fmtMB(m.mem_total_mb)}`} />
          <Gauge icon={<HardDrives className="size-3.5" />} label={t('vps.disk')} percent={m.disk_percent ?? 0} detail={`${m.disk_used_gb ?? 0}G / ${m.disk_total_gb ?? 0}G`} />
          {m.swap_total_mb ? <Gauge label={t('vps.swap')} percent={m.swap_percent ?? 0} detail={`${fmtMB(m.swap_used_mb)} / ${fmtMB(m.swap_total_mb)}`} /> : null}
          <div className="flex items-center justify-between pt-1">
            <span className="text-[11px] text-muted-foreground">{m.processes} {t('vps.procs')}</span>
            <Button variant="outline" size="sm" className="h-7 px-2 text-xs" onClick={() => setShowProc(true)}>{t('vps.showProc')}</Button>
          </div>
        </div>
      ) : null}
      <ProcessModal host={host} open={showProc} onOpenChange={setShowProc} />
    </Card>
  )
}

interface Process {
  pid: number
  user: string
  cpu: number
  mem: number
  command: string
}

function ProcessModal({ host, open, onOpenChange }: { host: VPSHost; open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  const [procs, setProcs] = useState<Process[] | null>(null)
  const [err, setErr] = useState<string>()
  const [q, setQ] = useState('')

  useEffect(() => {
    if (!open) return
    setProcs(null)
    setErr(undefined)
    setQ('')
    get<{ processes: Process[]; error?: string }>(`/vps/${encodeURIComponent(host.id)}/processes`)
      .then((d) => { if (d.error) setErr(d.error); setProcs(d.processes ?? []) })
      .catch((e) => setErr((e as Error).message))
  }, [open, host.id])

  const shown = (procs ?? []).filter((process) => {
    const search = q.trim().toLowerCase()
    return !search || process.command.toLowerCase().includes(search) || process.user.toLowerCase().includes(search) || String(process.pid).includes(search)
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader><DialogTitle>{t('vps.procTitle')} — {host.label}</DialogTitle></DialogHeader>
        <DialogBody className="space-y-2">
          <Input value={q} onChange={(event) => setQ(event.target.value)} placeholder={t('vps.procSearch')} />
          {err ? <p className="text-xs text-destructive">{err}</p> : procs === null ? (
            <div className="h-64 animate-pulse rounded-[var(--radius-sm)] bg-muted/40" />
          ) : shown.length === 0 ? <p className="py-8 text-center text-xs text-muted-foreground">{t('vps.procNone')}</p> : (
            <div className="max-h-[55vh] overflow-y-auto rounded-[var(--radius-sm)] border border-border">
              <table className="w-full text-left text-xs">
                <thead className="sticky top-0 bg-card"><tr className="border-b border-border text-[11px] text-muted-foreground"><th className="px-2 py-1.5 font-medium">{t('vps.procPid')}</th><th className="px-2 py-1.5 font-medium">{t('vps.procUser')}</th><th className="px-2 py-1.5 text-right font-medium">{t('vps.cpu')}</th><th className="px-2 py-1.5 text-right font-medium">{t('vps.memory')}</th><th className="px-2 py-1.5 font-medium">{t('vps.procCmd')}</th></tr></thead>
                <tbody className="font-mono">{shown.map((process) => <tr key={process.pid} className="border-b border-border/50 last:border-0"><td className="px-2 py-1 tabular-nums text-muted-foreground">{process.pid}</td><td className="px-2 py-1 text-muted-foreground">{process.user}</td><td className={cn('px-2 py-1 text-right tabular-nums', process.cpu >= 50 && 'text-[var(--warning)]')}>{process.cpu.toFixed(1)}</td><td className="px-2 py-1 text-right tabular-nums text-muted-foreground">{process.mem.toFixed(1)}</td><td className="max-w-0 truncate px-2 py-1">{process.command}</td></tr>)}</tbody>
              </table>
            </div>
          )}
        </DialogBody>
      </DialogContent>
    </Dialog>
  )
}

function Gauge({ icon, label, percent, detail }: { icon?: ReactNode; label: string; percent: number; detail: string }) {
  const p = Math.max(0, Math.min(100, percent))
  const tone = p >= 90 ? 'bg-destructive' : p >= 70 ? 'bg-[var(--warning)]' : 'bg-primary'
  return (
    <div>
      <div className="mb-1 flex items-center justify-between text-[11px]"><span className="flex items-center gap-1.5 text-muted-foreground">{icon}{label}</span><span className="tabular-nums text-muted-foreground">{p}% <span className="text-muted-foreground/60">· {detail}</span></span></div>
      <div className="h-1.5 overflow-hidden rounded-full bg-muted"><div className={cn('h-full rounded-full transition-all', tone)} style={{ width: `${p}%` }} /></div>
    </div>
  )
}

function fmtMB(mb?: number): string {
  if (!mb) return '0'
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)}G`
  return `${mb}M`
}

const SCHEMES = ['password', 'key']

function VPSDialog({ open, host, folderId = '', onOpenChange, onSaved }: { open: boolean; host?: VPSHost; folderId?: string; onOpenChange: (open: boolean) => void; onSaved: () => void }) {
  const { t } = useI18n()
  const isEdit = !!host
  const [label, setLabel] = useState('')
  const [hostname, setHostname] = useState('')
  const [port, setPort] = useState('22')
  const [username, setUsername] = useState('root')
  const [authMethod, setAuthMethod] = useState('password')
  const [password, setPassword] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [passphrase, setPassphrase] = useState('')
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; as?: string; error?: string } | null>(null)
  const [error, setError] = useState<string>()

  useEffect(() => {
    if (!open) return
    setLabel(host?.label ?? '')
    setHostname(host?.host ?? '')
    setPort(host?.port ? String(host.port) : '22')
    setUsername(host?.username ?? 'root')
    setAuthMethod(host?.auth_method || 'password')
    setPassword('')
    setPrivateKey('')
    setPassphrase('')
    setTestResult(null)
    setError(undefined)
  }, [open, host])

  const body = () => ({
    id: host?.id,
    label: label.trim(),
    host: hostname.trim(),
    port: port ? Number(port) : 22,
    username: username.trim(),
    auth_method: authMethod,
    password: authMethod === 'password' ? password : '',
    private_key: authMethod === 'key' ? privateKey : '',
    passphrase: authMethod === 'key' ? passphrase : '',
  })
  const valid = hostname.trim() !== ''

  const test = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const result = await post<{ ok: boolean; as?: string; error?: string }>('/vps/test', body())
      setTestResult(result)
    } catch (testError) {
      setTestResult({ ok: false, error: (testError as Error).message })
    } finally {
      setTesting(false)
    }
  }

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post('/vps', { ...body(), folder_id: host?.folder_id ?? folderId })
      onSaved()
      onOpenChange(false)
    } catch (submitError) {
      setError((submitError as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader><DialogTitle>{isEdit ? t('vps.edit') : t('vps.add')}</DialogTitle></DialogHeader>
        <DialogBody className="space-y-3">
          <div className="space-y-1.5"><Label>{t('vps.label')}</Label><Input value={label} onChange={(event) => setLabel(event.target.value)} placeholder={t('vps.labelPh')} /></div>
          <div className="grid grid-cols-[1fr_6rem] gap-2"><div className="space-y-1.5"><Label>{t('vps.host')}</Label><Input value={hostname} onChange={(event) => setHostname(event.target.value)} placeholder="1.2.3.4 / vps.example.com" /></div><div className="space-y-1.5"><Label>{t('vps.port')}</Label><Input type="number" value={port} onChange={(event) => setPort(event.target.value)} placeholder="22" /></div></div>
          <div className="grid grid-cols-2 gap-2"><div className="space-y-1.5"><Label>{t('vps.username')}</Label><Input value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="off" /></div><div className="space-y-1.5"><Label>{t('vps.authMethod')}</Label><select value={authMethod} onChange={(event) => setAuthMethod(event.target.value)} className="h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm">{SCHEMES.map((scheme) => <option key={scheme} value={scheme}>{scheme === 'password' ? t('vps.authPassword') : t('vps.authKey')}</option>)}</select></div></div>
          {authMethod === 'password' ? <div className="space-y-1.5"><Label>{t('vps.password')}</Label><Input type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder={isEdit && host?.has_password ? '••••••••' : ''} autoComplete="off" /></div> : <><div className="space-y-1.5"><Label>{t('vps.privateKey')}</Label><textarea value={privateKey} onChange={(event) => setPrivateKey(event.target.value)} spellCheck={false} rows={5} placeholder={isEdit && host?.has_key ? '•••••• (stored — leave blank to keep)' : '-----BEGIN OPENSSH PRIVATE KEY-----'} className="w-full resize-y rounded-[var(--radius-sm)] border border-input bg-background px-3 py-2 font-mono text-[11px]" /></div><div className="space-y-1.5"><Label>{t('vps.passphrase')}</Label><Input type="password" value={passphrase} onChange={(event) => setPassphrase(event.target.value)} placeholder={t('vps.passphrasePh')} autoComplete="off" /></div></>}
          {testResult ? <div className={cn('flex items-center gap-2 rounded-[var(--radius-sm)] border p-2.5 text-xs', testResult.ok ? 'border-[var(--success)]/40 text-foreground' : 'border-destructive/40 text-destructive')}>{testResult.ok ? <CheckCircle className="size-4 text-[var(--success)]" weight="fill" /> : <XCircle className="size-4" weight="fill" />}<span className="min-w-0 break-words">{testResult.ok ? t('vps.testOk', { as: testResult.as ?? '' }) : t('vps.testFail', { error: testResult.error ?? '' })}</span></div> : null}
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </DialogBody>
        <DialogFooter><Button variant="outline" onClick={() => void test()} loading={testing} disabled={!valid}>{testing ? t('vps.testing') : t('vps.test')}</Button><DialogClose asChild><Button variant="ghost">{t('common.close')}</Button></DialogClose><Button onClick={() => void submit()} loading={busy} disabled={!valid}>{t('vps.save')}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
