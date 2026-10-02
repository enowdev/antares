import { useEffect, useState } from 'react'
import { CaretDown, CaretUp, CheckCircle, Circle, CircleNotch, UsersThree } from '@phosphor-icons/react'
import { streamGet, type StreamEvent } from '@/lib/api'
import { cn } from '@/lib/utils'
import { useI18n } from '@/lib/i18n'
import type { ActiveAgent } from '@/components/chat/SubAgentPanel'

export type TodoItem = { content: string; status: string }

/** Parse the current task list out of a `todo` tool call's write args. */
export function parseTasks(rawArgs: string): TodoItem[] {
  try {
    const parsed = JSON.parse(rawArgs || '{}') as { items?: TodoItem[] }
    return Array.isArray(parsed.items) ? parsed.items : []
  } catch {
    return []
  }
}

// `live` is whether a turn is actively streaming. When it is not, an
// in_progress item shows a static dot instead of a spinner, so the checklist
// stops "running forever" once the run ends (whether it succeeded or failed).
const glyph = (status: string, live: boolean) =>
  status === 'completed' ? (
    <CheckCircle className="size-3.5 shrink-0 text-[var(--success)]" weight="fill" />
  ) : status === 'in_progress' ? (
    live ? (
      <CircleNotch className="size-3.5 shrink-0 animate-spin text-foreground" />
    ) : (
      <Circle className="size-3.5 shrink-0 text-foreground/60" weight="fill" />
    )
  ) : (
    <Circle className="size-3.5 shrink-0 text-muted-foreground/40" />
  )

/**
 * A sticky bar that butts up against the top of the composer. It carries two
 * views behind a small tab row: the agent's task checklist, and the live list
 * of running sub-agents. Clicking a sub-agent asks the parent to open its live
 * transcript (onOpenSubAgent). Mirrors a mission task bar.
 */
export function TaskBar({
  tasks,
  live = false,
  session,
  onOpenSubAgent,
}: {
  tasks: TodoItem[]
  live?: boolean
  // The current chat session, so the sub-agent list is scoped to workers THIS
  // session spawned — otherwise another session's sub-agents leak in.
  session?: string
  onOpenSubAgent?: (agent: ActiveAgent) => void
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [tab, setTab] = useState<'tasks' | 'agents'>('tasks')

  // Subscribe to the swarm so the sub-agent list tracks who is running live,
  // scoped to this session. A session with no id yet (brand-new chat) has no
  // sub-agents of its own, so skip the subscription and show none.
  const [agents, setAgents] = useState<ActiveAgent[]>([])
  useEffect(() => {
    setAgents([])
    if (!session) return
    const close = streamGet(
      `/swarm/stream?session=${encodeURIComponent(session)}`,
      (e: StreamEvent) => {
        const list = (e as unknown as { active?: ActiveAgent[] }).active
        if (list) setAgents(list)
      },
    )
    return close
  }, [session])

  // Nothing to show at all: no tasks and no sub-agents. Stay out of the way.
  if (tasks.length === 0 && agents.length === 0) return null

  const done = tasks.filter((it) => it.status === 'completed').length
  const active = live ? tasks.find((it) => it.status === 'in_progress') : undefined

  // If a tab's content vanishes (e.g. all sub-agents finished), fall back to the
  // one that still has content so the header summary stays meaningful.
  const showAgentsTab = agents.length > 0
  const effectiveTab = tab === 'agents' && !showAgentsTab ? 'tasks' : tab

  return (
    // Borderless: this renders inside the composer card as its top section.
    <div>
      <div className="flex items-center gap-2 px-3 py-2">
        {/* Tab switch — only shows the sub-agents tab when any are running. */}
        {showAgentsTab ? (
          <div className="flex shrink-0 items-center gap-0.5 rounded-full border border-border p-0.5">
            <button
              type="button"
              onClick={() => {
                setTab('tasks')
                setOpen(true)
              }}
              className={cn(
                'rounded-full px-2.5 py-0.5 text-[11px] transition-colors',
                effectiveTab === 'tasks'
                  ? 'bg-nav-active text-foreground'
                  : 'text-muted-foreground hover:text-foreground',
              )}
            >
              {t('chat.tasks')}
            </button>
            <button
              type="button"
              onClick={() => {
                setTab('agents')
                setOpen(true)
              }}
              className={cn(
                'flex items-center gap-1 rounded-full px-2.5 py-0.5 text-[11px] transition-colors',
                effectiveTab === 'agents'
                  ? 'bg-nav-active text-foreground'
                  : 'text-muted-foreground hover:text-foreground',
              )}
            >
              <UsersThree className="size-3" />
              {t('subagents.title')}
              <span className="tabular-nums">{agents.length}</span>
            </button>
          </div>
        ) : null}

        {/* Summary row (clickable to expand/collapse). */}
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
        >
          {effectiveTab === 'tasks' && active ? (
            <>
              <CircleNotch className="size-3.5 shrink-0 animate-spin text-foreground" />
              <span className="min-w-0 flex-1 truncate text-[12px] text-foreground">
                {active.content}
              </span>
            </>
          ) : (
            <span className="eyebrow min-w-0 flex-1 truncate !text-[10.5px]">
              {effectiveTab === 'tasks' ? t('chat.tasks') : t('subagents.running')}
            </span>
          )}
          {effectiveTab === 'tasks' && tasks.length > 0 ? (
            <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground">
              {done}/{tasks.length}
            </span>
          ) : null}
          {open ? (
            <CaretDown className="size-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <CaretUp className="size-3.5 shrink-0 text-muted-foreground" />
          )}
        </button>
      </div>

      {open && effectiveTab === 'tasks' && tasks.length > 0 ? (
        <ul className="m-fade max-h-48 space-y-1.5 overflow-y-auto border-t border-border px-3 py-2">
          {tasks.map((it, i) => (
            <li key={i} className="flex items-start gap-2 text-[12px] leading-snug">
              <span className="mt-0.5">{glyph(it.status, live)}</span>
              <span
                className={cn(
                  'min-w-0 flex-1',
                  it.status === 'completed'
                    ? 'text-muted-foreground line-through'
                    : it.status === 'in_progress'
                      ? 'font-medium text-foreground'
                      : 'text-muted-foreground',
                )}
              >
                {it.content}
              </span>
            </li>
          ))}
        </ul>
      ) : null}

      {open && effectiveTab === 'agents' ? (
        <ul className="m-fade max-h-48 space-y-1 overflow-y-auto border-t border-border px-2 py-2">
          {agents.map((a) => (
            <li key={a.id}>
              <button
                type="button"
                onClick={() => onOpenSubAgent?.(a)}
                className="flex w-full items-start gap-2 rounded-[var(--radius-sm)] px-2 py-1.5 text-left transition-colors hover:bg-raised"
              >
                <CircleNotch className="mt-0.5 size-3.5 shrink-0 animate-spin text-foreground" />
                <span className="min-w-0 flex-1">
                  <span className="block font-mono text-[12px] text-foreground">
                    {a.role || 'assistant'}
                  </span>
                  <span className="block truncate text-[11px] text-muted-foreground">{a.task}</span>
                </span>
                <CaretDown className="mt-0.5 size-3.5 shrink-0 -rotate-90 text-muted-foreground" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}

      {/* Divider to the composer below — only present because this whole
          component already returned null when there is nothing to show. */}
      <div className="h-px bg-border" />
    </div>
  )
}
