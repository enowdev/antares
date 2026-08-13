import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import {
  ArrowUp,
  Brain,
  CaretDown,
  Check,
  Cloud,
  Copy,
  FileText,
  GitBranch,
  Paperclip,
  PencilSimple,
  Plus,
  Prohibit,
  SidebarSimple,
  Stop,
  Terminal,
  Warning,
  X,
} from '@phosphor-icons/react'
import {
  ApiError,
  get,
  isDashboardPasswordRequired,
  post,
  streamGet,
  streamPost,
  type StreamEvent,
} from '@/lib/api'
import {
  approvalFromEvent,
  cursorHydrationFromDetail,
  mergeApprovals,
  pendingApprovalsForSession,
  shouldReconnectAttach,
  stopBehavior,
  type CursorHydration,
  type CursorStateProjection,
  type PendingApproval,
} from '@/lib/chatEvents'
import {
  groupStreamPatches,
  queueStreamDelta,
  shouldRefreshAfterAttach,
  type QueuedStreamPatch,
} from '@/lib/chatStreamQueue'
import { copyText } from '@/lib/clipboard'
import {
  cursorChatRequest,
  isCursorTarget,
  type ChatTarget,
  type ComposerTarget,
  type CursorMode,
  type CursorOptionsValue,
  type CursorRunBaseline,
} from '@/lib/composerTargets'
import {
  baselineAfterSend,
  composerCanSend,
  restoreIsCurrent,
  shouldAdoptDefaultTarget,
  stopStreamKind,
  targetAfterCursorHydration,
  targetChangeAllowed,
  type TargetOwner,
} from '@/lib/composerRestore'
import { composerImageLimit, validateCursorAttachments } from '@/lib/cursorAttachments'
import {
  defaultCursorVariant,
  resolveCursorVariant,
  type CursorModel,
} from '@/lib/cursorModels'
import { useI18n, useTimeAgo, type MessageKey } from '@/lib/i18n'
import type { ReasoningCapability } from '@/lib/models'
import {
  loadReasoningPreference,
  reasoningOptions,
  saveReasoningPreference,
} from '@/lib/reasoning'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/primitives'
import { SkeletonMessage } from '@/components/ui/skeleton'
import { Markdown } from '@/components/chat/Markdown'
import { ToolCallCard } from '@/components/chat/ToolCallCard'
import { TaskBar, parseTasks } from '@/components/chat/TaskBar'
import { ApprovalCard, type ApprovalView } from '@/components/chat/ApprovalCard'
import { AskUserCard } from '@/components/chat/AskUserCard'
import { RolePicker } from '@/components/chat/RolePicker'
import { ModelPicker } from '@/components/chat/ModelPicker'
import { CursorOptions } from '@/components/chat/CursorOptions'
import { ReasoningPicker } from '@/components/chat/ReasoningPicker'
import { ProjectPicker } from '@/components/chat/ProjectPicker'
import { ProjectSidebar } from '@/components/chat/ProjectSidebar'
import { AnalyzeProjectDialog } from '@/components/chat/AnalyzeProjectDialog'
import { EditMessageDialog } from '@/components/chat/EditMessageDialog'
import { SubAgentPanel, type ActiveAgent } from '@/components/chat/SubAgentPanel'
import {
  SlashPalette,
  useCommands,
  useMatches,
  type CommandSpec,
} from '@/components/chat/SlashPalette'

export interface ToolCallView {
  id: string
  name: string
  args: string
  result?: string
  isError?: boolean
  progress?: string
  running?: boolean
}

/** One part of an assistant turn, in the order it happened. */
export type Segment =
  | { kind: 'text'; text: string }
  | { kind: 'reasoning'; text: string }
  | { kind: 'tool'; call: ToolCallView }

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant' | 'tool' | 'system'
  content: string
  reasoning?: string
  toolCalls?: ToolCallView[]
  // segments is the timeline: text, reasoning, and tool calls interleaved as
  // they arrived, so the transcript reads in the order the model worked.
  segments?: Segment[]
  createdAt?: string
  tokensIn?: number
  tokensOut?: number
  error?: string
  images?: string[]
  // Non-image attachments shown as chips under the user's message.
  docs?: { path: string; name: string }[]
}

/**
 * Default soft cap for live reasoning text in React state while a turn streams.
 * Overridden by `display.max_live_reasoning_chars` from config (0 = unlimited).
 * High-effort models can emit hundreds of KB of reasoning per turn; unbounded
 * string growth freezes the dashboard main thread. Full text is still saved
 * server-side and restored on attach `done` / hydrate.
 */
export const DEFAULT_MAX_LIVE_REASONING_CHARS = 48_000

/** Append a text or reasoning delta, extending the last segment when it is the
 *  same kind so a streamed sentence stays one block.
 *  `maxLiveReasoningChars`: trailing-window cap; ≤0 means unlimited. */
export function appendSeg(
  m: ChatMessage,
  kind: 'text' | 'reasoning',
  delta: string,
  maxLiveReasoningChars: number = DEFAULT_MAX_LIVE_REASONING_CHARS,
): ChatMessage {
  const segs = m.segments ? m.segments.slice() : []
  const last = segs[segs.length - 1]
  if (last && last.kind === kind) {
    segs[segs.length - 1] = { kind, text: last.text + delta }
  } else {
    segs.push({ kind, text: delta })
  }
  let content = m.content
  let reasoning = m.reasoning
  if (kind === 'text') {
    content = m.content + delta
  } else {
    const next = (m.reasoning ?? '') + delta
    const cap = maxLiveReasoningChars
    // Keep a trailing window so the bubble stays usable and string growth is O(cap).
    reasoning = cap > 0 && next.length > cap ? next.slice(next.length - cap) : next
    const segLast = segs[segs.length - 1]
    if (cap > 0 && segLast?.kind === 'reasoning' && segLast.text.length > cap) {
      segs[segs.length - 1] = {
        kind: 'reasoning',
        text: segLast.text.slice(segLast.text.length - cap),
      }
    }
  }
  return {
    ...m,
    segments: segs,
    content,
    reasoning,
  }
}

export function pushToolSeg(m: ChatMessage, call: ToolCallView): ChatMessage {
  return {
    ...m,
    segments: [...(m.segments ?? []), { kind: 'tool', call }],
    toolCalls: [...(m.toolCalls ?? []), call],
  }
}

export function updateToolSeg(m: ChatMessage, id: string, fn: (c: ToolCallView) => ToolCallView): ChatMessage {
  return {
    ...m,
    segments: (m.segments ?? []).map((seg) =>
      seg.kind === 'tool' && seg.call.id === id ? { kind: 'tool', call: fn(seg.call) } : seg,
    ),
    toolCalls: (m.toolCalls ?? []).map((c) => (c.id === id ? fn(c) : c)),
  }
}

interface SessionDetail {
  session: {
    id: string
    title: string
    model: string
    provider: string
    meta?: { project_dir?: string } | null
  }
  messages: Array<{
    id: string
    role: ChatMessage['role']
    content: string
    reasoning?: string
    tool_calls?: string
    tool_call_id?: string
    tool_name?: string
    attachments?: string
    created_at: string
    tokens_in: number
    tokens_out: number
    hidden?: boolean
    model?: string
    meta?: Record<string, unknown> | null
  }>
  /**
   * Durable Cursor state. Null for an ordinary chat; absent only on a server
   * that predates the projection, where the transcript is the last resort.
   */
  cursor_state?: CursorStateProjection | null
}

/** Rebuild view models from the persisted message log. */
function hydrate(detail: SessionDetail): ChatMessage[] {
  const out: ChatMessage[] = []
  const pending = new Map<string, ToolCallView>()

  for (const m of detail.messages) {
    // Hidden messages (e.g. an injected sub-agent result) are context for the
    // model, not something to render — the agent's continuation shows instead.
    if (m.hidden) continue
    if (m.role === 'tool') {
      const call = pending.get(m.tool_call_id ?? '')
      if (call) {
        call.result = m.content
        call.running = false
      }
      continue
    }
    const msg: ChatMessage = {
      id: m.id,
      role: m.role,
      content: m.content,
      reasoning: m.reasoning || undefined,
      createdAt: m.created_at,
      tokensIn: m.tokens_in,
      tokensOut: m.tokens_out,
    }
    // Images sent with the message are stored as the same parts the model saw.
    if (m.attachments) {
      try {
        const parts = JSON.parse(m.attachments) as Array<{
          mime_type?: string
          data?: string
          url?: string
        }>
        const srcs = parts
          .map((p) => p.url || (p.data ? `data:${p.mime_type || 'image/png'};base64,${p.data}` : ''))
          .filter(Boolean)
        if (srcs.length > 0) msg.images = srcs
      } catch {
        /* ignore malformed history */
      }
    }
    const segments: Segment[] = []
    if (msg.reasoning) segments.push({ kind: 'reasoning', text: msg.reasoning })
    if (msg.content) segments.push({ kind: 'text', text: msg.content })
    if (m.tool_calls) {
      try {
        const parsed = JSON.parse(m.tool_calls) as Array<{
          id: string
          name: string
          arguments: string
        }>
        msg.toolCalls = parsed.map((c) => {
          const view: ToolCallView = { id: c.id, name: c.name, args: c.arguments }
          pending.set(c.id, view)
          segments.push({ kind: 'tool', call: view })
          return view
        })
      } catch {
        /* ignore malformed history */
      }
    }
    if (msg.role === 'assistant' && segments.length > 0) msg.segments = segments
    out.push(msg)
  }
  return out
}

/** The part of a Cursor turn that is not the model and its variant. */
interface CursorTurnSettings {
  mode: CursorMode
  repositoryUrl: string | null
  startingRef: string | null
  autoCreatePR: boolean
}

const SUGGESTION_KEYS: MessageKey[] = [
  'chat.suggest1',
  'chat.suggest2',
  'chat.suggest3',
  'chat.suggest4',
]

// The "resume where I left off" pointer. Stored in sessionStorage, not
// localStorage, so it is PER-TAB: two tabs can sit on different sessions at
// once, and each still survives a refresh of that tab. In localStorage the
// pointer was shared, so opening a second tab on "/" always resumed the first
// tab's session. Wrapped so a Safari private-mode throw can never break a send.
const LAST_SESSION_KEY = 'antares:last-session'
const lastSession = {
  get(): string | null {
    try {
      return sessionStorage.getItem(LAST_SESSION_KEY)
    } catch {
      return null
    }
  },
  set(id: string) {
    try {
      sessionStorage.setItem(LAST_SESSION_KEY, id)
    } catch {
      /* private mode / storage disabled — resume is best-effort */
    }
  },
  clear() {
    try {
      sessionStorage.removeItem(LAST_SESSION_KEY)
    } catch {
      /* ignore */
    }
  },
}

/** Composer ↑/↓ recall — most recent first, de-duped consecutive, capped. */
const INPUT_HISTORY_KEY = 'antares:composer-history'
const INPUT_HISTORY_MAX = 50

