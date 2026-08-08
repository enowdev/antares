import { useEffect, useRef, useState } from 'react'
import { ArrowLeft, CircleNotch, UsersThree } from '@phosphor-icons/react'
import { get, streamGet, type StreamEvent } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import {
  DEFAULT_MAX_LIVE_REASONING_CHARS,
  MessageBubble,
  appendSeg,
  pushToolSeg,
  updateToolSeg,
  type ChatMessage,
} from '@/pages/ChatPage'

export interface ActiveAgent {
  id: string
  role: string
  task: string
  parent: string
  started_at: string
}

/**
 * A full-height overlay that shows one sub-agent's live transcript, built from
 * its own SSE stream (the same event shape as the main chat). The back control
 * returns to the main agent. Rendering reuses MessageBubble so a sub-agent's
 * text, reasoning, and tool calls look exactly like the main agent's.
 */
export function SubAgentPanel({ agent, onBack }: { agent: ActiveAgent; onBack: () => void }) {
  const { t } = useI18n()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [done, setDone] = useState(false)
  const [showReasoning, setShowReasoning] = useState(true)
  const showReasoningRef = useRef(true)
  const maxLiveReasoningRef = useRef(DEFAULT_MAX_LIVE_REASONING_CHARS)
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    showReasoningRef.current = showReasoning
  }, [showReasoning])

  useEffect(() => {
    get<{
      values?: { display?: { show_reasoning?: boolean; max_live_reasoning_chars?: number } }
    }>('/config')
      .then((d) => {
        const disp = d.values?.display
        if (disp && typeof disp.show_reasoning === 'boolean') setShowReasoning(disp.show_reasoning)
        const n = Number(disp?.max_live_reasoning_chars)
        if (Number.isFinite(n)) {
          maxLiveReasoningRef.current = n < 0 ? DEFAULT_MAX_LIVE_REASONING_CHARS : n
        }
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    // Reset when switching between sub-agents.
    setMessages([])
    setDone(false)
    const id = `sub_${agent.id}`
    // Seed a single assistant message the stream fills in, mirroring how the
    // main transcript renders one streaming turn.
    setMessages([{ id, role: 'assistant', content: '' }])

    // Batch stream patches once per frame — sub-agents stream the same dense
    // reasoning token firehose as the main chat and used to setState per token.
    let pending: ((m: ChatMessage) => ChatMessage) | null = null
    let raf: number | null = null
    const flush = () => {
      raf = null
      const fn = pending
      pending = null
      if (!fn) return
      setMessages((prev) => {
        const idx = prev.findIndex((m) => m.id === id)
        if (idx < 0) return prev
        const next = prev.slice()
        next[idx] = fn(prev[idx])
        return next
      })
    }
    const patch = (fn: (m: ChatMessage) => ChatMessage) => {
      const prev = pending
      pending = prev ? (m) => fn(prev(m)) : fn
      if (raf == null) raf = requestAnimationFrame(flush)
    }

    const close = streamGet(
      `/subagent/${encodeURIComponent(agent.id)}/attach`,
      (event: StreamEvent) => {
        switch (event.type) {
          case 'text':
            patch((m) =>
              appendSeg(m, 'text', String(event.delta ?? ''), maxLiveReasoningRef.current),
            )
            break
          case 'reasoning':
            if (!showReasoningRef.current) break
            patch((m) =>
              appendSeg(m, 'reasoning', String(event.delta ?? ''), maxLiveReasoningRef.current),
            )
            break
          case 'tool_call':
            patch((m) =>
              pushToolSeg(m, {
                id: String(event.id ?? ''),
                name: String(event.name ?? ''),
                args: String(event.arguments ?? ''),
                running: true,
              }),
            )
            break
          case 'tool_progress':
            patch((m) =>
              updateToolSeg(m, String(event.id ?? ''), (c) => ({
                ...c,
                // chunk = streaming (append); message = status line (replace).
                progress:
                  event.chunk != null
                    ? (c.progress ?? '') + String(event.chunk)
                    : String(event.message ?? c.progress ?? ''),
              })),
            )
            break
          case 'tool_result':
            patch((m) =>
              updateToolSeg(m, String(event.id ?? ''), (c) => ({
                ...c,
                result: String(event.content ?? ''),
                isError: !!event.is_error,
                running: false,
              })),
            )
            break
          case 'error':
            patch((m) => ({ ...m, error: String(event.error ?? '') }))
            break
          case 'done':
            if (raf != null) {
              cancelAnimationFrame(raf)
              raf = null
            }
            if (pending) flush()
            setDone(true)
            break
        }
      },
      () => setDone(true),
    )
    return () => {
      if (raf != null) cancelAnimationFrame(raf)
      close()
    }
  }, [agent.id])

  // Keep the newest output in view as it streams.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' })
  }, [messages])

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* Header: back to main + which sub-agent this is. */}
      <div className="sticky top-0 z-10 flex items-center gap-2 border-b border-border bg-background/95 px-4 py-2.5 backdrop-blur sm:px-6">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-1.5 rounded-[var(--radius-sm)] border border-border px-2 py-1 text-xs text-muted-foreground transition hover:bg-muted hover:text-foreground"
        >
          <ArrowLeft className="size-3.5" />
          {t('subagents.backToMain')}
        </button>
        <div className="flex min-w-0 flex-1 items-center gap-1.5">
          <UsersThree className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate text-sm font-medium">{agent.role || 'assistant'}</span>
          {!done ? (
            <CircleNotch className="size-3.5 shrink-0 animate-spin text-primary" />
          ) : (
            <span className="text-[11px] text-muted-foreground">{t('subagents.finished')}</span>
          )}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-6">
        <div className="mx-auto w-full max-w-3xl space-y-5">
          {agent.task ? (
            <div className="rounded-[var(--radius-md)] border-l-2 border-primary bg-muted/40 px-3.5 py-2.5">
              <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                {t('subagents.task')}
              </p>
              <p className="mt-0.5 whitespace-pre-wrap break-words text-[13px] text-foreground">
                {agent.task}
              </p>
            </div>
          ) : null}
          {messages.map((m) => (
            <MessageBubble key={m.id} message={m} showReasoning={showReasoning} />
          ))}
          <div ref={bottomRef} />
        </div>
      </div>
    </div>
  )
}
