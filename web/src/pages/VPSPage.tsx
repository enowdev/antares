import { useEffect, useState, type DragEvent } from 'react'
import {
  ArrowRight,
  ArrowsClockwise,
  CaretDown,
  CaretRight,
  CheckCircle,
  Cpu,
  DotsSixVertical,
  Folder,
  FolderOpen,
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
import { usePageActions } from '@/components/layout/PageChrome'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { Spoiler } from '@/components/ui/Spoiler'
import { SensitiveGate, useDashboardLocked } from '@/components/ui/SensitiveGate'

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
  sort_order: number
}

interface VPSFolder {
  id: string
  name: string
  parent_id: string
  sort_order: number
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

type DragItem = { type: 'folder' | 'host'; id: string }
type FolderDialogState = { mode: 'add'; parentId: string } | { mode: 'rename'; folder: VPSFolder }
type MoveItem = { type: 'folder'; item: VPSFolder } | { type: 'host'; item: VPSHost }

const byOrder = <T extends { sort_order: number }>(a: T, b: T) => a.sort_order - b.sort_order

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

function flattenFolders(folders: VPSFolder[]) {
  const result: Array<{ folder: VPSFolder; depth: number; path: string }> = []
  const visit = (parentId: string, depth: number, parentPath: string, ancestors: Set<string>) => {
    folders
      .filter((folder) => folder.parent_id === parentId && !ancestors.has(folder.id))
      .sort(byOrder)
      .forEach((folder) => {
        const path = parentPath ? `${parentPath} / ${folder.name}` : folder.name
        result.push({ folder, depth, path })
        visit(folder.id, depth + 1, path, new Set([...ancestors, folder.id]))
      })
  }
  visit('', 0, '', new Set())
  return result
}

// Sidebar badges count every server beneath a folder, not just direct children.
// Walk upward from each host so malformed data cannot overflow the call stack.
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
  const [expandedFolders, setExpandedFolders] = useLocalStorage<string[]>('antares.vps.expandedFolders', [])
  const [editing, setEditing] = useState<VPSHost | null>(null)
  const [adding, setAdding] = useState(false)
  const [toRemove, setToRemove] = useState<VPSHost | null>(null)
  const [removing, setRemoving] = useState(false)
  const [folderDialog, setFolderDialog] = useState<FolderDialogState | null>(null)
  const [folderToRemove, setFolderToRemove] = useState<VPSFolder | null>(null)
  const [removingFolder, setRemovingFolder] = useState(false)
  const [moving, setMoving] = useState<MoveItem | null>(null)
  const [dragging, setDragging] = useState<DragItem | null>(null)
  const [expandedContentFolders, setExpandedContentFolders] = useState<string[]>([])
  const [operationError, setOperationError] = useState<string>()
  const locked = useDashboardLocked()

  const hosts = data?.hosts ?? []
  const folders = data?.folders ?? []
  const flatFolders = flattenFolders(folders)
  const serverCounts = folderServerCounts(folders, hosts)
  const selected = folders.find((folder) => folder.id === selectedFolder)
  const visibleHosts = hosts.filter((host) => host.folder_id === selectedFolder).sort(byOrder)
  const selectedChildren = folders.filter((folder) => folder.parent_id === selectedFolder).sort(byOrder)
  const selectedServerCount = selectedFolder ? (serverCounts.get(selectedFolder) ?? 0) : hosts.length

  useEffect(() => {
    if (!data) return
    if (selectedFolder && !data.folders.some((folder) => folder.id === selectedFolder)) setSelectedFolder('')
    const validExpanded = expandedFolders.filter((id) => data.folders.some((folder) => folder.id === id))
    if (validExpanded.length !== expandedFolders.length) setExpandedFolders(validExpanded)
    const validContentExpanded = expandedContentFolders.filter((id) => data.folders.some((folder) => folder.id === id))
    if (validContentExpanded.length !== expandedContentFolders.length) setExpandedContentFolders(validContentExpanded)
  }, [data, expandedContentFolders, expandedFolders, selectedFolder, setExpandedFolders, setSelectedFolder])

  usePageActions(
    // Hide "Add" until a dashboard password protects these credentials.
    locked === false ? null : (
      <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
        <Plus className="size-4" />
        {t('vps.add')}
      </Button>
    ),
    [t, locked],
  )

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

  const selectFolder = (id: string) => {
    setSelectedFolder(id)
    setExpandedContentFolders([])
    if (!id) return
    const byId = new Map(folders.map((folder) => [folder.id, folder]))
    const next = new Set(expandedFolders)
    let current = byId.get(id)?.parent_id ?? ''
    while (current) {
      next.add(current)
      current = byId.get(current)?.parent_id ?? ''
    }
    setExpandedFolders([...next])
  }

  const toggleExpanded = (id: string) => {
    setExpandedFolders(
      expandedFolders.includes(id) ? expandedFolders.filter((folderId) => folderId !== id) : [...expandedFolders, id],
    )
  }

  const toggleContentFolder = (id: string) => {
    setExpandedContentFolders((current) =>
      current.includes(id) ? current.filter((folderId) => folderId !== id) : [...current, id],
    )
  }

  const startDrag = (event: DragEvent<HTMLElement>, item: DragItem) => {
    setDragging(item)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', `${item.type}:${item.id}`)
  }

