import { useEffect, useState } from 'react'
import {
  CaretDown,
  FolderOpen,
  Gear,
  GitBranch,
  ListChecks,
  PencilSimple,
  Play,
  SidebarSimple,
  Sparkle,
  TreeStructure,
  Wrench,
} from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { EnvDialog } from '@/components/chat/EnvDialog'
import { ChangesPanel, FilesPanel, GitPanel, ScriptsPanel } from '@/components/chat/ProjectPanels'

interface ProjectInfo {
  summary?: string
  languages?: string[]
  frameworks?: string[]
  key_libraries?: string[]
  build?: string
  run?: string
  test?: string
  notes?: string
}

interface PlanTask {
  text: string
  done: boolean
  section?: string
}
interface PlanSection {
  title: string
  level: number
  body: string
}
interface PlanResp {
  exists: boolean
  raw?: string
  tasks?: PlanTask[]
  sections?: PlanSection[]
}

interface SessionResp {
  session?: { meta?: { project_info?: ProjectInfo } | null }
}

/**
 * The right-hand panel shown only for project sessions. It sits beside the chat
 * (which narrows to make room) and can be collapsed to give the chat full width.
 *
 * It shows the essential project facts the agent recorded via the project_info
 * tool, a Plan viewer reading the project's .antares/plan.md, and a Settings
 * button opening the Environment (.env) editor. Everything re-fetches whenever
 * `refreshKey` changes (bumped when a turn finishes).
 */
type Tab = 'info' | 'plan' | 'git' | 'changes' | 'files' | 'scripts' | 'tools'

