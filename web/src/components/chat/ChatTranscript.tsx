import { memo, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import {
  ArrowClockwise,
  ArrowUp,
  Brain,
  CaretDown,
  Check,
  Copy,
  FileText,
  FileX,
  PencilSimple,
  Terminal,
  Warning,
} from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { copyText } from '@/lib/clipboard'
import { useI18n, useTimeAgo } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { shouldCancelEditOnPointer, shouldFadeStickyPrompt, nextPromptLineCount, stickyPromptClipClass, type ChatMessage } from '@/lib/chatTranscript'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/primitives'
import { Markdown } from '@/components/chat/Markdown'
import { ToolCallCard } from '@/components/chat/ToolCallCard'
import { AskUserCard } from '@/components/chat/AskUserCard'

/**
 * Collapsible model-thinking block.
 *
 * Must NOT run the chat Markdown renderer on expand: reasoning traces are long
 * (tens of KB of decompiler/code-like text with many `*`/`[]`), and turning
 * that into hundreds of React nodes freezes the tab ("Page Unresponsive").
 * Plain pre-wrap text in a height-capped scroller is one DOM node, cheap to
 * open, and matches how thinking logs are meant to be read.
 *
 * Memoised for the same reason as ToolCallCard: on a message that grows to many
 * segments during one streaming turn, only the changed segment should re-render.
 * `text` is a primitive, so memo compares by value and finished blocks are free.
 */
const ReasoningBlock = memo(function ReasoningBlock({ text }: { text: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  // Defer mounting the body to the next frame so the click paints first and
  // Chrome does not treat the expand as a long task on the same turn.
  const [bodyReady, setBodyReady] = useState(false)
  useEffect(() => {
    if (!open) {
      setBodyReady(false)
      return
    }
    const id = requestAnimationFrame(() => setBodyReady(true))
    return () => cancelAnimationFrame(id)
  }, [open])

  return (
    <div className="text-muted-foreground">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex items-center gap-1.5 font-mono text-[11px] lowercase transition-colors hover:text-foreground"
      >
        <Brain className="size-3.5" />
        {t('chat.reasoning')}
        {text.length > 2000 ? (
          <span className="font-normal text-muted-foreground/70">
            ({Math.round(text.length / 1000)}k)
          </span>
        ) : null}
        <CaretDown className={cn('size-3 transition-transform', open && 'rotate-180')} />
      </button>
      {open ? (
        <div className="m-fade mt-1.5 max-h-80 overflow-y-auto overflow-x-hidden border-l border-line pl-3">
          {bodyReady ? (
            <pre className="m-0 whitespace-pre-wrap break-words font-mono text-[11px] leading-relaxed text-muted-foreground">
              {text}
            </pre>
          ) : (
            <p className="m-0 text-[11px] text-muted-foreground/60">…</p>
          )}
        </div>
      ) : null}
    </div>
  )
})

// A plain-text segment. Memoised so it is skipped when an unrelated segment on
// the same message changes during streaming.
const TextSegment = memo(function TextSegment({ text }: { text: string }) {
  return (
    <div className="text-[15px] leading-7">
      <Markdown content={text} className="space-y-3" />
    </div>
  )
})

export function ErrorBanner({ message, className }: { message: string; className?: string }) {
  return (
    <div
      className={cn(
        'm-rise flex items-start gap-2 rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card px-4 py-3 text-sm text-destructive',
        className,
      )}
    >
      <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
      <span className="min-w-0 break-words">{message}</span>
    </div>
  )
}

/** Terminal-turn error: renders the JSON payload verbatim in a monospace
 *  block with Copy (of the exact JSON) and optional Retry. Not run through
 *  Markdown — a `**` inside an error message would break the format. */
export function AssistantErrorBlock({
  payload,
  onCopy,
  onRetry,
  retryDisabled,
}: {
  payload: string
  onCopy: () => Promise<boolean> | boolean | void
  onRetry?: () => void
  retryDisabled?: boolean
}) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const doCopy = async () => {
    if (await onCopy()) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }
  return (
    <div className="overflow-hidden rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-card text-destructive">
      <div className="flex items-center justify-between gap-2 border-b border-[color-mix(in_oklch,var(--destructive)_30%,var(--border))] px-3 py-1">
        <div className="flex items-center gap-2 font-mono text-xs lowercase">
          <Warning className="size-4 shrink-0" weight="fill" />
          <span>{t('chat.errorLabel')}</span>
        </div>
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={doCopy}
            title={t('chat.copyError')}
            aria-label={t('chat.copyError')}
            className="inline-flex min-h-11 min-w-11 items-center justify-center gap-1.5 rounded-full px-3 font-mono text-xs lowercase transition-colors hover:bg-raised focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-destructive"
          >
            {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
            <span className="hidden sm:inline">{copied ? t('common.copied') : t('common.copy')}</span>
          </button>
          {onRetry ? (
            <button
              type="button"
              onClick={onRetry}
              disabled={retryDisabled}
              title={t('chat.retry')}
              aria-label={t('chat.retry')}
              className="inline-flex min-h-11 min-w-11 items-center justify-center gap-1.5 rounded-full px-3 font-mono text-xs lowercase transition-colors hover:bg-raised disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-destructive"
            >
              <ArrowClockwise className="size-3.5" />
              <span className="hidden sm:inline">{t('chat.retry')}</span>
            </button>
          ) : null}
        </div>
      </div>
      <pre className="m-0 max-h-64 overflow-auto whitespace-pre-wrap break-words px-3 py-2 font-mono text-[11px] leading-relaxed">
        {payload}
      </pre>
    </div>
  )
}

interface FileChange {
  path: string
  externally_changed: boolean
  will_delete: boolean
}

function autosize(el: HTMLTextAreaElement | null) {
  if (!el) return
  el.style.height = '0px'
  el.style.height = `${el.scrollHeight}px`
}

/** Inline user-prompt editor. Enter inserts a newline; only the send button
 *  (or ⌘/Ctrl+Enter) submits, so a mid-edit Return does not re-send. */
function InlineUserEditor({
  sessionId,
  messageId,
  initialText,
  onSubmit,
  onCancel,
}: {
  sessionId?: string
  messageId: string
  initialText: string
  onSubmit: (text: string, revert: boolean) => void
  onCancel: () => void
}) {
  const { t } = useI18n()
  const ref = useRef<HTMLTextAreaElement>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const [text, setText] = useState(initialText)
  const [revert, setRevert] = useState(true)
  const [changes, setChanges] = useState<FileChange[]>([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setText(initialText)
    setRevert(true)
    setChanges([])
    requestAnimationFrame(() => {
      autosize(ref.current)
      ref.current?.focus()
      const el = ref.current
      if (el) el.setSelectionRange(el.value.length, el.value.length)
    })
    if (!sessionId || !messageId) return
    setLoading(true)
    get<{ changes: FileChange[] }>(
      `/sessions/${sessionId}/edit-preview?message_id=${encodeURIComponent(messageId)}`,
    )
      .then((d) => setChanges(d.changes ?? []))
      .catch(() => setChanges([]))
      .finally(() => setLoading(false))
  }, [sessionId, messageId, initialText])

  useEffect(() => {
    const onPointerDown = (e: PointerEvent) => {
      if (e.button !== 0) return
      if (shouldCancelEditOnPointer(e.target instanceof Node ? e.target : null, rootRef.current)) onCancel()
    }
    document.addEventListener('pointerdown', onPointerDown)
    return () => document.removeEventListener('pointerdown', onPointerDown)
  }, [onCancel])

  const revertable = changes.filter((c) => !c.externally_changed)
  const external = changes.filter((c) => c.externally_changed)
  const send = () => {
    const next = text.trim()
    if (!next) return
    onSubmit(next, revert && revertable.length > 0)
  }

  return (
    <div ref={rootRef} className="rounded-2xl bg-secondary px-4 pb-3 pt-3">
      <Textarea
        ref={ref}
        value={text}
        rows={1}
        onChange={(e) => {
          setText(e.target.value)
          autosize(e.currentTarget)
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            onCancel()
            return
          }
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault()
            send()
          }
        }}
        className="max-h-80 min-h-11 w-full resize-none border-0 bg-transparent px-0 py-0 text-[15px] leading-7 shadow-none outline-none focus-visible:border-0 focus-visible:ring-0"
      />

      {loading ? (
        <p className="mt-2 text-[11px] text-muted-foreground">{t('edit.checking')}</p>
      ) : changes.length ? (
        <div className="mt-2 space-y-1.5">
          <label className="flex items-start gap-2">
            <input
              type="checkbox"
              checked={revert}
              onChange={(e) => setRevert(e.target.checked)}
              className="mt-0.5"
            />
            <span className="text-[11px] text-muted-foreground">
              {t('edit.revertLabel', { n: revertable.length })}
            </span>
          </label>
          {revertable.map((c) => (
            <div key={c.path} className="flex items-center gap-1.5 pl-6 font-mono text-[11px]">
              <FileX className="size-3 shrink-0 text-[var(--warning)]" />
              <span className="truncate">{c.path}</span>
            </div>
          ))}
          {external.map((c) => (
            <div
              key={c.path}
              className="flex items-center gap-1.5 pl-6 font-mono text-[11px] text-muted-foreground"
              title={t('edit.externalTip')}
            >
              <Warning className="size-3 shrink-0" />
              <span className="truncate">{c.path}</span>
            </div>
          ))}
        </div>
      ) : null}

      <div className="mt-2 flex justify-end">
        <Button
          size="icon"
          disabled={!text.trim()}
          onClick={send}
          aria-label={t('edit.resend')}
          className="shrink-0 rounded-full"
        >
          <ArrowUp weight="bold" />
        </Button>
      </div>
    </div>
  )
}