  const moveFolder = async (id: string, parentId: string, index: number) => {
    if (folderIsSelfOrDescendant(folders, id, parentId)) return false
    setOperationError(undefined)
    try {
      await put(`/vps/folders/${encodeURIComponent(id)}/move`, { parent_id: parentId, index })
      if (parentId && !expandedFolders.includes(parentId)) setExpandedFolders([...expandedFolders, parentId])
      reload()
      return true
    } catch (error) {
      setOperationError((error as Error).message)
      return false
    } finally {
      setDragging(null)
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
    } finally {
      setDragging(null)
    }
  }

  const dropIntoFolder = (item: DragItem, folderId: string) => {
    if (item.type === 'folder') {
      if (folderIsSelfOrDescendant(folders, item.id, folderId)) return
      const index = folders.filter((folder) => folder.parent_id === folderId && folder.id !== item.id).length
      void moveFolder(item.id, folderId, index)
    } else {
      const index = hosts.filter((host) => host.folder_id === folderId && host.id !== item.id).length
      void moveHost(item.id, folderId, index)
    }
  }

  const reorderFolderBefore = (item: DragItem, parentId: string, beforeId?: string) => {
    if (item.type !== 'folder' || folderIsSelfOrDescendant(folders, item.id, parentId)) return
    const siblings = folders.filter((folder) => folder.parent_id === parentId && folder.id !== item.id).sort(byOrder)
    const index = beforeId ? siblings.findIndex((folder) => folder.id === beforeId) : siblings.length
    void moveFolder(item.id, parentId, index < 0 ? siblings.length : index)
  }