function loadInputHistory(): string[] {
  try {
    const raw = localStorage.getItem(INPUT_HISTORY_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    return parsed.filter((x): x is string => typeof x === 'string' && x.trim() !== '').slice(0, INPUT_HISTORY_MAX)
  } catch {
    return []
  }
}

function pushInputHistory(entry: string, prev: string[]): string[] {
  const text = entry.trim()
  if (!text) return prev
  // Drop consecutive duplicate of the most recent entry.
  const next = prev[0] === text ? prev : [text, ...prev.filter((x) => x !== text)]
  return next.slice(0, INPUT_HISTORY_MAX)
}

/** Caret is on the first visual line of a textarea (for shell-style history ↑). */
function caretOnFirstLine(el: HTMLTextAreaElement): boolean {
  const pos = el.selectionStart ?? 0
  return !el.value.slice(0, pos).includes('\n')
}

/** Caret is on the last visual line (for history ↓). */
function caretOnLastLine(el: HTMLTextAreaElement): boolean {
  const pos = el.selectionStart ?? 0
  return !el.value.slice(pos).includes('\n')
}

export default function ChatPage() {
  const { sessionId } = useParams<{ sessionId: string }>()
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useI18n()

  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [loading, setLoading] = useState(!!sessionId)
  const [streaming, setStreaming] = useState(false)
  // Read by stable callbacks that must not change identity per render.
  const streamingRef = useRef(false)
  streamingRef.current = streaming
  // Live status for the streaming indicator: which step, and what tool (if any)
  // is running right now. Reset at the start of every send.
  const [live, setLive] = useState<{
    turn: number
    tool?: string
    waiting?: boolean
    /** Server notice (compacting, steering, …) shown while streaming. */
    notice?: string
  }>({ turn: 1 })
  const [input, setInput] = useState('')
  // Recent composer prompts (shell-style ↑/↓). Persisted across reloads.
  const [inputHistory, setInputHistory] = useState<string[]>(() => loadInputHistory())
  // -1 = editing a live draft (not browsing history). ≥0 = index into inputHistory
  // from the end (0 = most recent).
  const [historyPos, setHistoryPos] = useState(-1)
  const draftRef = useRef('') // draft saved when first leaving with ↑
  const [error, setError] = useState<string>()
  const [title, setTitle] = useState('')
  const [approvals, setApprovals] = useState<ApprovalView[]>([])
  // ask_user pauses the turn until answered. We keep the waiting ask's id (from
  // the `ask` event) so the card posts the answer to /api/asks/{id} — which
  // resumes the SAME turn — rather than sending a new chat message.
  const [askId, setAskId] = useState<string | undefined>()
  // Data URLs, which is what the API takes and what a preview needs.
  const [images, setImages] = useState<string[]>([])
  // Non-image attachments, uploaded to a temp dir. The agent reads them with
  // the read_document tool via the path we hand it on send.
  const [docs, setDocs] = useState<{ path: string; name: string }[]>([])
  // Role remembers the last one you used across sessions/reloads. An existing
  // session's own stored role still wins when you open it; new chats fall back
  // to this remembered value instead of resetting to the default.
  const [role, setRole] = useState(() => localStorage.getItem('antares:last-role') ?? '')
  const pickRole = useCallback((r: string) => {
    setRole(r)
    if (r) localStorage.setItem('antares:last-role', r)
    else localStorage.removeItem('antares:last-role')
  }, [])
  // Where the next message runs: an Antares chat model, or a Cursor Cloud
  // Agent. Only a chat target has an adaptive reasoning override — Cursor's own
  // variant controls take that role in Cursor mode.
  const [target, setTarget] = useState<ComposerTarget | null>(null)
  const targetRef = useRef<ComposerTarget | null>(null)
  targetRef.current = target
  const cursorMode = isCursorTarget(target)
  // Who may set the target right now. A session's own durable state outranks
  // the picker's automatic active-model default, which is stashed until the
  // session turns out not to own the target. The state mirror drives the
  // composer, which must not accept a message while the answer is unknown.
  const [targetOwner, setTargetOwnerState] = useState<TargetOwner>('free')
  const targetOwnerRef = useRef<TargetOwner>('free')
  const setTargetOwner = useCallback((owner: TargetOwner) => {
    targetOwnerRef.current = owner
    setTargetOwnerState(owner)
  }, [])
  const pendingDefaultRef = useRef<ChatTarget | null>(null)
  // Whether the target was chosen deliberately since this session opened.
  const userChoseRef = useRef(false)
  // Bumped for every session hydration, so an answer for the session that was
  // open a moment ago can never apply to the one open now.
  const hydrationRef = useRef(0)
  // The kind of stream that is actually running. The picker is locked while a
  // turn streams, but an attach can outlive a target change, so Stop asks this
  // rather than the composer.
  const streamKindRef = useRef<'chat' | 'cursor' | null>(null)
  // The non-model half of a Cursor turn, kept while switching Cursor models.
  const [cursorSettings, setCursorSettings] = useState<CursorTurnSettings>({
    mode: 'agent',
    repositoryUrl: null,
    startingRef: null,
    autoCreatePR: false,
  })
  const cursorOptions = useMemo<CursorOptionsValue | null>(
    () =>
      isCursorTarget(target)
        ? { model: target.model, variant: target.variant, ...cursorSettings }
        : null,
    [target, cursorSettings],
  )
  const cursorOptionsRef = useRef<CursorOptionsValue | null>(null)
  cursorOptionsRef.current = cursorOptions
  // The run a follow-up would continue, and whether the server still considers
  // it reusable, so the options popover can warn that sending now starts a new
  // agent instead of following up.
  const [lastCursorRun, setLastCursorRun] = useState<CursorRunBaseline | null>(null)
  const lastCursorRunRef = useRef<CursorRunBaseline | null>(null)
  lastCursorRunRef.current = lastCursorRun
  // What this session's Cursor run is doing or produced: remote status,
  // operation state, branches, and pull requests.
  const [cursorState, setCursorState] = useState<CursorHydration | null>(null)
  const cursorStateRef = useRef<CursorHydration | null>(null)
  cursorStateRef.current = cursorState
  // Local Stop detaches from a Cursor run that keeps going remotely. The ref is
  // what the standing attach loop reads, so it never re-follows immediately.
  const [detached, setDetached] = useState(false)
  const detachedRef = useRef(false)
  const [cancelling, setCancelling] = useState(false)

  // The model, its capability, and its scoped reasoning value move together.
  // Updating the ref synchronously prevents a send immediately after switching
  // models from carrying the previous model's override.
  const [reasoning, setReasoning] = useState('')
  const composerReasoningRef = useRef<{
    selection: ChatTarget
    capability?: ReasoningCapability
    value: string
  } | null>(null)
  /**
   * Set the target and its ref together. Restoration decides in one pass, and
   * every step of that pass has to see the target the previous step chose
   * rather than the one still rendered.
   */
  const commitTarget = useCallback((next: ComposerTarget | null) => {
    targetRef.current = next
    setTarget(next)
  }, [])
  /**
   * Point the composer at a target. `default` is the picker's automatic
   * active-model lookup, which must never outrank a session being restored or
   * a choice already made; `user` is an edit, refused while a turn streams.
   */
  const selectTarget = useCallback(
    (selection: ComposerTarget, origin: 'user' | 'default' | 'restore' = 'user') => {
      if (origin === 'default') {
        // Keep it either way: this session may turn out not to own the target.
        if (selection.kind === 'chat') pendingDefaultRef.current = selection
        if (
          !shouldAdoptDefaultTarget({
            owner: targetOwnerRef.current,
            hasTarget: targetRef.current !== null,
          })
        ) {
          return
        }
      }
      if (origin === 'user') {
        if (!targetChangeAllowed(streamingRef.current)) return
        // A deliberate choice outranks whatever this session would restore.
        userChoseRef.current = true
      }
      commitTarget(selection)
      if (selection.kind !== 'chat') return
      const capability = selection.reasoningCapability
      const { value } = loadReasoningPreference(
        localStorage,
        selection.provider,
        selection.model,
        capability,
      )
      composerReasoningRef.current = { selection, capability, value }
      setReasoning(value)
    },
    [commitTarget],
  )
  const changeCursorOptions = useCallback((next: CursorOptionsValue) => {
    if (!targetChangeAllowed(streamingRef.current)) return
    userChoseRef.current = true
    commitTarget({ kind: 'cursor', model: next.model, variant: next.variant })
    setCursorSettings({
      mode: next.mode,
      repositoryUrl: next.repositoryUrl,
      startingRef: next.startingRef,
      autoCreatePR: next.autoCreatePR,
    })
  }, [commitTarget])
  /**
   * Record the run a follow-up would continue, when the composer is already
   * pointed at exactly that selection. Reports whether it could.
   */
  const applyCursorBaseline = useCallback(
    (
      modelId: string,
      params: Array<{ id: string; value: string }>,
      settings: CursorTurnSettings,
      reuseValid: boolean,
    ): boolean => {
      const current = targetRef.current
      if (
        current?.kind !== 'cursor' ||
        current.model.id !== modelId ||
        resolveCursorVariant(current.model, params) !== current.variant
      ) {
        return false
      }
      setLastCursorRun({
        options: { model: current.model, variant: current.variant, ...settings },
        reuseValid,
      })
      return true
    },
    [],
  )
  /**
   * Point the composer back at the exact model and variant a session's durable
   * state names. The catalogue is the only place that knows a model's variants,
   * and a selection it no longer offers is reported instead of being replaced
   * by the default one — that would silently run a different configuration.
   */
  const restoreCursorTarget = useCallback(
    (
      modelId: string,
      /** Null when only a transcript named the model, so no exact selection exists. */
      params: Array<{ id: string; value: string }> | null,
      settings: CursorTurnSettings,
      reuseValid: boolean,
    ) => {
      // The composer already holds this exact selection; no catalogue lookup
      // can tell us anything new.
      if (params && applyCursorBaseline(modelId, params, settings, reuseValid)) return
      const generation = hydrationRef.current
      const current = targetRef.current
      void get<{ models?: CursorModel[]; needs_key?: boolean }>('/providers/cursor/models')
        .then((d) => {
          // Another session opened, or the user chose something, while this
          // catalogue request was in flight.
          if (!restoreIsCurrent(generation, hydrationRef.current)) return
          if (targetRef.current !== current) return
          if (d.needs_key) {
            setError(t('target.cursorNeedsKey'))
            return
          }
          const model = (d.models ?? []).find(
            (candidate) =>
              candidate.id === modelId || (candidate.aliases ?? []).includes(modelId),
          )
          // With a stored selection only that exact variant may be restored;
          // without one (an older server) the model's own default is the
          // honest starting point, and nothing claims a run to follow up on.
          const variant = model
            ? params
              ? resolveCursorVariant(model, params)
              : defaultCursorVariant(model)
            : null
          if (!model || !variant) {
            setError(t('cursor.staleSelection'))
            return
          }
          commitTarget({ kind: 'cursor', model, variant })
          setLastCursorRun(
            params ? { options: { model, variant, ...settings }, reuseValid } : null,
          )
        })
        .catch(() => {})
    },
    [applyCursorBaseline, commitTarget, t],
  )
  /**
   * Apply a session's durable Cursor state. Opening a session restores the
   * composer from it; a refresh during or after a turn only updates the run's
   * status and reuse baseline, so neither an edit made while the turn ran nor a
   * model the user has just switched to is overwritten.
   */
  const applyCursorHydration = useCallback(
    (hydration: CursorHydration, restoreComposer: boolean) => {
      const worthShowing =
        hydration.active || Boolean(hydration.remoteStatus) || hydration.branches.length > 0
      setCursorState(worthShowing ? hydration : null)
      const settings: CursorTurnSettings = {
        mode: hydration.mode ?? 'agent',
        repositoryUrl: hydration.repositoryUrl ?? null,
        // The exact stored ref, so a follow-up reproduces the same run identity.
        startingRef: hydration.startingRef ?? null,
        autoCreatePR: hydration.autoCreatePR === true,
      }
      const userChose = userChoseRef.current
      if (restoreComposer) {
        // Whatever this session is, the composer must stop pointing at another
        // conversation's Cursor agent — and must end up with somewhere to send
        // — before the next message can go out.
        const decision = targetAfterCursorHydration({
          active: hydration.active,
          modelId: hydration.modelId,
          current: targetRef.current,
          pendingDefault: pendingDefaultRef.current,
          lastChat: composerReasoningRef.current?.selection ?? null,
          userChose,
        })
        setTargetOwner(decision.owner)
        if (decision.action === 'set') {
          if (decision.target) selectTarget(decision.target, 'restore')
          else commitTarget(null)
        }
      }
      if (!hydration.active || !hydration.modelId) {
        // Not a Cursor session, or one whose exact selection is unreadable:
        // there is no run a follow-up could continue.
        if (restoreComposer && hydration.active) setCursorSettings(settings)
        setLastCursorRun(null)
        return
      }
      // An absent selection means a server that predates the projection; an
      // exact one must be matched exactly.
      const params = hydration.params ?? null
      const reuseValid = hydration.reuseValid === true
      // A deliberate choice keeps the composer; only the follow-up baseline is
      // still worth taking from durable state.
      if (!restoreComposer || userChose) {
        if (!params || !applyCursorBaseline(hydration.modelId, params, settings, reuseValid)) {
          setLastCursorRun(null)
        }
        return
      }
      setCursorSettings(settings)
      restoreCursorTarget(hydration.modelId, params, settings, reuseValid)
    },
    [applyCursorBaseline, commitTarget, restoreCursorTarget, selectTarget, setTargetOwner],
  )
  const pickReasoning = useCallback((value: string) => {
    const current = composerReasoningRef.current
    if (!current) return
    const next = reasoningOptions(current.capability).some(
      (option) => option.value === value,
    )
      ? value
      : ''
    saveReasoningPreference(
      localStorage,
      current.selection.provider,
      current.selection.model,
      next,
    )
    composerReasoningRef.current = { ...current, value: next }
    setReasoning(next)
  }, [])
  // Project session: the folder this chat is bound to. Chosen on a NEW chat and
  // sent with the first message; once the session exists it is fixed (locked).
  const [projectDir, setProjectDir] = useState('')
  // A folder just picked but not yet confirmed: drives the "analyze first?"
  // dialog. Choosing binds it as projectDir (and optionally analyzes).
  const [pendingProject, setPendingProject] = useState('')
  // Whether the project sidebar is expanded. Only meaningful for project chats.
  // Desktop: a docked column (starts open). Mobile: a slide-over overlay driven
  // by sidebarMobileOpen (starts closed so the chat has the full width).
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [sidebarMobileOpen, setSidebarMobileOpen] = useState(false)
  // Bumped when a turn finishes, so the sidebar re-reads project_info the agent
  // may have just written.
  const [sidebarRefresh, setSidebarRefresh] = useState(0)
  // Context-window fill: the last turn's prompt tokens over the model's window,
  // shown as a ring in the composer. `used` comes from the latest usage event
  // (live) or the last persisted message (on hydrate); `window` from the usage
  // event or, before any turn, the active model's window fetched on mount.
  const [ctxUsed, setCtxUsed] = useState(0)
  const [ctxWindow, setCtxWindow] = useState(0)
  // The model's context window, known even before the first turn.
  useEffect(() => {
    get<{ context_window?: number }>('/context-window')
      .then((d) => setCtxWindow((w) => w || Number(d.context_window ?? 0)))
      .catch(() => {})
  }, [])
  // display.* prefs from config: whether to show reasoning at all, and the
  // live-stream character cap (trailing window). Defaults match server defaults.
  const [showReasoning, setShowReasoning] = useState(true)
  const showReasoningRef = useRef(true)
  const maxLiveReasoningRef = useRef(DEFAULT_MAX_LIVE_REASONING_CHARS)
  useEffect(() => {
    showReasoningRef.current = showReasoning
  }, [showReasoning])
  useEffect(() => {
    get<{
      values?: {
        display?: {
          show_reasoning?: boolean
          max_live_reasoning_chars?: number
        }
      }
    }>('/config')
      .then((d) => {
        const disp = d.values?.display
        if (disp && typeof disp.show_reasoning === 'boolean') {
          setShowReasoning(disp.show_reasoning)
        }
        const n = Number(disp?.max_live_reasoning_chars)
        if (Number.isFinite(n)) {
          // 0 = unlimited; negative is normalized server-side to default.
          maxLiveReasoningRef.current = n < 0 ? DEFAULT_MAX_LIVE_REASONING_CHARS : n
        }
      })
      .catch(() => {})
  }, [])
  // When set, an overlay shows this sub-agent's live transcript instead of the
  // main one; clearing it returns to the main agent.
  const [viewingAgent, setViewingAgent] = useState<ActiveAgent | null>(null)
  // The user message being edited (id + current text), driving EditMessageDialog.
  const [editing, setEditing] = useState<{ id: string; content: string } | null>(null)

  const commands = useCommands()
  const matches = useMatches(input, commands)
  const [paletteSel, setPaletteSel] = useState(0)

  // The current checklist is the most recent todo write anywhere in the
  // transcript; it drives the sticky task bar above the composer.
  const tasks = useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const calls = messages[i].toolCalls
      if (!calls) continue
      for (let j = calls.length - 1; j >= 0; j--) {
        if (calls[j].name === 'todo') {
          const items = parseTasks(calls[j].args)
          if (items.length > 0) return items
        }
      }
    }
    return []
  }, [messages])

  // Per-tool usage this session (count + last-used), from the transcript's tool
  // calls — drives the sidebar's Tools tab. Sorted most-recent first.
  const toolStats = useMemo(() => {
    const by = new Map<string, { name: string; count: number; last?: string }>()
    for (const m of messages) {
      if (!m.toolCalls) continue
      for (const c of m.toolCalls) {
        const s = by.get(c.name) ?? { name: c.name, count: 0 }
        s.count++
        if (m.createdAt) s.last = m.createdAt
        by.set(c.name, s)
      }
    }
    return [...by.values()].sort((a, b) => (b.last ?? '').localeCompare(a.last ?? '') || b.count - a.count)
  }, [messages])

  // Files the agent wrote/edited this session, newest first and de-duplicated —
  // drives the sidebar's Changes tab. Parsed from write_file/edit_file calls.
  const changedFiles = useMemo(() => {
    const seen = new Set<string>()
    const out: { path: string; tool: string }[] = []
    for (let i = messages.length - 1; i >= 0; i--) {
      const calls = messages[i].toolCalls
      if (!calls) continue
      for (let j = calls.length - 1; j >= 0; j--) {
        const c = calls[j]
        if (c.name !== 'write_file' && c.name !== 'edit_file') continue
        try {
          const path = String(JSON.parse(c.args)?.path ?? '').trim()
          if (path && !seen.has(path)) {
            seen.add(path)
            out.push({ path, tool: c.name })
          }
        } catch {
          /* ignore unparseable args */
        }
      }
    }
    return out
  }, [messages])

  const abortRef = useRef<(() => void) | null>(null)
  // The bound project dir tracked in a ref, so a turn fired immediately after
  // binding (the auto-analyze on "Yes") posts with the project even before the
  // projectDir state re-render lands.
  const projectDirRef = useRef('')
  useEffect(() => {
    projectDirRef.current = projectDir
  }, [projectDir])
  // Whether the just-bound project opted into RAG indexing; carried on the first
  // turn alongside project_dir.
  const indexRagRef = useRef(false)
  // Holds the id of a session that was just created mid-stream on this page.
  // Its messages are already live on screen, so the hydrate effect must not
  // re-fetch and overwrite them before the turn is persisted.
  const localSessionRef = useRef<string | null>(null)
  // The current session id, tracked in a ref so a message always posts to the
  // right session even before the URL param has caught up. Without this, the
  // first reply creates a session but the next message — sent before the param
  // re-render lands — posts with an empty id and starts a second session.
  const sessionIdRef = useRef<string | undefined>(sessionId)
  useEffect(() => {
    sessionIdRef.current = sessionId
  }, [sessionId])
  const virtuosoRef = useRef<VirtuosoHandle>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  // Where the list opens. Captured once per mount: Virtuoso treats this as the
  // initial anchor, so recomputing it from messages.length on every render
  // re-anchors the list mid-stream and fights followOutput.
  const initialIndexRef = useRef(0)

  // A streaming turn emits hundreds of tiny events. Queue them for one render,
  // grouping by message first so flushing is O(messages + patches), not
  // O(messages * patches). Consecutive text/reasoning deltas are coalesced too:
  // replaying a long live-run must not create thousands of closures and perform
  // thousands of ever-growing string concatenations on the browser main thread.
  const patchQueue = useRef<QueuedStreamPatch<ChatMessage>[]>([])
  const flushHandle = useRef<number | null>(null)
  const flushPatches = useCallback(() => {
    flushHandle.current = null
    const queued = patchQueue.current
    if (queued.length === 0) return
    patchQueue.current = []
    const byMessage = groupStreamPatches(queued)
    // Only clone/replace messages that actually received patches. Mapping the
    // entire transcript every frame re-renders hundreds of bubbles on long
    // sessions and was a major source of dashboard freezes during long turns.
    setMessages((prev) => {
      if (byMessage.size === 0) return prev
      let next = prev
      let cloned = false
      for (const [id, patches] of byMessage) {
        const idx = next.findIndex((m) => m.id === id)
        if (idx < 0) continue
        let message = next[idx]
        for (const patch of patches) {
          message =
            patch.kind === 'delta'
              ? appendSeg(message, patch.segment, patch.delta, maxLiveReasoningRef.current)
              : patch.fn(message)
        }
        if (!cloned) {
          next = prev.slice()
          cloned = true
        }
        next[idx] = message
      }
      return next
    })
  }, [])
  const enqueuePatch = useCallback(
    (id: string, fn: (m: ChatMessage) => ChatMessage) => {
      patchQueue.current.push({ id, kind: 'apply', fn })
      if (flushHandle.current == null) {
        flushHandle.current = requestAnimationFrame(flushPatches)
      }
    },
    [flushPatches],
  )
  const enqueueDelta = useCallback(
    (id: string, segment: 'text' | 'reasoning', delta: string) => {
      if (!delta) return
      queueStreamDelta(patchQueue.current, id, segment, delta)
      if (flushHandle.current == null) {
        flushHandle.current = requestAnimationFrame(flushPatches)
      }
    },
    [flushPatches],
  )
  // Flush any tail synchronously and cancel a pending frame — used at end of
  // turn and on unmount so the last delta never lingers unrendered.
  const drainPatches = useCallback(() => {
    if (flushHandle.current != null) {
      cancelAnimationFrame(flushHandle.current)
      flushHandle.current = null
    }
    flushPatches()
  }, [flushPatches])

  // Landing on a bare "/" resumes the last conversation, so switching back to
  // Chat continues where you were rather than starting over. The New button
  // arrives with state.fresh set, which skips the resume and forgets it.
  useEffect(() => {
    if (sessionId) {
      lastSession.set(sessionId)
      return
    }
    if (location.state?.fresh) {
      lastSession.clear()
      return
    }
    const last = lastSession.get()
    if (last) navigate(`/c/${last}`, { replace: true })
    // Only when the route id changes, not on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId])

  useEffect(() => setPaletteSel(0), [input])

  // Leaving the page closes our stream connection, but the turn runs detached on
  // the server, so it keeps going and we reattach to it on return.
  useEffect(
    () => () => {
      abortRef.current?.()
      if (flushHandle.current != null) cancelAnimationFrame(flushHandle.current)
    },
    [],
  )

  // Grow the composer with its content, up to ~8 rows.
  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`
  }, [input])

  const stop = useCallback(() => {
    // A Cursor run lives in Cursor's cloud: Stop may only close this browser's
    // stream. Interrupting the turn, or cancelling it remotely, is never
    // implied by leaving — cancellation is a separate, approved action.
    // Which semantics apply is decided by the stream that is running, not by
    // the composer, which an attached run can outlive.
    const behavior = stopBehavior(
      stopStreamKind(streamKindRef.current, cursorStateRef.current?.active === true),
    )
    abortRef.current?.()
    abortRef.current = null
    setStreaming(false)
    if (behavior.detach) {
      detachedRef.current = true
      setDetached(true)
    }
    if (behavior.interrupt && sessionId) {
      void post<{ interrupted: boolean }>('/chat/interrupt', { session_id: sessionId }).catch(() => {
        // The stream is already closed locally. A failed interrupt will surface
        // when the user reattaches instead of leaving the stop button stuck.
      })
    }
  }, [sessionId])

  /**
   * Follow this session's Cursor run again after an intentional detach. The
   * attach stream replays the run from its first event, so the half-finished
   * bubble this browser was writing is dropped first — otherwise the replay
   * would render the same answer a second time until the turn ends.
   */
  const reattach = useCallback(() => {
    setMessages((prev) => {
      const last = prev[prev.length - 1]
      const optimistic =
        last?.role === 'assistant' && last.id.startsWith('local_') && last.id.endsWith('_a')
      return optimistic ? prev.slice(0, -1) : prev
    })
    detachedRef.current = false
    setDetached(false)
  }, [])

  const refreshApprovals = useCallback((sessionOverride?: string) => {
    const sid = sessionOverride ?? sessionIdRef.current
    if (!sid) return Promise.resolve()
    return get<{ approvals?: PendingApproval[] }>('/approvals')
      .then((d) => {
        setApprovals((prev) => pendingApprovalsForSession(prev, d.approvals ?? [], sid))
      })
      .catch(() => {})
  }, [])

  /**
   * Ask Cursor to cancel the remote run. The server holds the request until the
   * approval card is answered, so the pending list is polled while it waits —
   * the card must be reachable even when this browser has detached.
   */
  const cancelCursorRun = useCallback(async () => {
    const sid = sessionIdRef.current
    if (!sid || cancelling) return
    setCancelling(true)
    setError(undefined)
    const poll = window.setInterval(() => void refreshApprovals(), 2000)
    try {
      await post('/chat/cursor/cancel', { session_id: sid })
    } catch (e) {
      setError(
        isDashboardPasswordRequired(e)
          ? t('sensitive.needPasswordDesc')
          : e instanceof Error
            ? e.message
            : String(e),
      )
    } finally {
      window.clearInterval(poll)
      setCancelling(false)
      void refreshApprovals()
    }
  }, [cancelling, refreshApprovals, t])

  // Apply one stream event to the named assistant message. Shared by a fresh
  // send and a reattach, so both render a turn identically. Session handling
  // differs between the two (navigate vs. title-only), so it is delegated.
  const applyEvent = useCallback(
    (
      assistantId: string,
      event: StreamEvent,
      onSession?: (id: string, title?: string) => void,
    ) => {
      const patchAssistant = (fn: (m: ChatMessage) => ChatMessage) =>
        enqueuePatch(assistantId, fn)
      switch (event.type) {
        case 'session':
          onSession?.(
            typeof event.id === 'string' ? event.id : '',
            typeof event.title === 'string' ? event.title : undefined,
          )
          break
        case 'text':
          enqueueDelta(assistantId, 'text', String(event.delta ?? ''))
          break
        case 'reasoning':
          // Honour display.show_reasoning even if a stale event arrives (server
          // also suppresses when false; this keeps the UI consistent).
          if (showReasoningRef.current) {
            enqueueDelta(assistantId, 'reasoning', String(event.delta ?? ''))
          }
          break
        case 'tool_call':
          setLive((s) => ({
            turn: s.turn + 1,
            tool: String(event.name ?? ''),
            notice: undefined,
          }))
          patchAssistant((m) =>
            pushToolSeg(m, {
              id: String(event.id ?? ''),
              name: String(event.name ?? ''),
              args: String(event.arguments ?? ''),
              running: true,
            }),
          )
          break
        case 'tool_progress':
          patchAssistant((m) =>
            updateToolSeg(m, String(event.id ?? ''), (c) => ({
              ...c,
              // A chunk streams (append); a message is a status line (replace),
              // so "attempt 1/3" → "attempt 2/3" swaps in place instead of
              // concatenating.
              progress:
                event.chunk != null
                  ? (c.progress ?? '') + String(event.chunk)
                  : String(event.message ?? c.progress ?? ''),
            })),
          )
          break
        case 'tool_result':
          setLive((s) => ({ ...s, tool: undefined }))
          patchAssistant((m) =>
            updateToolSeg(m, String(event.id ?? ''), (c) => ({
              ...c,
              result: String(event.content ?? ''),
              isError: !!event.is_error,
              running: false,
            })),
          )
          break
        case 'notice':
          // Compaction, steering, retries, … — without this the UI only shows
          // "Working… · Ns" during multi-minute silent server work.
          setLive((s) => ({
            ...s,
            notice: String(event.message ?? event.content ?? '').trim() || undefined,
          }))
          break
        case 'ask':
          // The turn is now paused inside ask_user. Remember the id so the
          // answer card can resume it; the stream stays open (no 'done').
          setAskId(String(event.id ?? ''))
          setLive((s) => ({ ...s, tool: undefined, waiting: true, notice: undefined }))
          break
        case 'approval': {
          // The run is blocked on a decision. Replay and a reconnect both
          // deliver the same id, so the card is added exactly once and keeps
          // any decision already shown.
          const view = approvalFromEvent(event)
          if (view) setApprovals((prev) => mergeApprovals(prev, view))
          break
        }
        case 'usage':
          patchAssistant((m) => ({
            ...m,
            tokensIn: Number(event.input_tokens ?? m.tokensIn ?? 0),
            tokensOut: Number(event.output_tokens ?? m.tokensOut ?? 0),
          }))
          {
            // context_tokens is the latest turn's input alone — what actually
            // occupies the window right now — so context/window is the fill
            // gauge. (input_tokens above is the run's cumulative total, which
            // climbs past the window on long runs and must NOT feed the gauge.)
            const win = Number(event.context_window ?? 0)
            const used = Number(event.context_tokens ?? 0)
            if (used > 0) setCtxUsed(used)
            if (win > 0) setCtxWindow(win)
          }
          break
        case 'reset':
          // The turn is being retried after a provider glitch — throw away the
          // partial reply so the retry does not render on top of it.
          patchAssistant((m) => ({
            ...m,
            content: '',
            reasoning: undefined,
            toolCalls: undefined,
            segments: [],
            error: undefined,
          }))
          break
        case 'error':
          patchAssistant((m) => ({
            ...m,
            error: String(event.error ?? t('chat.somethingWrong')),
          }))
          break
      }
    },
    [t, enqueuePatch, enqueueDelta],
  )

  // Reconnect to a turn still running for this session (after navigating away
  // and back). Replays from the given cursor, so no tokens are missed, and
  // builds the assistant message lazily — if nothing is live, the server says
  // done at once and no empty bubble appears. Returns a closer.
  //
  // The done handler ALWAYS re-hydrates the persisted session, regardless of
  // whether any event arrived. That covers the race where the turn finished
  // between the user navigating away and back: the initial hydrate in the
  // outer useEffect ran against a stale DB snapshot (the assistant message
  // is persisted at end-of-turn), and the attach's `done` is the earliest
  // moment we know the canonical state is available. Skipping the re-fetch
  // when no event came (the previous behaviour) left the chat showing the
  // pre-turn state — the symptom that looked like "session disappeared".
  const attachLive = useCallback(
    (sid: string) => {
      // A standing attachment: after a turn ends we reconnect, so a turn the
      // SERVER starts later — a background sub-agent finishing and waking the
      // main agent — streams in live without a refresh. `alive` gates the loop
      // so the cleanup truly stops it.
      let alive = true
      let close: (() => void) | undefined
      let assistantId: string | null = null
      let cursor = 0
      // The initial hydrate can race a turn finishing just before attach. Refresh
      // once after the first idle `done` to close that race, then stop downloading
      // and rebuilding the entire transcript on every 1.5-second idle poll.
      let idleRefreshDone = false

      const connect = () => {
        if (!alive) return
        // Never run the standing attach while a foreground send is streaming:
        // that turn already renders via streamPost, and a second follower would
        // double-render it. An intentional Cursor detach holds the loop open
        // but idle in the same way, so Stop does not instantly re-follow the
        // run it just left. Retry shortly instead.
        if (
          !shouldReconnectAttach({
            alive,
            detached: abortRef.current !== null || detachedRef.current,
          })
        ) {
          window.setTimeout(connect, 1500)
          return
        }
        const ensure = () => {
          if (assistantId) return assistantId
          assistantId = `live_${Date.now()}_a`
          setMessages((prev) => [
            ...prev,
            { id: assistantId as string, role: 'assistant', content: '' },
          ])
          setStreaming(true)
          setLive({ turn: 1 })
          return assistantId
        }
        close = streamGet(
          `/chat/attach?session_id=${encodeURIComponent(sid)}&cursor=${cursor}`,
          (event) => {
            const eventCursor = Number(event.cursor ?? cursor)
            if (Number.isFinite(eventCursor) && eventCursor >= cursor) cursor = eventCursor
            if (event.type === 'done') {
              drainPatches()
              setStreaming(false)
              close?.() // stop EventSource from auto-reconnecting
              const shouldRefresh = shouldRefreshAfterAttach(assistantId !== null, idleRefreshDone)
              assistantId = null
              idleRefreshDone = true
              // A later server-initiated turn is a new liveRun with its own
              // zero-based cursor. Reset only after the current run is done.
              cursor = 0
              const refresh = shouldRefresh
                ? get<SessionDetail>(`/sessions/${sid}`)
                    .then((d) => {
                      if (!alive) return
                      setMessages(hydrate(d))
                      setTitle(d.session.title || t('chat.conversation'))
                      applyCursorHydration(cursorHydrationFromDetail(d), false)
                    })
                    .catch(() => {})
                : Promise.resolve()
              // Do not overlap canonical hydration with the next attachment: a
              // stale response could otherwise overwrite fresh live deltas.
              void refresh.finally(() => {
                if (alive) window.setTimeout(connect, 1500)
              })
              return
            }
            applyEvent(ensure(), event, (_id, evtTitle) => {
              if (evtTitle) setTitle(evtTitle)
            })
          },
          (err) => {
            setStreaming(false)
            close?.()
            // Auth failure will not fix itself with a retry — stop the 3s 401
            // loop that filled the daemon log after every restart.
            if (err instanceof ApiError && err.status === 401) {
              setError(t('chat.attachAuthFailed') || 'Dashboard login expired — refresh and sign in again.')
              return
            }
            if (alive) window.setTimeout(connect, 3000)
          },
        )
      }

      connect()
      return () => {
        alive = false
        close?.()
      }
    },
    [applyEvent, applyCursorHydration, drainPatches, t],
  )

  useEffect(() => {
    // A brand-new chat navigates to its own url mid-stream. The live messages
    // are already on screen; re-fetching now would find the turn not yet
    // persisted and wipe them, and adopting an id is not a session switch — the
    // running turn keeps its target, approvals, and Cursor state.
    if (sessionId && sessionId === localSessionRef.current) {
      setLoading(false)
      return
    }
    // Every hydration gets its own token, so a slower answer for the session
    // that was open a moment ago can never apply to the one open now.
    const generation = ++hydrationRef.current
    // Approvals, Cursor recovery state, and the detach flag all belong to one
    // conversation; carrying them into another session would show a decision
    // that no longer blocks anything.
    setApprovals([])
    setCursorState(null)
    setLastCursorRun(null)
    detachedRef.current = false
    setDetached(false)
    // A Cursor target belongs to the conversation that chose it. Until this
    // session's own state says otherwise, the composer holds no Cursor target,
    // so a message sent meanwhile cannot reach another session's agent.
    if (isCursorTarget(targetRef.current)) commitTarget(null)
    userChoseRef.current = false
    // Until this session says where its messages go, the composer accepts none.
    setTargetOwner(sessionId ? 'pending' : 'free')
    if (!sessionId) {
      // A new chat is owned by nobody, so the picker's default may fill it.
      const stashed = pendingDefaultRef.current
      if (stashed && !targetRef.current) selectTarget(stashed, 'default')
      setMessages([])
      setTitle('')
      setProjectDir('')
      setCtxUsed(0)
      setLoading(false)
      return
    }
    setLoading(true)
    let cancelled = false
    let closeAttach: (() => void) | undefined
    get<SessionDetail>(`/sessions/${sessionId}`)
      .then((d) => {
        if (cancelled || !restoreIsCurrent(generation, hydrationRef.current)) return
        const restored = hydrate(d)
        // Open a restored transcript at its newest message. Set before the list
        // mounts (it is still `loading`), so Virtuoso reads the final value once.
        initialIndexRef.current = Math.max(0, restored.length - 1)
        setMessages(restored)
        setTitle(d.session.title || t('chat.conversation'))
        setProjectDir(d.session.meta?.project_dir ?? '')
        // Restore the context gauge from persisted usage: the last turn's input
        // tokens ≈ what was in context, so the ring is right on reload — no need
        // to send a message first.
        for (let i = d.messages.length - 1; i >= 0; i--) {
          const ti = Number(d.messages[i].tokens_in ?? 0)
          if (ti > 0) {
            setCtxUsed(ti)
            break
          }
        }
        setError(undefined)
        // Durable Cursor state decides whether this conversation still runs on
        // Cursor, and with exactly which model, variant, repository, and mode.
        applyCursorHydration(cursorHydrationFromDetail(d), true)
        // A decision published before this page attached is still blocking the
        // run; the pending list is the only place left to find it.
        void refreshApprovals(sessionId)
        // Once the persisted history is on screen, reconnect to any turn still
        // in flight for this session so streaming continues where it left off.
        closeAttach = attachLive(sessionId)
      })
      .catch((e: unknown) => {
        if (cancelled) return
        // The session does not exist (e.g. a stale "last conversation" pointer
        // to a session that was deleted). Forget it and drop to a fresh chat
        // instead of getting stuck on a blank, dead url.
        if (e instanceof ApiError && e.status === 404) {
          if (lastSession.get() === sessionId) {
            lastSession.clear()
          }
          setMessages([])
          setTitle('')
          setError(undefined)
          navigate('/', { replace: true, state: { fresh: true } })
          return
        }
        setError(e instanceof Error ? e.message : String(e))
        // Nothing will claim the target now, so the composer must not stay
        // waiting on a session state that never arrived.
        if (restoreIsCurrent(generation, hydrationRef.current)) {
          setTargetOwner('free')
          const stashed = pendingDefaultRef.current
          if (stashed && !targetRef.current) selectTarget(stashed, 'default')
        }
      })
    get<{ role?: string }>(`/sessions/${sessionId}/role`)
      .then((r) => {
        // The session's own role wins; if it has none, keep the remembered
        // last-used role rather than snapping back to the default.
        if (r.role) pickRole(r.role)
      })
      .catch(() => {})
      .finally(() => setLoading(false))
    // t is stable per language; refetching on language change is harmless.
    return () => {
      cancelled = true
      closeAttach?.()
    }
  }, [
    sessionId,
    t,
    attachLive,
    refreshApprovals,
    applyCursorHydration,
    commitTarget,
    selectTarget,
    setTargetOwner,
  ])

  /** Append a locally-produced message without touching the server. */
  const pushSystem = useCallback((content: string) => {
    setMessages((prev) => [
      ...prev,
      { id: `cmd_${Date.now()}_${prev.length}`, role: 'system', content },
    ])
  }, [])

  /**
   * Slash commands are answered by the server rather than the model, so /status
   * costs nothing and returns instantly. A few of them only the browser can
   * carry out; those come back as an action.
   */
  const runCommand = useCallback(
    async (line: string) => {
      setInput('')
      setError(undefined)
      setMessages((prev) => [...prev, { id: `you_${Date.now()}`, role: 'user', content: line }])
      try {
        const r = await post<{
          ok: boolean
          error?: string
          output?: string
          action?: { kind?: string; value?: string }
        }>('/commands/run', {
          input: line,
          session_id: sessionIdRef.current ?? '',
          surface: 'web',
        })

        if (!r.ok) {
          pushSystem(r.error ?? t('chat.somethingWrong'))
          return
        }

        switch (r.action?.kind) {
          case 'new':
          case 'clear':
            stop()
            lastSession.clear()
            setMessages([])
            setTitle('')
            setApprovals([])
            navigate('/', { state: { fresh: true } })
            return
          case 'resume':
            if (r.action.value) navigate(`/c/${r.action.value}`)
            return
          case 'setup':
            navigate('/config')
            return
          case 'stop':
            stop()
            break
          case 'copy': {
            const last = [...messages].reverse().find((m) => m.role === 'assistant')
            if (last) await copyText(last.content)
            pushSystem(last ? t('chat.copied') : t('chat.nothingToCopy'))
            return
          }
          case 'retry': {
            const last = [...messages].reverse().find((m) => m.role === 'user')
            if (last) setInput(last.content)
            return
          }
        }
        if (r.output) pushSystem(r.output)
      } catch (e) {
        setError((e as Error).message)
      }
    },
    [sessionId, messages, navigate, pushSystem, stop, t],
  )

  // sendText posts a message directly, bypassing the composer input. Used both
  // by the composer (send) and by inline answer buttons (e.g. ask_user options).
  const sendText = useCallback(
    (raw: string, attached: string[] = [], attachedDocs: { path: string; name: string }[] = []) => {
      const text = raw.trim()
      if (!text && attached.length === 0 && attachedDocs.length === 0) return
      // Nothing may be sent before this session's execution target is known:
      // a Cursor conversation must not fall through to the chat model while
      // its exact selection is still loading.
      if (!composerCanSend({ owner: targetOwnerRef.current, streaming })) return
      if (text.startsWith('/') && text.length > 1) {
        // Still record slash commands so ↑ recalls them.
        if (text) {
          setInputHistory((prev) => {
            const next = pushInputHistory(text, prev)
            localStorage.setItem(INPUT_HISTORY_KEY, JSON.stringify(next))
            return next
          })
        }
        setHistoryPos(-1)
        draftRef.current = ''
        void runCommand(text)
        setInput('')
        return
      }

    // Cursor runs in its own cloud VM. Everything it cannot accept is rejected
    // here, before the draft and its attachments are cleared, so nothing is
    // silently dropped and no paid operation is ever offered for a turn that
    // could not have been sent.
    const cursor = cursorOptionsRef.current
    if (cursor) {
      const issue = validateCursorAttachments({ images: attached, docs: attachedDocs })
      if (issue) {
        setError(t(`cursorAttach.${issue.code}`, issue.values))
        return
      }
    }

    // Non-image attachments live in a temp dir; the model can't see them until
    // it reads them. Tell it they're there and how — read_document by path.
    let message = text
    if (attachedDocs.length > 0) {
      const list = attachedDocs.map((d) => `- ${d.name} (path: ${d.path})`).join('\n')
      const note = `Attached file(s) — read each with the read_document tool before answering:\n${list}`
      message = text ? `${text}\n\n${note}` : note
    }

    const userMsg: ChatMessage = {
      id: `local_${Date.now()}`,
      role: 'user',
      content: text,
      docs: attachedDocs.length > 0 ? attachedDocs : undefined,
    }
    const assistantId = `local_${Date.now()}_a`
    setMessages((prev) => [...prev, userMsg, { id: assistantId, role: 'assistant', content: '' }])
    // Remember what was sent for ↑/↓ (composer history).
    if (text) {
      setInputHistory((prev) => {
        const next = pushInputHistory(text, prev)
        localStorage.setItem(INPUT_HISTORY_KEY, JSON.stringify(next))
        return next
      })
    }
    setHistoryPos(-1)
    draftRef.current = ''
    setInput('')
    setImages([])
    setDocs([])
    setError(undefined)
    setStreaming(true)
    setLive({ turn: 1 })

    const composerReasoning = composerReasoningRef.current
    // Sending is a deliberate re-attachment: whatever was detached before, this
    // session is being followed again.
    detachedRef.current = false
    setDetached(false)
    // Stop must know what it is stopping even if the composer moves on.
    streamKindRef.current = cursor ? 'cursor' : 'chat'
    // A refused request starts nothing, so the run a follow-up would continue
    // only changes once the server has accepted this one.
    const baselineBeforeSend = lastCursorRunRef.current
    const generation = hydrationRef.current
    let accepted = false
    abortRef.current = streamPost(
      cursor ? '/chat/cursor' : '/chat',
      cursor
        ? cursorChatRequest(cursor, {
            sessionId: sessionIdRef.current ?? '',
            message,
            images: attached,
            // Only meaningful when starting a new session; the server binds the
            // project once and discovers its repository from there.
            projectDir: sessionIdRef.current ? undefined : projectDirRef.current,
          })
        : {
            session_id: sessionIdRef.current ?? '',
            message,
            images: attached,
            role,
            // Per-chat model override; omitted when unset so the server falls
            // back to the configured default.
            ...(composerReasoning
              ? {
                  model: `${composerReasoning.selection.provider}/${composerReasoning.selection.model}`,
                }
              : {}),
            // Per-turn reasoning override; omitted when unset so the server falls
            // back to the configured default.
            ...(composerReasoning?.value
              ? { reasoning_effort: composerReasoning.value }
              : {}),
            // Only meaningful when starting a new session; the server ignores it once
            // the session exists. Read from the ref so an auto-analyze turn fired
            // right after binding still carries the project.
            ...(projectDirRef.current && !sessionIdRef.current
              ? { project_dir: projectDirRef.current, index_rag: indexRagRef.current }
              : {}),
          },
      (event: StreamEvent) => {
        // Events only flow after the server accepted the request, which is the
        // first moment this turn is the one a follow-up would continue.
        if (!accepted) {
          accepted = true
          if (cursor) {
            setLastCursorRun(
              baselineAfterSend({
                previous: baselineBeforeSend,
                attempted: cursor,
                accepted: true,
              }),
            )
          }
        }
        // End-of-turn: stop streaming immediately rather than waiting for the
        // socket to close. A detached run keeps the connection open past the
        // final event, which otherwise left the indicator and the task bar
        // "running" forever.
        if (event.type === 'done') {
          drainPatches() // render the final buffered deltas before we stop
          setStreaming(false)
          setAskId(undefined)
          setLive((s) => ({ ...s, waiting: false }))
          abortRef.current?.()
          abortRef.current = null
          streamKindRef.current = null
          localSessionRef.current = null
          // The turn may have written project_info — refresh the sidebar.
          setSidebarRefresh((n) => n + 1)
          // Re-hydrate from the persisted turn so the optimistic you_/local_
          // message ids become their real DB ids. Without this the edit/revert
          // affordance never appears until a manual reload, since it is hidden
          // for optimistic ids (see MessageBubble). Mirrors the attach path.
          const sid = sessionIdRef.current
          if (sid) {
            get<SessionDetail>(`/sessions/${sid}`)
              .then((d) => {
                if (!restoreIsCurrent(generation, hydrationRef.current)) return
                setMessages(hydrate(d))
                setTitle(d.session.title || t('chat.conversation'))
                // The turn that just ended decides whether a follow-up can
                // reuse its agent; the composer's own edits are left alone.
                applyCursorHydration(cursorHydrationFromDetail(d), false)
              })
              .catch(() => {})
          }
          return
        }
        applyEvent(assistantId, event, (id, evtTitle) => {
          if (id) {
            // Adopt the real session id at once, so the next message posts to it
            // rather than opening another session.
            sessionIdRef.current = id
            if (id !== sessionId) {
              // The server assigned this id — either a brand-new chat, or the
              // one in the url was stale/missing so a fresh session was created.
              // Point the url at the real session (and remember it so the hydrate
              // the navigation triggers does not overwrite the live messages).
              localSessionRef.current = id
              lastSession.set(id)
              navigate(`/c/${id}`, { replace: true })
            }
          }
          if (evtTitle) setTitle(evtTitle)
        })
      },
      (err) => {
        drainPatches()
        // A refused turn carries the server's own explanation (busy session,
        // rate limit, stale Cursor selection); only the password gate answers
        // with a marker instead of a sentence.
        setError(
          isDashboardPasswordRequired(err) ? t('sensitive.needPasswordDesc') : err.message,
        )
        setStreaming(false)
        abortRef.current = null
        streamKindRef.current = null
        // The turn is persisted now, so a later revisit should hydrate fresh.
        localSessionRef.current = null
        if (!cursor) return
        // Nothing was started, so the previous follow-up baseline still stands.
        // Anything that did happen is in the durable state, which decides.
        setLastCursorRun(
          baselineAfterSend({ previous: baselineBeforeSend, attempted: cursor, accepted: false }),
        )
        const sid = sessionIdRef.current
        if (sid) {
          get<SessionDetail>(`/sessions/${sid}`)
            .then((d) => {
              if (!restoreIsCurrent(generation, hydrationRef.current)) return
              applyCursorHydration(cursorHydrationFromDetail(d), false)
            })
            .catch(() => {})
        }
      },
      () => {
        drainPatches()
        setStreaming(false)
        abortRef.current = null
        streamKindRef.current = null
        localSessionRef.current = null
      },
    )
    },
    [
      role,
      projectDir,
      streaming,
      sessionId,
      navigate,
      runCommand,
      applyEvent,
      applyCursorHydration,
      drainPatches,
      t,
    ],
  )

  // Both composer routes — the send button and Enter — go through here, so the
  // hydration gate covers each of them.
  const canSend = composerCanSend({ owner: targetOwner, streaming })
  const send = useCallback(() => {
    const text = input.trim()
    if (!text && images.length === 0 && docs.length === 0) return
    if (!composerCanSend({ owner: targetOwnerRef.current, streaming })) return
    sendText(text, images, docs)
  }, [input, images, docs, streaming, sendText])

  // Resolve the "analyze first?" dialog: bind the pending folder as the project,
  // then either fire an automatic analysis turn (Yes) or just open the chat (No).
  const resolveAnalyze = useCallback(
    (analyze: boolean, indexRag: boolean) => {
      const dir = pendingProject
      setPendingProject('')
      if (!dir) return
      setProjectDir(dir)
      projectDirRef.current = dir // so the turn below carries it immediately
      indexRagRef.current = indexRag
      setSidebarOpen(true)
      if (analyze) sendText(t('project.analyzePrompt'))
    },
    [pendingProject, sendText, t],
  )

  // applyEdit commits a message edit: drop the edited message and everything
  // after it (optionally reverting file changes since), then re-send the new
  // text so the conversation continues from that point.
  const applyEdit = useCallback(
    async (text: string, revert: boolean) => {
      const target = editing
      setEditing(null)
      if (!target || !sessionIdRef.current) return
      try {
        await post(`/sessions/${sessionIdRef.current}/edit`, {
          message_id: target.id,
          revert,
        })
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
        return
      }
      // Trim the on-screen transcript to before the edited message, then re-send.
      setMessages((prev) => {
        const idx = prev.findIndex((m) => m.id === target.id)
        return idx >= 0 ? prev.slice(0, idx) : prev
      })
      setSidebarRefresh((n) => n + 1) // files may have been reverted
      sendText(text)
    },
    [editing, sendText],
  )

  // answerAsk delivers an ask_user answer to the paused turn. Unlike sending a
  // message, this resumes the SAME turn: the answer becomes the tool result and
  // the model keeps going. The stream is already open, so nothing restarts.
  const answerAsk = useCallback(
    (answer: string) => {
      const id = askId
      if (!id) return
      setAskId(undefined)
      setLive((s) => ({ ...s, waiting: false }))
      void post(`/asks/${encodeURIComponent(id)}`, { answer }).catch((e) => {
        setError((e as Error).message)
      })
    },
    [askId],
  )

  const complete = (c: CommandSpec) => {
    // Commands that take arguments keep the composer open on a trailing space;
    // ones that do not are ready to send.
    setInput(`/${c.name}${c.args ? ' ' : ''}`)
    textareaRef.current?.focus()
  }

  const readDataURL = (file: File) =>
    new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.onerror = () => reject(reader.error)
      reader.readAsDataURL(file)
    })

  /**
   * Attach files. Images become data URLs (the vision API takes them inline).
   * Everything else is uploaded to a temp dir and tracked by path; the agent
   * reads it with the read_document tool via the path we hand it on send.
   */
  const attachFiles = useCallback(async (files: FileList | File[]) => {
    const all = Array.from(files)
    const imgs = all.filter((f) => f.type.startsWith('image/'))
    const others = all.filter((f) => !f.type.startsWith('image/'))
    const cursor = isCursorTarget(targetRef.current)
    const limit = composerImageLimit(cursor ? 'cursor' : 'chat')

    if (imgs.length > 0) {
      const read = await Promise.all(imgs.slice(0, limit).map(readDataURL))
      setImages((prev) => [...prev, ...read].slice(0, limit))
    }

    // A Cursor cloud VM cannot read a path on this machine, so a document is
    // refused at the point it is attached rather than uploaded and ignored.
    if (cursor && others.length > 0) {
      setError(
        t('cursorAttach.documents', { names: others.map((file) => file.name).join(', ') }),
      )
      return
    }

    for (const file of others.slice(0, 4)) {
      try {
        const dataURL = await readDataURL(file)
        const res = await post<{ path: string; name: string }>('/upload', {
          session_id: sessionIdRef.current ?? '',
          name: file.name,
          data: dataURL,
        })
        setDocs((prev) => [...prev, { path: res.path, name: res.name }].slice(0, 8))
      } catch (e) {
        setError((e as Error).message)
      }
    }
  }, [t])

  // Pasting a screenshot is the fastest way to show the agent something.
  const onPaste = (e: React.ClipboardEvent) => {
    const files = Array.from(e.clipboardData.files)
    if (files.length > 0) {
      e.preventDefault()
      void attachFiles(files)
    }
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (matches.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setPaletteSel((i) => (i + 1) % matches.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setPaletteSel((i) => (i - 1 + matches.length) % matches.length)
        return
      }
      if (e.key === 'Tab') {
        e.preventDefault()
        complete(matches[paletteSel])
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setInput('')
        return
      }
      // Enter completes a partial name but sends one that is already whole,
      // so typing a full command and pressing Enter does what it looks like.
      if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
        const typed = input.slice(1).toLowerCase()
        if (!commands.some((c) => c.name === typed)) {
          e.preventDefault()
          complete(matches[paletteSel])
          return
        }
      }
    }
    // Shell-style prompt history: ↑ older, ↓ newer. Only when the caret is on
    // the first/last line so multi-line editing still moves the cursor normally.
    if (e.key === 'ArrowUp' && !e.shiftKey && !e.altKey && !e.metaKey && !e.ctrlKey) {
      const el = e.currentTarget
      if (inputHistory.length > 0 && caretOnFirstLine(el)) {
        e.preventDefault()
        if (historyPos === -1) draftRef.current = input
        const idx = historyPos === -1 ? 0 : Math.min(historyPos + 1, inputHistory.length - 1)
        setHistoryPos(idx)
        setInput(inputHistory[idx] ?? '')
        return
      }
    }
    if (e.key === 'ArrowDown' && !e.shiftKey && !e.altKey && !e.metaKey && !e.ctrlKey) {
      const el = e.currentTarget
      if (historyPos >= 0 && caretOnLastLine(el)) {
        e.preventDefault()
        if (historyPos <= 0) {
          setHistoryPos(-1)
          setInput(draftRef.current)
        } else {
          const idx = historyPos - 1
          setHistoryPos(idx)
          setInput(inputHistory[idx] ?? '')
        }
        return
      }
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      send()
    }
  }

  // Typing while browsing history leaves history mode (treat as new draft).
  const onInputChange = useCallback((value: string) => {
    if (historyPos !== -1) {
      setHistoryPos(-1)
      draftRef.current = ''
    }
    setInput(value)
  }, [historyPos])

  // Virtuoso's `components` must keep a stable identity. Declared inline it was a
  // fresh object — and a fresh Footer component type — on every render, so the
  // list unmounted and remounted the footer on each streaming tick, resizing the
  // scroller under itself. Footer reads live values through refs so the component
  // type never has to change.
  const approvalsRef = useRef(approvals)
  approvalsRef.current = approvals
  const errorRef = useRef(error)
  errorRef.current = error
  // Re-render the footer when its contents actually change (not per token).
  const footerTick = `${approvals.map((a) => `${a.id}:${a.decided ?? ''}`).join(',')}|${error ?? ''}`
  const virtuosoComponents = useMemo(
    () => ({
      Footer: () => (
        <div className="mx-auto w-full max-w-3xl space-y-5 px-4 pb-6 sm:px-6">
          {approvalsRef.current.map((a) => (
            <ApprovalCard
              key={a.id}
              approval={a}
              onDecided={(id, decision) =>
                setApprovals((prev) =>
                  prev.map((x) => (x.id === id ? { ...x, decided: decision } : x)),
                )
              }
            />
          ))}
          {errorRef.current ? <ErrorBanner message={errorRef.current} /> : null}
        </div>
      ),
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [footerTick],
  )

  const newChat = () => {
    stop()
    lastSession.clear()
    setMessages([])
    setTitle('')
    setApprovals([])
    // A new Antares chat is a new Cursor conversation too: the next Cursor turn
    // starts a fresh agent rather than following up on the previous one.
    setCursorState(null)
    setLastCursorRun(null)
    detachedRef.current = false
    setDetached(false)
    // Keep the remembered role for the new chat instead of resetting to default.
    setRole(localStorage.getItem('antares:last-role') ?? '')
    // A project binding belongs to one session; a new chat starts unbound.
    setProjectDir('')
    projectDirRef.current = ''
    setPendingProject('')
    setSidebarOpen(true)
    setCtxUsed(0)
    navigate('/', { state: { fresh: true } })
  }

  // composerCard renders the input surface. When `withTasks` is set, the task
  // list is folded into the top of the same card (normal view); the empty state
  // passes it false so there is nothing to fold in.
  const composerCard = (withTasks: boolean) => (
    <>
      <SlashPalette matches={matches} selected={paletteSel} onPick={complete} />
      <Composer
        ref={textareaRef}
        value={input}
        images={images}
        docs={docs}
        onAttach={attachFiles}
        onRemoveImage={(i) => setImages((prev) => prev.filter((_, x) => x !== i))}
        onRemoveDoc={(i) => setDocs((prev) => prev.filter((_, x) => x !== i))}
        onPaste={onPaste}
        onChange={onInputChange}
        onKeyDown={onKeyDown}
        onSend={send}
        onStop={stop}
        streaming={streaming}
        canSend={canSend}
        pendingLabel={t('target.resolving')}
        placeholder={t('chat.placeholder')}
        sendLabel={t('chat.send')}
        stopLabel={t('chat.stop')}
        attachLabel={t('chat.attach')}
        roleSlot={
          <div className="flex min-w-0 items-center gap-1.5">
            {/* A Cursor run has no Antares role and no generic reasoning
                override — its own variant controls take that place. */}
            {cursorMode ? null : <RolePicker value={role} onChange={pickRole} compact />}
            {/* The running stream owns the target: changing it mid-turn would
                leave Stop and the next send disagreeing about where it went. */}
            <ModelPicker value={target} onChange={selectTarget} disabled={streaming} />
            {cursorMode && cursorOptions ? (
              <CursorOptions
                value={cursorOptions}
                onChange={changeCursorOptions}
                projectDir={projectDir}
                lastStarted={lastCursorRun}
                disabled={streaming}
              />
            ) : (
              <ReasoningPicker
                value={reasoning}
                capability={
                  target?.kind === 'chat' ? target.reasoningCapability : undefined
                }
                onChange={pickReasoning}
                compact
              />
            )}
            <ProjectPicker
              value={projectDir}
              onChange={(dir) => {
                // Clearing the binding is immediate; picking a folder first asks
                // whether the agent should analyze the project.
                if (!dir) setProjectDir('')
                else setPendingProject(dir)
              }}
              locked={Boolean(sessionId)}
            />
          </div>
        }
        topSlot={
          withTasks ? (
            <TaskBar
              tasks={tasks}
              live={streaming}
              session={sessionId}
              onOpenSubAgent={setViewingAgent}
            />
          ) : undefined
        }
        contextSlot={<ContextBar used={ctxUsed} window={ctxWindow} />}
      />
    </>
  )

  const isEmpty = !loading && messages.length === 0

  // The "analyze first?" dialog is rendered in both the empty and normal states.
  const analyzeDialog = (
    <AnalyzeProjectDialog
      open={Boolean(pendingProject)}
      projectDir={pendingProject}
      onChoose={resolveAnalyze}
    />
  )

  // Empty state mirrors the familiar centred layout: greeting, composer, then
  // starter prompts — no bottom-anchored bar on an otherwise blank page.
  if (isEmpty) {
    return (
      <div className="flex min-h-[calc(100dvh-8rem)] flex-col lg:min-h-dvh">
        {analyzeDialog}
        <div className="flex flex-1 items-center justify-center px-4 py-10 sm:px-6">
          <div className="w-full max-w-3xl space-y-6">
            <div className="space-y-3 text-center">
              <img
                src="/antares-192.png"
                alt=""
                aria-hidden
                width={64}
                height={64}
                className="mx-auto size-16 select-none object-contain"
                draggable={false}
              />
              <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">
                {t('chat.welcomeTitle')}
              </h1>
              <p className="mx-auto max-w-lg text-sm text-muted-foreground">
                {t('chat.welcomeDesc')}
              </p>
            </div>

            {composerCard(false)}

            <div className="grid gap-2 sm:grid-cols-2">
              {SUGGESTION_KEYS.map((key) => (
                <button
                  key={key}
                  onClick={() => {
                    setInput(t(key))
                    textareaRef.current?.focus()
                  }}
                  className="rounded-[var(--radius-md)] border border-border bg-card px-3.5 py-3 text-left text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-foreground sm:text-sm"
                >
                  {t(key)}
                </button>
              ))}
            </div>

            {error ? <ErrorBanner message={error} /> : null}
          </div>
        </div>
      </div>
    )
  }

  // A project session splits the view: the chat column on the left and a
  // collapsible sidebar on the right. An ordinary chat renders full width.
  const isProject = Boolean(projectDir)

  return (
    <div className="flex h-[calc(100dvh-8rem)] overflow-hidden lg:h-dvh">
      <div className="relative flex min-w-0 flex-1 flex-col overflow-x-hidden">
      {analyzeDialog}
      <EditMessageDialog
        open={Boolean(editing)}
        onOpenChange={(o) => !o && setEditing(null)}
        sessionId={sessionId}
        messageId={editing?.id ?? ''}
        initialText={editing?.content ?? ''}
        onSubmit={applyEdit}
      />
      {/* Sub-agent live view: overlays the chat while keeping the main
          transcript state intact underneath, so "back to Main" is instant. */}
      {viewingAgent ? (
        <div className="absolute inset-0 z-20 bg-background">
          <SubAgentPanel agent={viewingAgent} onBack={() => setViewingAgent(null)} />
        </div>
      ) : null}

      <div className="flex items-center gap-3 border-b border-border px-4 py-3 sm:px-6">
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">{title || t('chat.newConversation')}</p>
          {sessionId ? (
            <p className="truncate text-[11px] text-muted-foreground">
              {t('chat.session')} {sessionId.slice(0, 12)}
            </p>
          ) : null}
        </div>
        <Button variant="outline" size="sm" onClick={newChat} className="gap-1.5">
          <Plus className="size-4" />
          <span className="hidden sm:inline">{t('common.new')}</span>
        </Button>
        {isProject ? (
          <Button
            // On desktop the sidebar is a docked column toggled by sidebarOpen;
            // on mobile it is an overlay toggled by sidebarMobileOpen. This one
            // button drives whichever applies, and always shows for a project.
            variant="outline"
            size="icon-sm"
            onClick={() => {
              // Toggle only the state for the current breakpoint (lg = 1024px),
              // so the desktop column and mobile overlay don't fight each other.
              if (window.matchMedia('(min-width: 1024px)').matches) {
                setSidebarOpen((v) => !v)
              } else {
                setSidebarMobileOpen((v) => !v)
              }
            }}
            title={t('project.sidebarShow')}
            aria-label={t('project.sidebarShow')}
          >
            <SidebarSimple className="size-4" mirrored />
          </Button>
        ) : null}
      </div>

      {loading ? (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-3xl space-y-6 px-4 py-6 sm:px-6">
            <SkeletonMessage />
            <SkeletonMessage />
          </div>
        </div>
      ) : (
        // Virtualised transcript: only on-screen messages are in the DOM, so a
        // very long session stays light. followOutput keeps it pinned to the
        // newest message only while the user is at the bottom — scroll up and it
        // stops, scroll back and it resumes.
        //
        // followOutput is `true`, not "auto": "auto" scrolls only *after* it has
        // re-measured, so while a message is streaming (its height grows on every
        // token) the list plays catch-up — measure, scroll, content grows, measure
        // again — which reads as the viewport juddering near the bottom.
        //
        // initialTopMostItemIndex is deliberately NOT derived from messages.length
        // here: it only defines the *initial* position, but recomputing it on every
        // render re-anchors the list mid-stream. See initialIndexRef.
        <Virtuoso
          ref={virtuosoRef}
          className="min-h-0 flex-1"
          data={messages}
          followOutput={true}
          initialTopMostItemIndex={initialIndexRef.current}
          components={virtuosoComponents}
          computeItemKey={(_, m) => m.id}
          itemContent={(_, m) => (
            <div className="mx-auto w-full min-w-0 max-w-3xl overflow-x-clip px-4 sm:px-6">
              <div className="min-w-0 py-2.5">
                <MessageBubble
                  message={m}
                  showReasoning={showReasoning}
                  askActive={!!askId}
                  onAnswer={answerAsk}
                  onEdit={
                    streaming ? undefined : (id, content) => setEditing({ id, content })
                  }
                />
              </div>
            </div>
          )}
        />
      )}

      {/* Floating composer: sits close to the last message rather than pinned
          against the very bottom edge of the viewport.

          The streaming indicator lives here, OUTSIDE the virtualised list. Its
          height changes every second (the elapsed-seconds counter, and notice /
          tool labels of varying length). Inside the list that turned every tick
          into a resize the scroller had to correct for, which is what made the
          viewport judder while pinned to the bottom. */}
      <div className="bg-gradient-to-t from-background via-background to-transparent px-4 pt-3 pb-[max(1.5rem,env(safe-area-inset-bottom))] sm:px-6 sm:pb-[max(2rem,env(safe-area-inset-bottom))]">
        <div className="mx-auto w-full max-w-3xl">
          {streaming ? (
            <div className="pb-2">
              <StreamingIndicator
                turn={live.turn}
                tool={live.tool}
                waiting={live.waiting}
                notice={live.notice}
              />
            </div>
          ) : null}
          {cursorMode || cursorState ? (
            <div className="pb-2">
              <CursorRunBar
                streaming={streaming}
                detached={detached}
                cancelling={cancelling}
                canCancel={Boolean(sessionId)}
                state={cursorState}
                onCancel={cancelCursorRun}
                onReattach={reattach}
              />
            </div>
          ) : null}
          {composerCard(true)}
        </div>
      </div>
      </div>

      {/* Right sidebar — project sessions only. Docked column on desktop, a
          slide-over overlay on mobile. */}
      {isProject ? (
        <>
          {/* Desktop: docked column, toggled by sidebarOpen. */}
          {sidebarOpen ? (
            <div className="hidden w-[26rem] shrink-0 lg:block xl:w-[30rem]">
              <ProjectSidebar
                projectDir={projectDir}
                sessionId={sessionId}
                refreshKey={sidebarRefresh}
                changedFiles={changedFiles}
                toolStats={toolStats}
                onRun={(command) => sendText(t('project.runReq', { command }))}
                onCollapse={() => setSidebarOpen(false)}
              />
            </div>
          ) : null}

          {/* Mobile: full-height overlay from the right + dimmed backdrop. */}
          {sidebarMobileOpen ? (
            <div className="fixed inset-0 z-40 lg:hidden">
              <div
                className="absolute inset-0 bg-black/40"
                onClick={() => setSidebarMobileOpen(false)}
              />
              <div className="absolute inset-y-0 right-0 w-[85%] max-w-sm bg-background shadow-xl">
                <ProjectSidebar
                  projectDir={projectDir}
                  sessionId={sessionId}
                  refreshKey={sidebarRefresh}
                  changedFiles={changedFiles}
                  toolStats={toolStats}
                  onRun={(command) => {
                    setSidebarMobileOpen(false)
                    sendText(t('project.runReq', { command }))
                  }}
                  onCollapse={() => setSidebarMobileOpen(false)}
                />
              </div>
            </div>
          ) : null}
        </>
      ) : null}
    </div>
  )
}

interface ComposerProps {
  value: string
  images: string[]
  docs: { path: string; name: string }[]
  onChange: (v: string) => void
  onAttach: (files: FileList | File[]) => void
  onRemoveImage: (index: number) => void
  onRemoveDoc: (index: number) => void
  onPaste: (e: React.ClipboardEvent) => void
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
  onSend: () => void
  onStop: () => void
  streaming: boolean
  /** False while this session's execution target is still being resolved. */
  canSend: boolean
  pendingLabel: string
  placeholder: string
  sendLabel: string
  stopLabel: string
  attachLabel: string
  // roleSlot renders on the left of the bottom control row (the role picker).
  roleSlot?: React.ReactNode
  // topSlot renders above the textarea inside the same card (the task list),
  // separated by a divider — so tasks and composer read as one surface.
  topSlot?: React.ReactNode
  // contextSlot renders on the right of the control row, before attach/send —
  // the context-window fill gauge.
  contextSlot?: React.ReactNode
}

/** Rounded single-surface composer with the actions inside the field. */
const Composer = ({
  ref,
  value,
  images,
  docs,
  onChange,
  onAttach,
  onRemoveImage,
  onRemoveDoc,
  onPaste,
  onKeyDown,
  onSend,
  onStop,
  streaming,
  canSend,
  pendingLabel,
  placeholder,
  sendLabel,
  stopLabel,
  attachLabel,
  roleSlot,
  topSlot,
  contextSlot,
}: ComposerProps & { ref: React.RefObject<HTMLTextAreaElement | null> }) => {
  const fileRef = useRef<HTMLInputElement>(null)

  return (
    // No overflow-hidden: the role picker's dropdown pops upward out of this
    // card, and clipping would cut it off. The top section rounds its own top
    // corners instead so the merged look survives without clipping.
    <div className="rounded-[var(--radius-xl)] border border-border bg-card shadow-sm transition-colors focus-within:border-ring">
      {/* Task list / sub-agents (when present) sit above the input, in the same
          card. The section renders its own bottom divider only when it actually
          has content, so an empty TaskBar leaves no phantom line. */}
      {topSlot ? (
        <div className="overflow-hidden rounded-t-[var(--radius-xl)]">{topSlot}</div>
      ) : null}

      <div className="p-2">
        {images.length > 0 ? (
          <div className="mb-2 flex flex-wrap gap-2 px-1 pt-1">
            {images.map((src, i) => (
              <div key={i} className="group relative">
                <img
                  src={src}
                  alt=""
                  className="size-16 rounded-[var(--radius-sm)] border border-border object-cover"
                />
                <button
                  onClick={() => onRemoveImage(i)}
                  aria-label="Remove"
                  className="absolute -right-1.5 -top-1.5 rounded-full bg-background p-0.5 text-muted-foreground shadow ring-1 ring-border transition-colors hover:text-destructive"
                >
                  <X className="size-3.5" weight="bold" />
                </button>
              </div>
            ))}
          </div>
        ) : null}

        {docs.length > 0 ? (
          <div className="mb-2 flex flex-wrap gap-2 px-1 pt-1">
            {docs.map((d, i) => (
              <div
                key={i}
                className="group flex max-w-56 items-center gap-1.5 rounded-[var(--radius-sm)] border border-border bg-muted/40 py-1 pl-2 pr-1 text-xs"
              >
                <FileText className="size-4 shrink-0 text-muted-foreground" />
                <span className="truncate" title={d.name}>
                  {d.name}
                </span>
                <button
                  onClick={() => onRemoveDoc(i)}
                  aria-label="Remove"
                  className="shrink-0 rounded-full p-0.5 text-muted-foreground transition-colors hover:text-destructive"
                >
                  <X className="size-3.5" weight="bold" />
                </button>
              </div>
            ))}
          </div>
        ) : null}

        <input
          ref={fileRef}
          type="file"
          multiple
          hidden
          onChange={(e) => {
            if (e.target.files) onAttach(e.target.files)
            // Reset so picking the same file twice still fires.
            e.target.value = ''
          }}
        />

        {/* Row 1: the input, full width. */}
        <Textarea
          ref={ref}
          rows={1}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          placeholder={placeholder}
          className="max-h-50 min-h-9 w-full resize-none border-0 bg-transparent px-1.5 py-1.5 shadow-none outline-none focus-visible:border-0 focus-visible:outline-none focus-visible:ring-0 focus-visible:ring-offset-0"
        />

        {/* Row 2: controls — pickers on the left, actions on the right. The
            pickers collapse to icon-only chips on small screens (labels return
            at sm), so the whole row stays on one tidy line even on a phone. */}
        <div className="mt-1 flex items-center gap-1.5">
          <div className="flex min-w-0 flex-1 items-center gap-1.5">{roleSlot}</div>
          <div className="flex shrink-0 items-center gap-1.5">
            {contextSlot}
            <Button
              size="icon"
              variant="ghost"
              onClick={() => fileRef.current?.click()}
              aria-label={attachLabel}
              className="shrink-0 rounded-full text-muted-foreground"
            >
              <Paperclip className="size-5" />
            </Button>
            {streaming ? (
              <Button
                size="icon"
                variant="destructive"
                onClick={onStop}
                aria-label={stopLabel}
                className="shrink-0 rounded-full"
              >
                <Stop weight="fill" />
              </Button>
            ) : (
              <Button
                size="icon"
                onClick={onSend}
                disabled={!canSend || (!value.trim() && images.length === 0)}
                aria-label={sendLabel}
                title={canSend ? sendLabel : pendingLabel}
                className="shrink-0 rounded-full"
              >
                <ArrowUp weight="bold" />
              </Button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

/** Rounded compact token count for the context gauge: 512, 48k, 1.2M, 1M.
 *  Distinct from formatCount (which keeps a decimal for k) — the gauge is an
 *  approximation, so whole-k reads cleaner (48k, not 48.2k). */
function ctxTokens(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${Math.round(n / 1000)}k`
  const m = n / 1_000_000
  return `${(Math.round(m * 10) / 10).toString().replace(/\.0$/, '')}M`
}

/** Context-window fill gauge for the composer: a compact progress RING that,
 *  on hover, reveals a popover card with the used/total token counts, the
 *  percentage, and a horizontal fill bar. The ring is tinted green→amber→red as
 *  the window fills. Before the first turn it reads 0 / 0. */
function ContextBar({ used, window }: { used: number; window: number }) {
  const { t } = useI18n()
  const pct = window > 0 ? Math.min(100, Math.round((used / window) * 100)) : 0
  const tone =
    pct >= 90 ? 'var(--destructive)' : pct >= 70 ? 'var(--warning)' : 'var(--success)'

  // SVG ring geometry.
  const r = 7
  const circ = 2 * Math.PI * r
  const dash = (pct / 100) * circ

  // Click/tap toggles the detail popover — works on touch (no hover). On devices
  // that do have hover, opening on hover too is a nicety layered on top.
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  return (
    <div
      ref={ref}
      className="group relative block"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
    >
      <button
        type="button"
        aria-label={t('chat.contextLabel')}
        onClick={() => setOpen((v) => !v)}
        className="grid size-7 place-items-center rounded-full text-muted-foreground transition-colors hover:bg-muted"
      >
        <svg width="18" height="18" viewBox="0 0 18 18" className="-rotate-90">
          <circle cx="9" cy="9" r={r} fill="none" stroke="var(--muted)" strokeWidth="2.5" />
          <circle
            cx="9"
            cy="9"
            r={r}
            fill="none"
            stroke={tone}
            strokeWidth="2.5"
            strokeLinecap="round"
            strokeDasharray={`${dash} ${circ}`}
            className="transition-all duration-500"
          />
        </svg>
      </button>

      {/* Detail popover, anchored above the ring. Shown on tap (mobile) or
          hover (desktop). */}
      {open ? (
        <div className="absolute bottom-full right-0 z-30 mb-2 w-60 origin-bottom-right">
          <div className="rounded-[var(--radius-lg)] border border-border bg-popover p-3 shadow-lg">
            <div className="flex items-baseline justify-between">
              <span className="text-xs font-medium">{t('chat.contextLabel')}</span>
              <span className="text-[11px] tabular-nums text-muted-foreground">
                {ctxTokens(used)}/{ctxTokens(window)}{' '}
                <span style={{ color: tone }}>({pct}%)</span>
              </span>
            </div>
            <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full transition-all duration-500"
                style={{ width: `${pct === 0 ? 0 : Math.max(2, pct)}%`, backgroundColor: tone }}
              />
            </div>
            <p className="mt-2 text-[10.5px] leading-relaxed text-muted-foreground">
              {t('chat.contextHint')}
            </p>
          </div>
        </div>
      ) : null}
    </div>
  )
}

/**
 * The Cursor run's own controls. Stop (in the composer) only closes this
 * browser's stream, so this bar is where the run's remote state, a way back to
 * it, and the separate approved cancellation live.
 */
function CursorRunBar({
  streaming,
  detached,
  cancelling,
  canCancel,
  state,
  onCancel,
  onReattach,
}: {
  streaming: boolean
  detached: boolean
  cancelling: boolean
  canCancel: boolean
  state: CursorHydration | null
  onCancel: () => void
  onReattach: () => void
}) {
  const { t } = useI18n()
  const branches = (state?.branches ?? []).filter((b) => b.branch || b.prUrl)

  return (
    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 rounded-[var(--radius-md)] border border-border bg-card/60 px-2.5 py-1.5 text-[11px] text-muted-foreground">
      <span className="flex items-center gap-1.5 font-medium text-foreground/70">
        <Cloud className="size-3.5 text-primary" />
        {t('cursor.runLabel')}
      </span>
      {state?.remoteStatus ? (
        <span className="font-mono text-[10px]">{state.remoteStatus}</span>
      ) : null}
      {detached ? <span className="min-w-0">{t('cursor.detachedNotice')}</span> : null}
      {branches.map((branch) => (
        <span key={`${branch.repoUrl}/${branch.branch}`} className="flex items-center gap-1">
          <GitBranch className="size-3 shrink-0" />
          {branch.prUrl ? (
            <a
              href={branch.prUrl}
              target="_blank"
              rel="noreferrer noopener"
              className="truncate text-primary underline underline-offset-2"
            >
              {branch.branch || branch.prUrl}
            </a>
          ) : (
            <span className="truncate font-mono text-[10px]">{branch.branch}</span>
          )}
        </span>
      ))}
      <div className="ml-auto flex shrink-0 items-center gap-1.5">
        {detached ? (
          <Button size="sm" variant="outline" onClick={onReattach}>
            {t('cursor.reattach')}
          </Button>
        ) : null}
        {/* A run the server still owns can be cancelled even when this browser
            is neither streaming nor detached — after a reload, for example. */}
        {canCancel && (streaming || detached || state?.running === true) ? (
          <Button
            size="sm"
            variant="outline"
            loading={cancelling}
            onClick={onCancel}
            className="gap-1.5"
            title={t('cursor.cancelHint')}
          >
            <Prohibit className="size-3.5" />
            {t('cursor.cancel')}
          </Button>
        ) : null}
      </div>
    </div>
  )
}

function ErrorBanner({ message, className }: { message: string; className?: string }) {
  return (
    <div
      className={cn(
        'flex items-start gap-2 rounded-[var(--radius-sm)] border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive',
        className,
      )}
    >
      <Warning className="mt-0.5 size-4 shrink-0" weight="fill" />
      <span className="min-w-0 break-words">{message}</span>
    </div>
  )
}

export function StreamingIndicator({
  turn,
  tool,
  waiting,
  notice,
}: {
  turn?: number
  tool?: string
  waiting?: boolean
  notice?: string
}) {
  const { t } = useI18n()
  const [secs, setSecs] = useState(0)
  useEffect(() => {
    setSecs(0)
    const start = Date.now()
    const id = setInterval(() => setSecs(Math.round((Date.now() - start) / 1000)), 1000)
    return () => clearInterval(id)
  }, [turn, tool, waiting, notice])
  // Paused on a question: no timer, no pulsing "working" — the run is idle by
  // design, waiting on the person. Otherwise show the running tool / step.
  if (waiting) {
    return (
      <div className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
        <span className="size-1.5 rounded-full bg-[var(--warning)]" />
        <span className="font-medium text-foreground/70">{t('chat.waitingAnswer')}</span>
      </div>
    )
  }
  const label = tool
    ? t('chat.running', { tool })
    : notice
      ? notice
      : turn && turn > 1
        ? t('chat.workingStep', { n: turn })
        : t('chat.working')
  return (
    <div className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
      <span className="flex items-center gap-1">
        <span className="pulse-dot size-1.5 rounded-full bg-primary" />
        <span className="pulse-dot size-1.5 rounded-full bg-primary [animation-delay:0.2s]" />
        <span className="pulse-dot size-1.5 rounded-full bg-primary [animation-delay:0.4s]" />
      </span>
      <span className="min-w-0 font-medium text-foreground/70">{label}</span>
      <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground/60">· {secs}s</span>
    </div>
  )
}

// Memoised for the same reason as ToolCallCard: on a message that grows to many
// segments during one streaming turn, only the changed segment should re-render.
// `text` is a primitive, so memo compares by value and finished blocks are free.
const ReasoningBlock = memo(function ReasoningBlock({ text }: { text: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  // A slim inline toggle rather than a boxed card: collapsed reasoning should
  // barely take a line, expanding into a quiet left-ruled block when opened.
  return (
    <div className="text-muted-foreground">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 text-[11px] font-medium transition-colors hover:text-foreground"
      >
        <Brain className="size-3.5" />
        {t('chat.reasoning')}
        <CaretDown className={cn('size-3 transition-transform', open && 'rotate-180')} />
      </button>
      {open ? (
        <div className="mt-1.5 border-l-2 border-border pl-3 text-xs">
          <Markdown content={text} />
        </div>
      ) : null}
    </div>
  )
})

// A plain-text segment. Memoised so it is skipped when an unrelated segment on
// the same message changes during streaming.
const TextSegment = memo(function TextSegment({ text }: { text: string }) {
  return (
    <div className="text-[13px] leading-relaxed">
      <Markdown content={text} />
    </div>
  )
})

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
}) {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    if (await copyText(message.content)) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }

  if (message.role === 'user') {
    // Full-width, quietly set apart with a left rule and a faint tint — the
    // model's replies own the column, the prompt sits above them as context.
    return (
      <div className="fade-up space-y-2">
        {message.images?.length ? (
          <div className="flex flex-wrap gap-2">
            {message.images.map((src, i) => (
              <img
                key={i}
                src={src}
                alt=""
                className="max-h-48 rounded-[var(--radius-md)] border border-border object-contain"
              />
            ))}
          </div>
        ) : null}
        {message.docs?.length ? (
          <div className="flex flex-wrap gap-2">
            {message.docs.map((d, i) => (
              <div
                key={i}
                className="flex max-w-56 items-center gap-1.5 rounded-[var(--radius-sm)] border border-border bg-muted/40 px-2 py-1 text-xs"
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
          <div className="group/user relative rounded-[var(--radius-md)] border-l-2 border-primary bg-muted/40 px-3.5 py-2.5">
            <p className="whitespace-pre-wrap break-words text-[13px] leading-relaxed text-foreground">
              {message.content}
            </p>
            {/* Edit affordance: re-send the conversation from this message,
                optionally reverting file changes made after it. Only when an
                onEdit handler is wired and the message has a real (persisted) id
                — a local optimistic id cannot be edited server-side yet. */}
            {onEdit && !message.id.startsWith('local_') && !message.id.startsWith('you_') ? (
              <button
                onClick={() => onEdit(message.id, message.content)}
                title={t('edit.button')}
                aria-label={t('edit.button')}
                className="absolute -top-2 right-2 hidden items-center gap-1 rounded-full border border-border bg-background px-2 py-0.5 text-[10px] text-muted-foreground shadow-sm transition-colors hover:text-foreground group-hover/user:flex"
              >
                <PencilSimple className="size-3" />
                {t('edit.button')}
              </button>
            ) : null}
          </div>
        ) : null}
      </div>
    )
  }

  // Slash-command output is not the model talking. Setting it apart keeps the
  // transcript honest about what came from where.
  if (message.role === 'system') {
    return (
      <div className="fade-up rounded-[var(--radius-md)] border border-border bg-muted/40 px-3.5 py-3">
        <div className="mb-1.5 flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
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
    <div className="group min-w-0 space-y-1.5 fade-up">
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
              <div className="text-[13px] leading-relaxed">
                <Markdown content={message.content} />
              </div>
            ) : null}
          </>}

      {message.error ? <ErrorBanner message={message.error} /> : null}

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
            <span className="text-[10px] text-muted-foreground">
              {t('chat.tokensOut', { n: message.tokensOut })}
            </span>
          ) : null}
          {message.createdAt ? (
            <span className="text-[10px] text-muted-foreground">{timeAgo(message.createdAt)}</span>
          ) : null}
        </div>
      ) : null}
    </div>
  )
})