function nearestScroller(el: HTMLElement | null): Element | null {
  let n: HTMLElement | null = el
  while (n) {
    const oy = getComputedStyle(n).overflowY
    if (oy === 'auto' || oy === 'scroll' || oy === 'overlay') return n
    n = n.parentElement
  }
  return null
}

/**
 * Blog-style sticky prompt: the bubble stays in document flow, then docks at
 * the scroller top. Once docked, long text fades after the first line.
 */
export function StickyUserAnchor({ children }: { children: (compact: boolean) => ReactNode }) {
  const sentinelRef = useRef<HTMLDivElement>(null)
  const [stuck, setStuck] = useState(false)

  useEffect(() => {
    const sentinel = sentinelRef.current
    if (!sentinel) return
    const root = nearestScroller(sentinel.parentElement)
    const io = new IntersectionObserver(
      ([entry]) => {
        if (!entry) return
        setStuck(!entry.isIntersecting)
      },
      { root, threshold: 0 },
    )
    io.observe(sentinel)
    return () => io.disconnect()
  }, [])

  return (
    <>
      <div ref={sentinelRef} className="h-px w-full" aria-hidden />
      <div className="sticky top-0 z-10 bg-background py-4">
        {children(stuck)}
      </div>
    </>
  )
}

const USER_BUBBLE_FILL = 'color-mix(in srgb, var(--primary) 10%, var(--secondary))'