  const confirmFolderRemove = async () => {
    if (!folderToRemove) return
    setRemovingFolder(true)
    setOperationError(undefined)
    const parentId = folderToRemove.parent_id
    try {
      await del(`/vps/folders/${encodeURIComponent(folderToRemove.id)}`)
      if (selectedFolder === folderToRemove.id) selectFolder(parentId)
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
          onOpenChange={(o) => !o && setEditing(null)}
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
          onOpenChange={(o) => !o && setToRemove(null)}
          title={t('vps.removeTitle')}
          description={t('vps.removeDesc', { label: toRemove?.label ?? '' })}
          confirmLabel={t('common.remove')}
          loading={removing}
          onConfirm={() => void confirmRemove()}
        />
        <ConfirmDialog
          open={!!folderToRemove}
          onOpenChange={(open) => !open && setFolderToRemove(null)}
          title={t('vps.deleteFolderTitle')}
          description={t('vps.deleteFolderDesc', {
            name: folderToRemove?.name ?? '',
            parent: folders.find((folder) => folder.id === folderToRemove?.parent_id)?.name ?? t('vps.ungrouped'),
          })}
          confirmLabel={t('common.delete')}
          loading={removingFolder}
          onConfirm={() => void confirmFolderRemove()}
        />

        <MobileFolderNavigator
          folders={folders}
          flatFolders={flatFolders}
          selectedFolder={selectedFolder}
          onSelect={selectFolder}
          onAdd={() => setFolderDialog({ mode: 'add', parentId: selectedFolder })}
          onRename={() => selected && setFolderDialog({ mode: 'rename', folder: selected })}
          onMove={() => selected && setMoving({ type: 'folder', item: selected })}
          onRemove={() => selected && setFolderToRemove(selected)}
        />

        {operationError ? (
          <p className="mb-3 rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/5 p-2.5 text-xs text-destructive">
            {t('vps.operationFailed', { error: operationError })}
          </p>
        ) : null}

        <div className="items-start gap-5 lg:grid lg:grid-cols-[minmax(15rem,18rem)_minmax(0,1fr)]">
          <FolderSidebar
            folders={folders}
            hosts={hosts}
            serverCounts={serverCounts}
            selectedFolder={selectedFolder}
            expandedFolders={expandedFolders}
            dragging={dragging}
            onSelect={selectFolder}
            onToggle={toggleExpanded}
            onAdd={(parentId) => setFolderDialog({ mode: 'add', parentId })}
            onRename={(folder) => setFolderDialog({ mode: 'rename', folder })}
            onMove={(folder) => setMoving({ type: 'folder', item: folder })}
            onRemove={setFolderToRemove}
            onDragStart={startDrag}
            onDragEnd={() => setDragging(null)}
            onDropInto={dropIntoFolder}
            onReorderBefore={reorderFolderBefore}
          />

          <section className="min-w-0">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <div>
                <h2 className="flex items-center gap-2 text-sm font-semibold">
                  {selected ? <FolderOpen className="size-4 text-primary" weight="fill" /> : <HardDrives className="size-4 text-primary" />}
                  {selected?.name ?? t('vps.ungrouped')}
                </h2>
                <p className="text-[11px] text-muted-foreground">
                  {t('vps.serverSummary', { direct: visibleHosts.length, total: selectedServerCount })}
                </p>
              </div>
              <Button size="sm" variant="outline" onClick={() => setAdding(true)} className="gap-1.5">
                <Plus className="size-4" />
                {t('vps.add')}
              </Button>
            </div>

            {visibleHosts.length === 0 && selectedChildren.length === 0 ? (
              <EmptyState
                icon={<HardDrives className="size-6" />}
                title={hosts.length === 0 && folders.length === 0 ? t('vps.none') : t('vps.folderEmpty')}
                description={hosts.length === 0 && folders.length === 0 ? t('vps.noneDesc') : t('vps.folderEmptyDesc')}
                action={
                  <Button size="sm" onClick={() => setAdding(true)} className="gap-1.5">
                    <Plus className="size-4" />
                    {t('vps.add')}
                  </Button>
                }
              />
            ) : (
              <div className="space-y-6">
                {visibleHosts.length > 0 ? (
                  <VPSHostGrid
                    hosts={visibleHosts}
                    folderId={selectedFolder}
                    dragging={dragging}
                    onMoveHost={moveHost}
                    onEdit={setEditing}
                    onMove={(host) => setMoving({ type: 'host', item: host })}
                    onRemove={setToRemove}
                    onDragStart={startDrag}
                    onDragEnd={() => setDragging(null)}
                  />
                ) : null}
                {selectedChildren.length > 0 ? (
                  <div className="space-y-3">
                    <p className="text-xs font-medium text-muted-foreground">{t('vps.nestedFolders')}</p>
                    <VPSFolderGroups
                      parentId={selectedFolder}
                      folders={folders}
                      hosts={hosts}
                      serverCounts={serverCounts}
                      expandedFolders={expandedContentFolders}
                      dragging={dragging}
                      onToggle={toggleContentFolder}
                      onMoveHost={moveHost}
                      onEdit={setEditing}
                      onMove={(host) => setMoving({ type: 'host', item: host })}
                      onRemove={setToRemove}
                      onDragStart={startDrag}
                      onDragEnd={() => setDragging(null)}
                    />
                  </div>
                ) : null}
              </div>
            )}
          </section>
        </div>
      </SensitiveGate>
    </PageLayout>
  )
}

function MobileFolderNavigator({
  folders,
  flatFolders,
  selectedFolder,
  onSelect,
  onAdd,
  onRename,
  onMove,
  onRemove,
}: {
  folders: VPSFolder[]
  flatFolders: ReturnType<typeof flattenFolders>
  selectedFolder: string
  onSelect: (id: string) => void
  onAdd: () => void
  onRename: () => void
  onMove: () => void
  onRemove: () => void
}) {
  const { t } = useI18n()
  return (
    <Card className="mb-4 space-y-2.5 p-3 lg:hidden">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor="vps-mobile-folder">{t('vps.folders')}</Label>
        <Button size="sm" variant="outline" onClick={onAdd}>
          <Plus /> {t('vps.addFolder')}
        </Button>
      </div>
      <select
        id="vps-mobile-folder"
        value={selectedFolder}
        onChange={(event) => onSelect(event.target.value)}
        className="h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm"
      >
        <option value="">{t('vps.ungrouped')}</option>
        {flatFolders.map(({ folder, path }) => (
          <option key={folder.id} value={folder.id}>{path}</option>
        ))}
      </select>
      {folders.some((folder) => folder.id === selectedFolder) ? (
        <div className="flex flex-wrap gap-1.5">
          <Button size="sm" variant="ghost" onClick={onRename}><Pencil /> {t('vps.renameFolder')}</Button>
          <Button size="sm" variant="ghost" onClick={onMove}><ArrowRight /> {t('vps.move')}</Button>
          <Button size="sm" variant="ghost" onClick={onRemove}><Trash /> {t('common.delete')}</Button>
        </div>
      ) : null}
    </Card>
  )
}

function FolderSidebar({
  folders,
  hosts,
  serverCounts,
  selectedFolder,
  expandedFolders,
  dragging,
  onSelect,
  onToggle,
  onAdd,
  onRename,
  onMove,
  onRemove,
  onDragStart,
  onDragEnd,
  onDropInto,
  onReorderBefore,
}: {
  folders: VPSFolder[]
  hosts: VPSHost[]
  serverCounts: Map<string, number>
  selectedFolder: string
  expandedFolders: string[]
  dragging: DragItem | null
  onSelect: (id: string) => void
  onToggle: (id: string) => void
  onAdd: (parentId: string) => void
  onRename: (folder: VPSFolder) => void
  onMove: (folder: VPSFolder) => void
  onRemove: (folder: VPSFolder) => void
  onDragStart: (event: DragEvent<HTMLElement>, item: DragItem) => void
  onDragEnd: () => void
  onDropInto: (item: DragItem, folderId: string) => void
  onReorderBefore: (item: DragItem, parentId: string, beforeId?: string) => void
}) {
  const { t } = useI18n()
  const roots = folders.filter((folder) => folder.parent_id === '').sort(byOrder)
  return (
    <Card className="sticky top-4 hidden max-h-[calc(100vh-7rem)] min-h-0 w-full overflow-hidden p-0 lg:flex lg:flex-col">
      <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border px-3 py-2.5">
        <div className="min-w-0">
          <p className="text-xs font-semibold">{t('vps.folders')}</p>
          <p className="truncate text-[10px] text-muted-foreground">
            {t('vps.sidebarSummary', { folders: folders.length, servers: hosts.length })}
          </p>
        </div>
        <Button size="icon-sm" variant="ghost" className="shrink-0" onClick={() => onAdd(selectedFolder)} aria-label={t('vps.addFolder')}>
          <Plus />
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        <FolderRow
          root
          selected={selectedFolder === ''}
          hostCount={hosts.length}
          dragging={dragging}
          onSelect={() => onSelect('')}
          onDrop={(item) => onDropInto(item, '')}
        />
        <FolderBranch
          parentId=""
          folders={folders}
          hosts={hosts}
          serverCounts={serverCounts}
          nodes={roots}
          selectedFolder={selectedFolder}
          expandedFolders={expandedFolders}
          dragging={dragging}
          onSelect={onSelect}
          onToggle={onToggle}
          onRename={onRename}
          onMove={onMove}
          onRemove={onRemove}
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          onDropInto={onDropInto}
          onReorderBefore={onReorderBefore}
        />
      </div>
    </Card>
  )
}

function FolderBranch({
  parentId,
  folders,
  nodes,
  hosts,
  serverCounts,
  selectedFolder,
  expandedFolders,
  dragging,
  onSelect,
  onToggle,
  onRename,
  onMove,
  onRemove,
  onDragStart,
  onDragEnd,
  onDropInto,
  onReorderBefore,
}: {
  parentId: string
  folders: VPSFolder[]
  nodes: VPSFolder[]
  hosts: VPSHost[]
  serverCounts: Map<string, number>
  selectedFolder: string
  expandedFolders: string[]
  dragging: DragItem | null
  onSelect: (id: string) => void
  onToggle: (id: string) => void
  onRename: (folder: VPSFolder) => void
  onMove: (folder: VPSFolder) => void
  onRemove: (folder: VPSFolder) => void
  onDragStart: (event: DragEvent<HTMLElement>, item: DragItem) => void
  onDragEnd: () => void
  onDropInto: (item: DragItem, folderId: string) => void
  onReorderBefore: (item: DragItem, parentId: string, beforeId?: string) => void
}) {
  return (
    <div className={parentId ? 'ml-3 min-w-0 border-l border-border/60 pl-1.5' : 'min-w-0'}>
      {nodes.map((folder) => {
        const children = folders.filter((candidate) => candidate.parent_id === folder.id).sort(byOrder)
        const expanded = expandedFolders.includes(folder.id)
        return (
          <div key={folder.id}>
            <FolderDropLine dragging={dragging} onDrop={(item) => onReorderBefore(item, parentId, folder.id)} />
            <FolderRow
              folder={folder}
              selected={selectedFolder === folder.id}
              expanded={expanded}
              hasChildren={children.length > 0}
              hostCount={serverCounts.get(folder.id) ?? 0}
              dragging={dragging}
              onSelect={() => onSelect(folder.id)}
              onToggle={() => onToggle(folder.id)}
              onRename={() => onRename(folder)}
              onMove={() => onMove(folder)}
              onRemove={() => onRemove(folder)}
              onDragStart={(event) => onDragStart(event, { type: 'folder', id: folder.id })}
              onDragEnd={onDragEnd}
              onDrop={(item) => onDropInto(item, folder.id)}
              invalidDrop={dragging?.type === 'folder' && folderIsSelfOrDescendant(folders, dragging.id, folder.id)}
            />
            {expanded && children.length > 0 ? (
              <FolderBranch
                parentId={folder.id}
                folders={folders}
                nodes={children}
                hosts={hosts}
                serverCounts={serverCounts}
                selectedFolder={selectedFolder}
                expandedFolders={expandedFolders}
                dragging={dragging}
                onSelect={onSelect}
                onToggle={onToggle}
                onRename={onRename}
                onMove={onMove}
                onRemove={onRemove}
                onDragStart={onDragStart}
                onDragEnd={onDragEnd}
                onDropInto={onDropInto}
                onReorderBefore={onReorderBefore}
              />
            ) : null}
          </div>
        )
      })}
      <FolderDropLine dragging={dragging} onDrop={(item) => onReorderBefore(item, parentId)} />
    </div>
  )
}

function FolderDropLine({ dragging, onDrop }: { dragging: DragItem | null; onDrop: (item: DragItem) => void }) {
  const [active, setActive] = useState(false)
  if (dragging?.type !== 'folder') return <div className="h-1" />
  return (
    <div
      className={cn('h-2 rounded-full transition-colors', active && 'bg-primary/70')}
      onDragEnter={() => setActive(true)}
      onDragLeave={() => setActive(false)}
      onDragOver={(event) => event.preventDefault()}
      onDrop={(event) => {
        event.preventDefault()
        event.stopPropagation()
        setActive(false)
        onDrop(dragging)
      }}
    />
  )
}

function FolderRow({
  folder,
  root = false,
  selected,
  expanded = false,
  hasChildren = false,
  hostCount,
  dragging,
  invalidDrop = false,
  onSelect,
  onToggle,
  onRename,
  onMove,
  onRemove,
  onDragStart,
  onDragEnd,
  onDrop,
}: {
  folder?: VPSFolder
  root?: boolean
  selected: boolean
  expanded?: boolean
  hasChildren?: boolean
  hostCount: number
  dragging: DragItem | null
  invalidDrop?: boolean
  onSelect: () => void
  onToggle?: () => void
  onRename?: () => void
  onMove?: () => void
  onRemove?: () => void
  onDragStart?: (event: DragEvent<HTMLElement>) => void
  onDragEnd?: () => void
  onDrop: (item: DragItem) => void
}) {
  const { t } = useI18n()
  const canDrop = !!dragging && !invalidDrop
  return (
    <div
      className={cn(
        'group flex w-full min-w-0 items-center rounded-[var(--radius-sm)] border border-transparent text-xs transition-colors',
        selected ? 'border-primary/20 bg-primary/10 text-foreground' : 'text-muted-foreground hover:bg-accent hover:text-foreground',
        canDrop && 'hover:border-primary hover:bg-primary/10',
      )}
      onDragOver={(event) => {
        if (canDrop) event.preventDefault()
      }}
      onDrop={(event) => {
        if (!canDrop || !dragging) return
        event.preventDefault()
        event.stopPropagation()
        onDrop(dragging)
      }}
    >
      {root ? (
        <span className="ml-1 flex size-6 shrink-0" />
      ) : (
        <button
          type="button"
          className="ml-1 flex size-6 shrink-0 items-center justify-center rounded hover:bg-accent"
          onClick={onToggle}
          aria-label={expanded ? t('vps.collapseFolder') : t('vps.expandFolder')}
        >
          {hasChildren ? expanded ? <CaretDown /> : <CaretRight /> : null}
        </button>
      )}
      {!root ? (
        <span
          draggable
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          className="flex size-5 shrink-0 cursor-grab items-center justify-center opacity-40 hover:opacity-100 active:cursor-grabbing"
          title={t('vps.dragFolder')}
        >
          <DotsSixVertical className="size-3.5" />
        </span>
      ) : null}
      <button type="button" onClick={onSelect} className="flex min-w-0 flex-1 items-center gap-1.5 py-1.5 text-left">
        {root ? <HardDrives className="size-3.5 shrink-0" /> : expanded ? <FolderOpen className="size-3.5 shrink-0" weight="fill" /> : <Folder className="size-3.5 shrink-0" weight="fill" />}
        <span className="truncate">{root ? t('vps.ungrouped') : folder?.name}</span>
        <span className="ml-auto shrink-0 tabular-nums text-[10px] opacity-60" title={t('vps.totalServers', { n: hostCount })}>
          {hostCount}
        </span>
      </button>
      {!root ? (
        <div className="mr-1 hidden shrink-0 items-center bg-inherit group-hover:flex group-focus-within:flex">
          <Button variant="ghost" size="icon-sm" className="size-6" onClick={onMove} aria-label={t('vps.move')}><ArrowRight /></Button>
          <Button variant="ghost" size="icon-sm" className="size-6" onClick={onRename} aria-label={t('vps.renameFolder')}><Pencil /></Button>
          <Button variant="ghost" size="icon-sm" className="size-6" onClick={onRemove} aria-label={t('common.delete')}><Trash /></Button>
        </div>
      ) : null}
    </div>
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

  const parentName =
    state?.mode === 'add'
      ? folders.find((folder) => folder.id === state.parentId)?.name ?? t('vps.ungrouped')
      : ''

  return (
    <Dialog open={!!state} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>
            {state?.mode === 'rename' ? t('vps.renameFolderTitle') : t('vps.addFolderTitle', { parent: parentName })}
          </DialogTitle>
        </DialogHeader>
        <DialogBody>
          <div className="space-y-1.5">
            <Label htmlFor="vps-folder-name">{t('vps.folderName')}</Label>
            <Input
              id="vps-folder-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') void submit()
              }}
              placeholder={t('vps.folderNamePh')}
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

  const options = flattenFolders(folders).filter(
    ({ folder }) => moving?.type !== 'folder' || !folderIsSelfOrDescendant(folders, moving.item.id, folder.id),
  )

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
          <DialogTitle>
            {t('vps.moveItemTitle', {
              name: moving ? (moving.type === 'folder' ? moving.item.name : moving.item.label) : '',
            })}
          </DialogTitle>
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
            <p className="text-[11px] text-muted-foreground">{t('vps.moveToEndHint')}</p>
          </div>
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild><Button variant="ghost">{t('common.close')}</Button></DialogClose>
          <Button onClick={() => void submit()} loading={busy}>{t('vps.moveHere')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function VPSFolderGroups({
  parentId,
  folders,
  hosts,
  serverCounts,
  expandedFolders,
  dragging,
  onToggle,
  onMoveHost,
  onEdit,
  onMove,
  onRemove,
  onDragStart,
  onDragEnd,
  depth = 0,
}: {
  parentId: string
  folders: VPSFolder[]
  hosts: VPSHost[]
  serverCounts: Map<string, number>
  expandedFolders: string[]
  dragging: DragItem | null
  onToggle: (id: string) => void
  onMoveHost: (id: string, folderId: string, index: number) => Promise<boolean>
  onEdit: (host: VPSHost) => void
  onMove: (host: VPSHost) => void
  onRemove: (host: VPSHost) => void
  onDragStart: (event: DragEvent<HTMLElement>, item: DragItem) => void
  onDragEnd: () => void
  depth?: number
}) {
  const { t } = useI18n()
  const children = folders.filter((folder) => folder.parent_id === parentId).sort(byOrder)
  return (
    <div className={cn('space-y-3', depth > 0 && 'ml-3 border-l border-border/60 pl-3')}>
      {children.map((folder) => {
        const directHosts = hosts.filter((host) => host.folder_id === folder.id).sort(byOrder)
        const childFolders = folders.filter((candidate) => candidate.parent_id === folder.id)
        const expanded = expandedFolders.includes(folder.id)
        const total = serverCounts.get(folder.id) ?? 0
        return (
          <Card key={folder.id} className="overflow-hidden shadow-none">
            <button
              type="button"
              className="flex w-full items-center gap-2 px-3 py-2.5 text-left transition-colors hover:bg-accent"
              onClick={() => onToggle(folder.id)}
              aria-expanded={expanded}
              aria-label={expanded ? t('vps.collapseFolder') : t('vps.expandFolder')}
            >
              {expanded ? <CaretDown className="size-4 shrink-0 text-muted-foreground" /> : <CaretRight className="size-4 shrink-0 text-muted-foreground" />}
              {expanded ? <FolderOpen className="size-4 shrink-0 text-primary" weight="fill" /> : <Folder className="size-4 shrink-0 text-primary" weight="fill" />}
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{folder.name}</span>
              <Badge variant="outline" className="shrink-0 tabular-nums" title={t('vps.totalServers', { n: total })}>
                {total}
              </Badge>
            </button>
            {expanded ? (
              <div className="space-y-4 border-t border-border px-3 py-3">
                {directHosts.length > 0 ? (
                  <VPSHostGrid
                    hosts={directHosts}
                    folderId={folder.id}
                    dragging={dragging}
                    onMoveHost={onMoveHost}
                    onEdit={onEdit}
                    onMove={onMove}
                    onRemove={onRemove}
                    onDragStart={onDragStart}
                    onDragEnd={onDragEnd}
                  />
                ) : childFolders.length === 0 ? (
                  <p className="py-1 text-xs text-muted-foreground">{t('vps.folderEmpty')}</p>
                ) : null}
                {childFolders.length > 0 ? (
                  <VPSFolderGroups
                    parentId={folder.id}
                    folders={folders}
                    hosts={hosts}
                    serverCounts={serverCounts}
                    expandedFolders={expandedFolders}
                    dragging={dragging}
                    onToggle={onToggle}
                    onMoveHost={onMoveHost}
                    onEdit={onEdit}
                    onMove={onMove}
                    onRemove={onRemove}
                    onDragStart={onDragStart}
                    onDragEnd={onDragEnd}
                    depth={depth + 1}
                  />
                ) : null}
              </div>
            ) : null}
          </Card>
        )
      })}
    </div>
  )
}

function VPSHostGrid({
  hosts,
  folderId,
  dragging,
  onMoveHost,
  onEdit,
  onMove,
  onRemove,
  onDragStart,
  onDragEnd,
}: {
  hosts: VPSHost[]
  folderId: string
  dragging: DragItem | null
  onMoveHost: (id: string, folderId: string, index: number) => Promise<boolean>
  onEdit: (host: VPSHost) => void
  onMove: (host: VPSHost) => void
  onRemove: (host: VPSHost) => void
  onDragStart: (event: DragEvent<HTMLElement>, item: DragItem) => void
  onDragEnd: () => void
}) {
  const reorderHostAtCard = (event: DragEvent<HTMLDivElement>, targetId: string) => {
    if (!dragging || dragging.type !== 'host') return
    event.preventDefault()
    const ordered = hosts.filter((host) => host.id !== dragging.id)
    const targetIndex = ordered.findIndex((host) => host.id === targetId)
    const after = event.clientY > event.currentTarget.getBoundingClientRect().top + event.currentTarget.clientHeight / 2
    void onMoveHost(dragging.id, folderId, Math.max(0, targetIndex) + (after ? 1 : 0))
  }

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      {hosts.map((host) => (
        <div
          key={host.id}
          onDragOver={(event) => {
            if (dragging?.type === 'host') event.preventDefault()
          }}
          onDrop={(event) => reorderHostAtCard(event, host.id)}
        >
          <VPSCard
            host={host}
            onEdit={() => onEdit(host)}
            onMove={() => onMove(host)}
            onRemove={() => onRemove(host)}
            onDragStart={(event) => onDragStart(event, { type: 'host', id: host.id })}
            onDragEnd={onDragEnd}
          />
        </div>
      ))}
      <div
        className={cn(
          'col-span-full h-6 rounded-[var(--radius-sm)] border border-dashed border-transparent transition-colors',
          dragging?.type === 'host' && 'border-border hover:border-primary hover:bg-primary/5',
        )}
        onDragOver={(event) => {
          if (dragging?.type === 'host') event.preventDefault()
        }}
        onDrop={(event) => {
          event.preventDefault()
          if (dragging?.type === 'host') {
            void onMoveHost(dragging.id, folderId, hosts.filter((host) => host.id !== dragging.id).length)
          }
        }}
        aria-hidden="true"
      />
    </div>
  )
}

function VPSCard({
  host,
  onEdit,
  onMove,
  onRemove,
  onDragStart,
  onDragEnd,
}: {
  host: VPSHost
  onEdit: () => void
  onMove: () => void
  onRemove: () => void
  onDragStart: (event: DragEvent<HTMLElement>) => void
  onDragEnd: () => void
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
  // Load once on mount; refresh is manual (each poll is a live SSH round trip).
  useEffect(refresh, [host.id])

  return (
    <Card className="p-4">
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{host.label}</span>
            {m ? (
              m.reachable ? (
                <Badge variant="success">
                  <CheckCircle className="size-3" weight="fill" />
                  {t('vps.online')}
                </Badge>
              ) : (
                <Badge variant="destructive">
                  <XCircle className="size-3" weight="fill" />
                  {t('vps.offline')}
                </Badge>
              )
            ) : null}
          </div>
          <p className="truncate font-mono text-[11px] text-muted-foreground">
            {host.username}@<Spoiler>{host.host}</Spoiler>:{host.port}
          </p>
        </div>
        <div className="flex shrink-0 gap-1">
          <span
            draggable
            onDragStart={onDragStart}
            onDragEnd={onDragEnd}
            className="flex size-8 cursor-grab items-center justify-center rounded-[var(--radius-sm)] text-muted-foreground hover:bg-accent active:cursor-grabbing"
            title={t('vps.dragServer')}
          >
            <DotsSixVertical className="size-4" />
          </span>
          <Button variant="ghost" size="icon-sm" onClick={refresh} aria-label={t('vps.refresh')} loading={loading}>
            <ArrowsClockwise className="size-4" />
          </Button>
          <Button variant="ghost" size="icon-sm" onClick={onMove} aria-label={t('vps.move')}>
            <ArrowRight className="size-4" />
          </Button>
          <Button variant="ghost" size="icon-sm" onClick={onEdit} aria-label={t('vps.edit')}>
            <Pencil className="size-4" />
          </Button>
          <Button variant="ghost" size="icon-sm" onClick={onRemove} aria-label={t('common.remove')}>
            <Trash className="size-4" />
          </Button>
        </div>
      </div>

      {loading && !m ? (
        <div className="h-24 animate-pulse rounded-[var(--radius-sm)] bg-muted/40" />
      ) : m && !m.reachable ? (
        <p className="rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/5 p-2.5 text-xs text-destructive">
          {m.error || t('vps.unreachable')}
        </p>
      ) : m ? (
        <div className="space-y-3">
          {/* identity line */}
          <p className="text-[11px] text-muted-foreground">
            {[m.os, m.kernel && `kernel ${m.kernel}`, m.uptime && `up ${m.uptime}`]
              .filter(Boolean)
              .join(' · ')}
          </p>

          <Gauge
            icon={<Cpu className="size-3.5" />}
            label={t('vps.cpu')}
            percent={m.cpu_percent ?? 0}
            detail={`${m.load1 ?? 0} / ${m.load5 ?? 0} / ${m.load15 ?? 0} · ${m.cpu_cores ?? 0} ${t('vps.cores')}`}
          />
          <Gauge
            icon={<Memory className="size-3.5" />}
            label={t('vps.memory')}
            percent={m.mem_percent ?? 0}
            detail={`${fmtMB(m.mem_used_mb)} / ${fmtMB(m.mem_total_mb)}`}
          />
          <Gauge
            icon={<HardDrives className="size-3.5" />}
            label={t('vps.disk')}
            percent={m.disk_percent ?? 0}
            detail={`${m.disk_used_gb ?? 0}G / ${m.disk_total_gb ?? 0}G`}
          />
          {m.swap_total_mb ? (
            <Gauge label={t('vps.swap')} percent={m.swap_percent ?? 0} detail={`${fmtMB(m.swap_used_mb)} / ${fmtMB(m.swap_total_mb)}`} />
          ) : null}

          <div className="flex items-center justify-between pt-1">
            <span className="text-[11px] text-muted-foreground">
              {m.processes} {t('vps.procs')}
            </span>
            <Button variant="outline" size="sm" className="h-7 px-2 text-xs" onClick={() => setShowProc(true)}>
              {t('vps.showProc')}
            </Button>
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

function ProcessModal({
  host,
  open,
  onOpenChange,
}: {
  host: VPSHost
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
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
      .then((d) => {
        if (d.error) setErr(d.error)
        setProcs(d.processes ?? [])
      })
      .catch((e) => setErr((e as Error).message))
  }, [open, host.id])

  const shown = (procs ?? []).filter((p) => {
    const s = q.trim().toLowerCase()
    return !s || p.command.toLowerCase().includes(s) || p.user.toLowerCase().includes(s) || String(p.pid).includes(s)
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {t('vps.procTitle')} — {host.label}
          </DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-2">
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('vps.procSearch')} />
          {err ? (
            <p className="text-xs text-destructive">{err}</p>
          ) : procs === null ? (
            <div className="h-64 animate-pulse rounded-[var(--radius-sm)] bg-muted/40" />
          ) : shown.length === 0 ? (
            <p className="py-8 text-center text-xs text-muted-foreground">{t('vps.procNone')}</p>
          ) : (
            <div className="max-h-[55vh] overflow-y-auto rounded-[var(--radius-sm)] border border-border">
              <table className="w-full text-left text-xs">
                <thead className="sticky top-0 bg-card">
                  <tr className="border-b border-border text-[11px] text-muted-foreground">
                    <th className="px-2 py-1.5 font-medium">{t('vps.procPid')}</th>
                    <th className="px-2 py-1.5 font-medium">{t('vps.procUser')}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t('vps.cpu')}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t('vps.memory')}</th>
                    <th className="px-2 py-1.5 font-medium">{t('vps.procCmd')}</th>
                  </tr>
                </thead>
                <tbody className="font-mono">
                  {shown.map((p) => (
                    <tr key={p.pid} className="border-b border-border/50 last:border-0">
                      <td className="px-2 py-1 tabular-nums text-muted-foreground">{p.pid}</td>
                      <td className="px-2 py-1 text-muted-foreground">{p.user}</td>
                      <td className={cn('px-2 py-1 text-right tabular-nums', p.cpu >= 50 && 'text-[var(--warning)]')}>
                        {p.cpu.toFixed(1)}
                      </td>
                      <td className="px-2 py-1 text-right tabular-nums text-muted-foreground">{p.mem.toFixed(1)}</td>
                      <td className="max-w-0 truncate px-2 py-1">{p.command}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </DialogBody>
      </DialogContent>
    </Dialog>
  )
}

function Gauge({
  icon,
  label,
  percent,
  detail,
}: {
  icon?: React.ReactNode
  label: string
  percent: number
  detail: string
}) {
  const p = Math.max(0, Math.min(100, percent))
  const tone =
    p >= 90 ? 'bg-destructive' : p >= 70 ? 'bg-[var(--warning)]' : 'bg-primary'
  return (
    <div>
      <div className="mb-1 flex items-center justify-between text-[11px]">
        <span className="flex items-center gap-1.5 text-muted-foreground">
          {icon}
          {label}
        </span>
        <span className="tabular-nums text-muted-foreground">
          {p}% <span className="text-muted-foreground/60">· {detail}</span>
        </span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div className={cn('h-full rounded-full transition-all', tone)} style={{ width: `${p}%` }} />
      </div>
    </div>
  )
}

function fmtMB(mb?: number): string {
  if (!mb) return '0'
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)}G`
  return `${mb}M`
}

const SCHEMES = ['password', 'key']

function VPSDialog({
  open,
  host,
  folderId = '',
  onOpenChange,
  onSaved,
}: {
  open: boolean
  host?: VPSHost
  folderId?: string
  onOpenChange: (o: boolean) => void
  onSaved: () => void
}) {
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

  // Reset the form each time the dialog opens for a specific host (or a new one).
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
    // Blank on edit = keep stored secret.
    password: authMethod === 'password' ? password : '',
    private_key: authMethod === 'key' ? privateKey : '',
    passphrase: authMethod === 'key' ? passphrase : '',
  })

  const valid = hostname.trim() !== ''

  const test = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const r = await post<{ ok: boolean; as?: string; error?: string }>('/vps/test', body())
      setTestResult(r)
    } catch (e) {
      setTestResult({ ok: false, error: (e as Error).message })
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
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{isEdit ? t('vps.edit') : t('vps.add')}</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-3">
          <div className="space-y-1.5">
            <Label>{t('vps.label')}</Label>
            <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder={t('vps.labelPh')} />
          </div>
          <div className="grid grid-cols-[1fr_6rem] gap-2">
            <div className="space-y-1.5">
              <Label>{t('vps.host')}</Label>
              <Input value={hostname} onChange={(e) => setHostname(e.target.value)} placeholder="1.2.3.4 / vps.example.com" />
            </div>
            <div className="space-y-1.5">
              <Label>{t('vps.port')}</Label>
              <Input type="number" value={port} onChange={(e) => setPort(e.target.value)} placeholder="22" />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1.5">
              <Label>{t('vps.username')}</Label>
              <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />
            </div>
            <div className="space-y-1.5">
              <Label>{t('vps.authMethod')}</Label>
              <select
                value={authMethod}
                onChange={(e) => setAuthMethod(e.target.value)}
                className="h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm"
              >
                {SCHEMES.map((s) => (
                  <option key={s} value={s}>
                    {s === 'password' ? t('vps.authPassword') : t('vps.authKey')}
                  </option>
                ))}
              </select>
            </div>
          </div>

          {authMethod === 'password' ? (
            <div className="space-y-1.5">
              <Label>{t('vps.password')}</Label>
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={isEdit && host?.has_password ? '••••••••' : ''}
                autoComplete="off"
              />
            </div>
          ) : (
            <>
              <div className="space-y-1.5">
                <Label>{t('vps.privateKey')}</Label>
                <textarea
                  value={privateKey}
                  onChange={(e) => setPrivateKey(e.target.value)}
                  spellCheck={false}
                  rows={5}
                  placeholder={
                    isEdit && host?.has_key
                      ? '•••••• (stored — leave blank to keep)'
                      : '-----BEGIN OPENSSH PRIVATE KEY-----'
                  }
                  className="w-full resize-y rounded-[var(--radius-sm)] border border-input bg-background px-3 py-2 font-mono text-[11px]"
                />
              </div>
              <div className="space-y-1.5">
                <Label>{t('vps.passphrase')}</Label>
                <Input
                  type="password"
                  value={passphrase}
                  onChange={(e) => setPassphrase(e.target.value)}
                  placeholder={t('vps.passphrasePh')}
                  autoComplete="off"
                />
              </div>
            </>
          )}

          {testResult ? (
            <div
              className={cn(
                'flex items-center gap-2 rounded-[var(--radius-sm)] border p-2.5 text-xs',
                testResult.ok ? 'border-[var(--success)]/40 text-foreground' : 'border-destructive/40 text-destructive',
              )}
            >
              {testResult.ok ? (
                <CheckCircle className="size-4 text-[var(--success)]" weight="fill" />
              ) : (
                <XCircle className="size-4" weight="fill" />
              )}
              <span className="min-w-0 break-words">
                {testResult.ok ? t('vps.testOk', { as: testResult.as ?? '' }) : t('vps.testFail', { error: testResult.error ?? '' })}
              </span>
            </div>
          ) : null}
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </DialogBody>
        <DialogFooter>
          <Button variant="outline" onClick={() => void test()} loading={testing} disabled={!valid}>
            {testing ? t('vps.testing') : t('vps.test')}
          </Button>
          <DialogClose asChild>
            <Button variant="ghost">{t('common.close')}</Button>
          </DialogClose>
          <Button onClick={() => void submit()} loading={busy} disabled={!valid}>
            {t('vps.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