export function ProjectSidebar({
  projectDir,
  sessionId,
  refreshKey,
  changedFiles,
  toolStats,
  onRun,
  onCollapse,
}: {
  projectDir: string
  sessionId?: string
  refreshKey?: number
  // Files the agent wrote/edited this session (from tool calls).
  changedFiles: { path: string; tool: string }[]
  // Per-tool usage this session (from the transcript).
  toolStats: { name: string; count: number; last?: string }[]
  // Ask the agent to run a command (fills + sends the composer).
  onRun: (command: string) => void
  onCollapse: () => void
}) {
  const { t } = useI18n()
  const name = projectDir.split('/').filter(Boolean).pop() || projectDir
  const [info, setInfo] = useState<ProjectInfo | null>(null)
  const [plan, setPlan] = useState<PlanResp | null>(null)
  const [envOpen, setEnvOpen] = useState(false)
  const [tab, setTab] = useState<Tab>('info')

  useEffect(() => {
    if (sessionId) {
      let alive = true
      get<SessionResp>(`/sessions/${sessionId}`)
        .then((d) => alive && setInfo(d.session?.meta?.project_info ?? null))
        .catch(() => {})
      return () => {
        alive = false
      }
    }
    setInfo(null)
  }, [sessionId, refreshKey])

  useEffect(() => {
    let alive = true
    get<PlanResp>(`/project/plan?dir=${encodeURIComponent(projectDir)}`)
      .then((d) => alive && setPlan(d))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [projectDir, refreshKey])

  const hasInfo =
    info &&
    (info.summary ||
      info.languages?.length ||
      info.frameworks?.length ||
      info.key_libraries?.length ||
      info.build ||
      info.run ||
      info.test ||
      info.notes)

  return (
    <aside className="flex h-full w-full flex-col border-l border-border bg-card">
      <div className="flex items-center gap-2 border-b border-border px-3 py-3">
        <FolderOpen className="size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium" title={projectDir}>
            {name}
          </p>
          <p className="truncate font-mono text-[11px] text-dim" title={projectDir}>
            {projectDir}
          </p>
        </div>
        <button
          onClick={() => setEnvOpen(true)}
          title={t('env.title')}
          className="grid size-8 shrink-0 place-items-center rounded-full text-muted-foreground transition-colors hover:bg-raised hover:text-foreground"
        >
          <Gear className="size-4" />
        </button>
        <button
          onClick={onCollapse}
          title={t('project.sidebarHide')}
          className="grid size-8 shrink-0 place-items-center rounded-full text-muted-foreground transition-colors hover:bg-raised hover:text-foreground"
        >
          <SidebarSimple className="size-4" mirrored />
        </button>
      </div>

      {/* Tab navigation across the top; scrolls horizontally when narrow. A
          vertical mouse wheel over it scrolls the tabs left/right. */}
      <div
        className="flex shrink-0 gap-1 overflow-x-auto border-b border-border px-2 py-2 [scrollbar-width:none]"
        onWheel={(e) => {
          // Trackpads already send horizontal delta; a plain wheel only sends
          // vertical — translate it so the row scrolls sideways either way.
          if (Math.abs(e.deltaY) > Math.abs(e.deltaX)) {
            e.currentTarget.scrollLeft += e.deltaY
          }
        }}
      >
        {(
          [
            { id: 'info', icon: Sparkle, label: t('project.tabInfo') },
            { id: 'plan', icon: ListChecks, label: t('plan.title') },
            { id: 'git', icon: GitBranch, label: t('git.tab') },
            { id: 'changes', icon: PencilSimple, label: t('changes.tab'), badge: changedFiles.length },
            { id: 'files', icon: TreeStructure, label: t('files.tab') },
            { id: 'scripts', icon: Play, label: t('scripts.tab') },
            { id: 'tools', icon: Wrench, label: t('toolsUsage.tab') },
          ] as const
        ).map((tb) => (
          <button
            key={tb.id}
            onClick={() => setTab(tb.id)}
            title={tb.label}
            className={cn(
              'flex shrink-0 items-center gap-1 rounded-full px-2.5 py-1 text-[11px] transition-colors',
              tab === tb.id
                ? 'bg-nav-active text-foreground'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            <tb.icon className="size-3.5" />
            {tb.label}
            {'badge' in tb && tb.badge ? (
              <span className="rounded-full bg-raised px-1.5 font-mono text-[9px] tabular-nums text-foreground">{tb.badge}</span>
            ) : null}
          </button>
        ))}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {tab === 'info' ? (
          !hasInfo ? (
            <div className="flex flex-col items-center gap-2 py-6 text-center">
              <Sparkle className="size-6 text-muted-foreground/60" />
              <p className="text-[11px] leading-relaxed text-muted-foreground">
                {t('project.sidebarEmpty')}
              </p>
            </div>
          ) : (
            <div className="space-y-4">
              {info!.summary ? (
                <p className="text-xs leading-relaxed text-foreground/90">{info!.summary}</p>
              ) : null}
              <TagSection title={t('project.langs')} items={info!.languages} />
              <TagSection title={t('project.frameworks')} items={info!.frameworks} />
              <TagSection title={t('project.libraries')} items={info!.key_libraries} />
              <CmdSection title={t('project.build')} value={info!.build} />
              <CmdSection title={t('project.run')} value={info!.run} />
              <CmdSection title={t('project.test')} value={info!.test} />
              {info!.notes ? (
                <div className="space-y-1">
                  <SectionLabel>{t('project.notes')}</SectionLabel>
                  <p className="text-[11px] leading-relaxed text-muted-foreground">{info!.notes}</p>
                </div>
              ) : null}
            </div>
          )
        ) : tab === 'plan' ? (
          <PlanView plan={plan} />
        ) : tab === 'git' ? (
          <GitPanel projectDir={projectDir} refreshKey={refreshKey} />
        ) : tab === 'changes' ? (
          <ChangesPanel files={changedFiles} />
        ) : tab === 'files' ? (
          <FilesPanel projectDir={projectDir} refreshKey={refreshKey} />
        ) : tab === 'scripts' ? (
          <ScriptsPanel projectDir={projectDir} onRun={onRun} />
        ) : (
          <ToolsPanel sessionId={sessionId} toolStats={toolStats} refreshKey={refreshKey} />
        )}
      </div>

      <EnvDialog open={envOpen} onOpenChange={setEnvOpen} projectDir={projectDir} />
    </aside>
  )
}

function PlanView({ plan }: { plan: PlanResp | null }) {
  const { t } = useI18n()
  const [view, setView] = useState<'tasks' | 'raw'>('tasks')
  const [openSection, setOpenSection] = useState<string | null>(null)

  const tasks = plan?.tasks ?? []
  const sections = plan?.sections ?? []
  const done = tasks.filter((x) => x.done).length
  const pct = tasks.length ? Math.round((done / tasks.length) * 100) : 0

  return (
    <div className="space-y-2">
      {plan?.exists ? (
        <div className="flex justify-end">
          <div className="flex overflow-hidden rounded-full border border-border p-0.5">
            {(['tasks', 'raw'] as const).map((v) => (
              <button
                key={v}
                onClick={() => setView(v)}
                className={cn(
                  'rounded-full px-2.5 py-0.5 text-[10px] transition-colors',
                  view === v ? 'bg-nav-active text-foreground' : 'text-muted-foreground hover:text-foreground',
                )}
              >
                {v === 'tasks' ? t('plan.tabTable') : t('plan.tabRaw')}
              </button>
            ))}
          </div>
        </div>
      ) : null}

      {!plan?.exists ? (
        <p className="text-[11px] leading-relaxed text-muted-foreground">{t('plan.none')}</p>
      ) : view === 'raw' ? (
        <pre className="max-h-80 overflow-auto rounded-[var(--radius-md)] border border-border bg-background/40 p-2.5 font-mono text-[11px] leading-relaxed">
          {plan.raw}
        </pre>
      ) : (
        <div className="space-y-3">
          {tasks.length ? (
            <div className="space-y-1.5">
              <div className="flex items-center justify-between text-[10px] text-muted-foreground">
                <span>
                  {done}/{tasks.length} {t('plan.done')}
                </span>
                <span>{pct}%</span>
              </div>
              <div className="h-1 overflow-hidden rounded-full bg-raised">
                <div className="h-full rounded-full bg-foreground/70 transition-all" style={{ width: `${pct}%` }} />
              </div>
              <div className="space-y-1 pt-1">
                {tasks.map((task, i) => (
                  <div key={i} className="flex items-start gap-2 text-[11px]">
                    <span className={cn('mt-px', task.done ? 'text-[var(--success)]' : 'text-muted-foreground')}>
                      {task.done ? '☑' : '☐'}
                    </span>
                    <span className={task.done ? 'text-muted-foreground line-through' : ''}>{task.text}</span>
                  </div>
                ))}
              </div>
            </div>
          ) : null}

          {/* Structured sections (PRD/TRD/etc.), expandable. */}
          {sections.length ? (
            <div className="space-y-1">
              {sections
                .filter((s) => s.body)
                .map((s) => {
                  const isOpen = openSection === s.title
                  return (
                    <div key={s.title} className="overflow-hidden rounded-[var(--radius-md)] border border-border transition-colors hover:border-line">
                      <button
                        onClick={() => setOpenSection(isOpen ? null : s.title)}
                        className="flex w-full items-center gap-1.5 px-2 py-1.5 text-left text-[11px] font-medium transition-colors hover:bg-raised"
                      >
                        <CaretDown
                          className={cn('size-3 shrink-0 transition-transform', !isOpen && '-rotate-90')}
                        />
                        <span className="truncate">{s.title}</span>
                      </button>
                      {isOpen ? (
                        <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words border-t border-border px-2.5 py-2 font-mono text-[10.5px] leading-relaxed text-muted-foreground">
                          {s.body}
                        </pre>
                      ) : null}
                    </div>
                  )
                })}
            </div>
          ) : null}
        </div>
      )}
    </div>
  )
}

interface BgStat {
  name: string
  count: number
  last: string
}

// ToolsPanel shows how tools were used this session: model tool calls (from the
// transcript — read_file ×N, etc.) and background actions (RAG index/retrieve,
// which never appear in the transcript), each with a last-used time.
function ToolsPanel({
  sessionId,
  toolStats,
  refreshKey,
}: {
  sessionId?: string
  toolStats: { name: string; count: number; last?: string }[]
  refreshKey?: number
}) {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [bg, setBg] = useState<BgStat[]>([])

  useEffect(() => {
    if (!sessionId) {
      setBg([])
      return
    }
    let alive = true
    get<{ activity: BgStat[] }>(`/sessions/${sessionId}/bg-activity`)
      .then((d) => alive && setBg(d.activity ?? []))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [sessionId, refreshKey])

  const row = (name: string, count: number, last?: string) => (
    <div key={name} className="flex items-center gap-2 py-1 text-[11px]">
      <span className="min-w-0 flex-1 truncate font-mono">{name}</span>
      {last ? <span className="shrink-0 text-[10px] text-muted-foreground">{timeAgo(last)}</span> : null}
      <span className="shrink-0 rounded-full bg-raised px-1.5 py-0.5 font-mono text-[10px] tabular-nums">
        {count}×
      </span>
    </div>
  )

  return (
    <div className="space-y-4">
      <div className="space-y-0.5">
        <SectionLabel>{t('toolsUsage.calls')}</SectionLabel>
        {toolStats.length ? (
          toolStats.map((s) => row(s.name, s.count, s.last))
        ) : (
          <p className="py-2 text-[11px] text-muted-foreground">{t('toolsUsage.none')}</p>
        )}
      </div>
      <div className="space-y-0.5 border-t border-border pt-3">
        <SectionLabel>{t('toolsUsage.background')}</SectionLabel>
        {bg.length ? (
          bg.map((s) => row(s.name, s.count, s.last))
        ) : (
          <p className="py-2 text-[11px] text-muted-foreground">{t('toolsUsage.noBackground')}</p>
        )}
      </div>
    </div>
  )
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <p className="eyebrow !text-[10px]">
      {children}
    </p>
  )
}

function TagSection({ title, items }: { title: string; items?: string[] }) {
  if (!items?.length) return null
  return (
    <div className="space-y-1.5">
      <SectionLabel>{title}</SectionLabel>
      <div className="flex flex-wrap gap-1.5">
        {items.map((it) => (
          <span
            key={it}
            className="rounded-full border border-border bg-raised px-2.5 py-0.5 text-[11px]"
          >
            {it}
          </span>
        ))}
      </div>
    </div>
  )
}

function CmdSection({ title, value }: { title: string; value?: string }) {
  if (!value) return null
  return (
    <div className="space-y-1">
      <SectionLabel>{title}</SectionLabel>
      <code className="block overflow-x-auto rounded-[var(--radius-sm)] border border-border bg-background/40 px-2 py-1 font-mono text-[11px]">
        {value}
      </code>
    </div>
  )
}