function UserPromptBubble({
  compact,
  lines,
  onEdit,
  editLabel,
  children,
}: {
  compact: boolean
  lines: number
  onEdit?: () => void
  editLabel: string
  children: ReactNode
}) {
  const fade = shouldFadeStickyPrompt(compact, lines)
  return (
    <div
      className={cn(
        'group/user relative rounded-2xl px-4 py-3 pr-11',
        'shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--primary)_30%,transparent)]',
        stickyPromptClipClass(fade),
        onEdit && 'cursor-pointer',
      )}
      style={{ background: USER_BUBBLE_FILL }}
      onClick={() => {
        if (!onEdit) return
        if (window.getSelection()?.toString()) return
        onEdit()
      }}
    >
      {children}
      <div
        aria-hidden
        className={cn(
          'pointer-events-none absolute inset-x-0 bottom-0 h-9 rounded-b-2xl transition-opacity duration-200 ease-in-out',
          fade ? 'opacity-100' : 'opacity-0',
        )}
        style={{
          backgroundImage: `linear-gradient(to top, ${USER_BUBBLE_FILL} 28%, transparent)`,
        }}
      />
      {onEdit && !fade ? (
        <button
          onClick={(e) => {
            e.stopPropagation()
            onEdit()
          }}
          title={editLabel}
          aria-label={editLabel}
          className="absolute bottom-2 right-2 z-10 hidden size-7 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-background/80 hover:text-foreground group-hover/user:flex"
        >
          <PencilSimple className="size-3.5" />
        </button>
      ) : null}
    </div>
  )
}

