import { useEffect, useState } from 'react'
import { File as FileIcon, Folder, GitBranch, PencilSimple, Play } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'

// ---- Git ---------------------------------------------------------------------

interface GitResp {
  repo: boolean
  branch?: string
  ahead?: number
  behind?: number
  last?: string
  changes?: { path: string; status: string }[]
}

const GIT_TONE: Record<string, string> = {
  staged: 'text-[var(--success)]',
  modified: 'text-[var(--warning)]',
  untracked: 'text-muted-foreground',
  deleted: 'text-[var(--destructive)]',
}

export function GitPanel({ projectDir, refreshKey }: { projectDir: string; refreshKey?: number }) {
  const { t } = useI18n()
  const [git, setGit] = useState<GitResp | null>(null)

  useEffect(() => {
    let alive = true
    get<GitResp>(`/project/git?dir=${encodeURIComponent(projectDir)}`)
      .then((d) => alive && setGit(d))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [projectDir, refreshKey])

  if (!git) return <PanelHint text={t('common.loadFailed')} />
  if (!git.repo) return <PanelHint text={t('git.noRepo')} />

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-1.5 text-xs">
        <GitBranch className="size-3.5 text-muted-foreground" />
        <span className="font-medium">{git.branch || 'HEAD'}</span>
        {git.ahead ? <Badge>↑{git.ahead}</Badge> : null}
        {git.behind ? <Badge>↓{git.behind}</Badge> : null}
      </div>
      {git.last ? (
        <p className="truncate font-mono text-[11px] text-muted-foreground" title={git.last}>
          {git.last}
        </p>
      ) : null}
      {git.changes && git.changes.length ? (
        <div className="space-y-1">
          <SectionLabel>
            {t('git.changes')} ({git.changes.length})
          </SectionLabel>
          {git.changes.map((c) => (
            <div key={c.path} className="flex items-center gap-2 font-mono text-[11px]">
              <span className={cn('w-16 shrink-0 uppercase', GIT_TONE[c.status])}>{c.status}</span>
              <span className="truncate" title={c.path}>
                {c.path}
              </span>
            </div>
          ))}
        </div>
      ) : (
        <PanelHint text={t('git.clean')} />
      )}
    </div>
  )
}

// ---- Session file changes (from tool calls) ---------------------------------

export function ChangesPanel({ files }: { files: { path: string; tool: string }[] }) {
  const { t } = useI18n()
  if (!files.length) return <PanelHint text={t('changes.none')} />
  return (
    <div className="space-y-1">
      <SectionLabel>
        {t('changes.title')} ({files.length})
      </SectionLabel>
      {files.map((f) => (
        <div key={f.path} className="flex items-center gap-2 font-mono text-[11px]">
          <PencilSimple
            className={cn('size-3 shrink-0', f.tool === 'write_file' ? 'text-[var(--success)]' : 'text-[var(--warning)]')}
          />
          <span className="truncate" title={f.path}>
            {f.path}
          </span>
        </div>
      ))}
    </div>
  )
}

// ---- File tree ---------------------------------------------------------------

interface TreeNode {
  name: string
  path: string
  is_dir: boolean
}

export function FilesPanel({ projectDir, refreshKey }: { projectDir: string; refreshKey?: number }) {
  const { t } = useI18n()
  // Expanded directories keyed by their project-relative path; each holds its
  // loaded children. Root is the empty-string key.
  const [tree, setTree] = useState<Record<string, TreeNode[]>>({})
  const [open, setOpen] = useState<Record<string, boolean>>({})

  const load = (sub: string) => {
    get<{ entries: TreeNode[] }>(`/project/tree?dir=${encodeURIComponent(projectDir)}&sub=${encodeURIComponent(sub)}`)
      .then((d) => setTree((prev) => ({ ...prev, [sub]: d.entries ?? [] })))
      .catch(() => {})
  }

  useEffect(() => {
    setTree({})
    setOpen({})
    load('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectDir, refreshKey])

  const toggle = (node: TreeNode) => {
    if (!node.is_dir) return
    const isOpen = open[node.path]
    setOpen((p) => ({ ...p, [node.path]: !isOpen }))
    if (!isOpen && !tree[node.path]) load(node.path)
  }

  const render = (sub: string, depth: number) =>
    (tree[sub] ?? []).map((node) => (
      <div key={node.path}>
        <button
          onClick={() => toggle(node)}
          style={{ paddingLeft: `${depth * 12 + 4}px` }}
          className="flex w-full items-center gap-1.5 rounded-[var(--radius-sm)] py-0.5 pr-1 text-left font-mono text-[11px] transition-colors hover:bg-raised"
        >
          {node.is_dir ? (
            <Folder className="size-3.5 shrink-0 text-muted-foreground" weight={open[node.path] ? 'fill' : 'regular'} />
          ) : (
            <FileIcon className="size-3.5 shrink-0 text-muted-foreground/70" />
          )}
          <span className="truncate">{node.name}</span>
        </button>
        {node.is_dir && open[node.path] ? render(node.path, depth + 1) : null}
      </div>
    ))

  if (!tree['']) return <PanelHint text={t('common.loadFailed')} />
  return <div className="space-y-0.5">{render('', 0)}</div>
}

// ---- Scripts -----------------------------------------------------------------

interface Script {
  name: string
  command: string
  source: string
}

export function ScriptsPanel({
  projectDir,
  onRun,
}: {
  projectDir: string
  // onRun asks the agent to run the command (fills the composer / sends).
  onRun: (command: string) => void
}) {
  const { t } = useI18n()
  const [scripts, setScripts] = useState<Script[] | null>(null)

  useEffect(() => {
    let alive = true
    get<{ scripts: Script[] }>(`/project/scripts?dir=${encodeURIComponent(projectDir)}`)
      .then((d) => alive && setScripts(d.scripts ?? []))
      .catch(() => alive && setScripts([]))
    return () => {
      alive = false
    }
  }, [projectDir])

  if (!scripts) return <PanelHint text={t('common.loadFailed')} />
  if (!scripts.length) return <PanelHint text={t('scripts.none')} />
  return (
    <div className="space-y-1.5">
      {scripts.map((sc) => (
        <div
          key={`${sc.source}:${sc.name}`}
          className="flex items-center gap-2 rounded-[var(--radius-md)] border border-border px-2 py-1.5 transition-[border-color,background-color] duration-200 hover:border-line hover:bg-raised"
        >
          <div className="min-w-0 flex-1">
            <p className="truncate text-[11px] font-medium">{sc.name}</p>
            <p className="truncate font-mono text-[10px] text-muted-foreground">{sc.command}</p>
          </div>
          <button
            onClick={() => onRun(sc.command)}
            title={t('scripts.run', { command: sc.command })}
            className="grid size-7 shrink-0 place-items-center rounded-full text-muted-foreground transition-colors hover:bg-nav-active hover:text-foreground"
          >
            <Play className="size-3.5" weight="fill" />
          </button>
        </div>
      ))}
    </div>
  )
}

// ---- shared ------------------------------------------------------------------

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <p className="eyebrow !text-[10px]">{children}</p>
  )
}

function Badge({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-full bg-raised px-1.5 py-0.5 font-mono text-[10px] tabular-nums text-muted-foreground">
      {children}
    </span>
  )
}

function PanelHint({ text }: { text: string }) {
  return <p className="py-4 text-center text-[11px] text-muted-foreground">{text}</p>
}