// Memoised: a streaming turn mutates only the last message, but setMessages
// hands a new array each token. Without memo every bubble in a long transcript
// re-renders per token — the main source of lag. With stable props (message
// reference unchanged for old turns, onAnswer via useCallback), React skips
// them and only the changed bubble re-renders.
export const MessageBubble = memo(function MessageBubble({
  message,
  showReasoning = true,
  askActive,
  onAnswer,
  onEdit,
  editing = false,
  onSubmitEdit,
  onCancelEdit,
  sessionId,
  onRetry,
  retryDisabled,
  compact = false,
}: {
  message: ChatMessage
  /** When false, hide reasoning blocks (display.show_reasoning). */
  showReasoning?: boolean
  // Whether an ask_user question is still awaiting an answer. When false the
  // card locks (already answered, or the run ended).
  askActive?: boolean
  onAnswer?: (text: string) => void
  // Edit this (user) message: re-send from here, optionally reverting file
  // changes made since. Absent while streaming or for a local optimistic msg.
  onEdit?: (id: string, content: string) => void
  editing?: boolean
  onSubmitEdit?: (text: string, revert: boolean) => void
  onCancelEdit?: () => void
  sessionId?: string
  /** Retry the failed turn this error belongs to — resends the preceding user
   *  prompt and its attachments. Only wired on messages that carry an error. */
  onRetry?: (assistantMessageId: string) => void
  /** Disables the Retry button while another run is in flight so a click
   *  cannot fire a second turn on top of a live one. */
  retryDisabled?: boolean
  /** Docked sticky prompt: first line solid, second line faded off. */
  compact?: boolean
}) {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [copied, setCopied] = useState(false)
  const promptRef = useRef<HTMLParagraphElement>(null)
  const [promptLines, setPromptLines] = useState(1)

  useLayoutEffect(() => {
    if (message.role !== 'user' || editing) return
    const el = promptRef.current
    if (!el) return
    const measure = () => {
      const lh = parseFloat(getComputedStyle(el).lineHeight)
      setPromptLines((prev) => nextPromptLineCount(prev, el.scrollHeight, lh))
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [message.role, message.content, editing])

  const copy = async () => {
    if (await copyText(message.content)) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }

  if (message.role === 'user') {
    // Prompt sits in a rounded bubble tinted with a thin primary accent so it
    // still reads on palettes whose secondary is close to the chat canvas.
    return (
      <div className="fade-up space-y-2">
        {message.images?.length ? (
          <div className="flex flex-wrap gap-2">
            {message.images.map((src, i) => (
              <img
                key={i}
                src={src}
                alt=""
                className="max-h-48 rounded-2xl border border-border object-contain"
              />
            ))}
          </div>
        ) : null}
        {message.docs?.length ? (
          <div className="flex flex-wrap gap-2">
            {message.docs.map((d, i) => (
              <div
                key={i}
                className="flex max-w-56 items-center gap-1.5 rounded-full border border-border bg-muted/50 px-2.5 py-1 text-xs"
              >
                <FileText className="size-4 shrink-0 text-muted-foreground" />
                <span className="truncate" title={d.name}>
                  {d.name}
                </span>
              </div>
            ))}
          </div>
        ) : null}
        {message.content ? (
          editing && onSubmitEdit && onCancelEdit ? (
            <div className="w-full">
            <InlineUserEditor
              sessionId={sessionId}
              messageId={message.id}
              initialText={message.content}
              onSubmit={onSubmitEdit}
              onCancel={onCancelEdit}
            />
            </div>
          ) : (
            <UserPromptBubble
              compact={compact}
              lines={promptLines}
              onEdit={onEdit ? () => onEdit(message.id, message.content) : undefined}
              editLabel={t('edit.button')}
            >
              <p
                ref={promptRef}
                className="whitespace-pre-wrap break-words text-[15px] leading-7 text-foreground"
              >
                {message.content}
              </p>
            </UserPromptBubble>
          )
        ) : null}
      </div>
    )
  }

  // Slash-command output is not the model talking. Setting it apart keeps the
  // transcript honest about what came from where.
  if (message.role === 'system') {
    return (
      <div className="fade-up rounded-[var(--radius-lg)] border border-border bg-card px-3.5 py-3">
        <div className="mb-1.5 flex items-center gap-1.5 font-mono text-[11px] lowercase text-dim">
          <Terminal className="size-3" />
          {t('chat.command')}
        </div>
        <div className="text-xs">
          <Markdown content={message.content} />
        </div>
      </div>
    )
  }

  return (
    <div className="group min-w-0 space-y-3 fade-up">
      {message.segments && message.segments.length > 0
        ? message.segments.map((seg, i) => {
            if (seg.kind === 'reasoning') {
              if (!showReasoning) return null
              return <ReasoningBlock key={`r${i}`} text={seg.text} />
            }
            if (seg.kind === 'tool') {
              // todo calls surface in the sticky TaskBar, not inline.
              if (seg.call.name === 'todo') return null
              // ask_user renders as a question with clickable answers instead
              // of a raw tool card.
              if (seg.call.name === 'ask_user') {
                return (
                  <AskUserCard
                    key={seg.call.id}
                    call={seg.call}
                    disabled={!askActive}
                    onAnswer={onAnswer ?? (() => {})}
                  />
                )
              }
              return <ToolCallCard key={seg.call.id} call={seg.call} />
            }
            return <TextSegment key={`t${i}`} text={seg.text} />
          })
        : // Fallback for any message that predates the timeline model.
          <>
            {showReasoning && message.reasoning ? (
              <ReasoningBlock text={message.reasoning} />
            ) : null}
            {message.toolCalls?.map((call) =>
              call.name === 'todo' ? null : call.name === 'ask_user' ? (
                <AskUserCard key={call.id} call={call} disabled={!askActive} onAnswer={onAnswer ?? (() => {})} />
              ) : (
                <ToolCallCard key={call.id} call={call} />
              ),
            )}
            {message.content ? (
              <div className="text-[15px] leading-7">
                <Markdown content={message.content} className="space-y-3" />
              </div>
            ) : null}
          </>}

      {message.error ? (
        <AssistantErrorBlock
          payload={message.error}
          onCopy={() => copyText(message.error ?? '')}
          onRetry={onRetry ? () => onRetry(message.id) : undefined}
          retryDisabled={retryDisabled}
        />
      ) : null}

      {message.content ? (
        <div className="flex items-center gap-2 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
          <Button variant="ghost" size="icon-sm" onClick={copy} aria-label={t('common.copy')}>
            {copied ? (
              <Check className="size-3.5 text-[var(--success)]" />
            ) : (
              <Copy className="size-3.5" />
            )}
          </Button>
          {message.tokensOut ? (
            <span className="font-mono text-[10px] tabular-nums text-dim">
              {t('chat.tokensOut', { n: message.tokensOut })}
            </span>
          ) : null}
          {message.createdAt ? (
            <span className="font-mono text-[10px] text-dim">{timeAgo(message.createdAt)}</span>
          ) : null}
        </div>
      ) : null}
    </div>
  )
})
