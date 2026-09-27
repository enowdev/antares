import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import {
  ArrowUp,
  FileText,
  Paperclip,
  Plus,
  SidebarSimple,
  Stop,
  X,
} from '@phosphor-icons/react'
import { ApiError, get, post } from '@/lib/api'
import {
  DEFAULT_MAX_LIVE_REASONING_CHARS,
  hydrate,
  normalizeErrorPayload,
  type ChatMessage,
  type SessionDetail,
} from '@/lib/chatTranscript'
import { useChatStream } from '@/lib/useChatStream'
import type { LiveStatus, ForegroundRun } from '@/lib/chatStreamController'
import { copyText } from '@/lib/clipboard'
import { useI18n, type MessageKey } from '@/lib/i18n'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/primitives'
import { SkeletonMessage } from '@/components/ui/skeleton'
import { ErrorBanner, MessageBubble } from '@/components/chat/ChatTranscript'
import { TaskBar, parseTasks } from '@/components/chat/TaskBar'
import { ApprovalCard, type ApprovalView } from '@/components/chat/ApprovalCard'
import { RolePicker } from '@/components/chat/RolePicker'
import { ModelPicker } from '@/components/chat/ModelPicker'
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

/** Prefer the raw API error body when it carries a server-supplied string —
 *  `{ error }` or a plain string — so a 500's real message survives instead of
 *  the generic "HTTP 500". Everything else falls back to err.message. */
function pickErrorText(err: Error): string {
  if (err instanceof ApiError) {
    const body = err.body
    if (typeof body === 'string' && body.trim() !== '') return body
    if (body && typeof body === 'object' && 'error' in body) {
      const field = body.error
      if (typeof field === 'string' && field.trim() !== '') return field
    }
  }
  return err.message
}

export default function ChatPage() {
  const { sessionId } = useParams<{ sessionId: string }>()
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useI18n()

  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [loading, setLoading] = useState(!!sessionId)
  const [streaming, setStreaming] = useState(false)
  // Live status for the streaming indicator: which step, and what tool (if any)
  // is running right now. Reset at the start of every send.
  const [live, setLive] = useState<LiveStatus>({ turn: 1 })
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
  // Per-turn reasoning effort picked in the composer. Empty means "use the
  // configured default" (agent.reasoning_effort, then model). Persisted so the
  // choice survives a reload, mirroring the role picker.
  const [reasoning, setReasoning] = useState(
    () => localStorage.getItem('antares:reasoning') ?? '',
  )
  const pickReasoning = useCallback((r: string) => {
    setReasoning(r)
    if (r) localStorage.setItem('antares:reasoning', r)
    else localStorage.removeItem('antares:reasoning')
  }, [])
  // Per-chat model override, chosen via the picker in the composer. Kept in a
  // ref so the stream request closure always reads the latest selection.
  const [activeModel, setActiveModel] = useState('')
  const activeModelRef = useRef('')
  useEffect(() => {
    activeModelRef.current = activeModel
  }, [activeModel])
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
  // The model's context window, known even before the first turn. Refetched
  // whenever the active model changes so the gauge reflects the new pick
  // immediately — no need to wait for the first usage event.
  useEffect(() => {
    const q = activeModel ? `?model=${encodeURIComponent(activeModel)}` : ''
    get<{ context_window?: number }>(`/context-window${q}`)
      .then((d) => {
        const w = Number(d.context_window ?? 0)
        if (w > 0) setCtxWindow(w)
      })
      .catch(() => {})
  }, [activeModel])
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
  // Foreground /chat run handle. Held so a session switch (or an explicit
  // stop) can dispose it and silence every late frame the aborted socket
  // still delivers — a text/reasoning/done event from session A must NEVER
  // apply to session B's transcript.
  const foregroundRunRef = useRef<ForegroundRun | null>(null)
  // The session id the foreground run belongs to. `undefined` for a run
  // started before the server assigned an id (brand-new chat).
  const foregroundOwnerRef = useRef<string | undefined>(undefined)
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
    // A route change (or the URL re-syncing to a stale/deleted id) belongs to
    // a different session than the run currently in flight. Dispose it so a
    // late text/reasoning/done/hydrate callback from the old socket can never
    // mutate the new transcript.
    //
    // Ownership rule: a run is safe iff its owner id equals the route id, OR
    // the URL just adopted a brand-new-chat id that this run itself produced
    // (localSessionRef, set inside the run's onSession callback). An owner of
    // `undefined` means "no session assigned yet" — that run is a pending
    // handshake for whatever route was mounted when it started, so if the
    // route changes mid-handshake the user has moved on and the run MUST die.
    const run = foregroundRunRef.current
    if (!run) return
    const owner = foregroundOwnerRef.current
    const routeMatches = owner === sessionId
    const justAdopted = sessionId != null && sessionId === localSessionRef.current
    if (routeMatches || justAdopted) return
    run.dispose()
    foregroundRunRef.current = null
    foregroundOwnerRef.current = undefined
    // dispose() deliberately skips onSettled, so the adoption ref has to be
    // cleared here too: left set, the hydrate effect below would keep
    // short-circuiting and show another session's transcript under this URL.
    localSessionRef.current = null
    abortRef.current = null
    foregroundActiveRef.current = false
    setStreaming(false)
  }, [sessionId])
  const virtuosoRef = useRef<VirtuosoHandle>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  // Where the list opens. Captured once per mount: Virtuoso treats this as the
  // initial anchor, so recomputing it from messages.length on every render
  // re-anchors the list mid-stream and fights followOutput.
  const initialIndexRef = useRef(0)

  // Ref mirroring "a foreground turn is currently streaming" so the standing
  // attach can retry-later without re-rendering. Kept in step with abortRef,
  // which is updated at each foreground send/done/error path.
  const foregroundActiveRef = useRef(false)

  const setUsage = useCallback((u: { used?: number; window?: number }) => {
    if (u.used != null) setCtxUsed(u.used)
    if (u.window != null) setCtxWindow(u.window)
  }, [])

  // Streaming controller + hook: owns the RAF frame batcher and the standing
  // attach reconnection loop. Every state change comes back through the
  // setters below, so the controller stays framework-agnostic (see
  // chatStreamController.test.mjs for the deterministic behavioural suite).
  const chatStream = useChatStream({
    setMessages,
    setLive,
    setAskId,
    setTitle,
    setUsage,
    setError,
    setStreaming,
    showReasoningRef,
    maxLiveReasoningRef,
    isForegroundActiveRef: foregroundActiveRef,
    errorFallback: t('chat.somethingWrong'),
    authFailureMessage:
      t('chat.attachAuthFailed') || 'Dashboard login expired — refresh and sign in again.',
    conversationFallback: t('chat.conversation'),
  })

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
    abortRef.current?.()
    abortRef.current = null
    foregroundRunRef.current = null
    foregroundOwnerRef.current = undefined
    localSessionRef.current = null
    foregroundActiveRef.current = false
    setStreaming(false)
    if (sessionId) {
      void post<{ interrupted: boolean }>('/chat/interrupt', { session_id: sessionId }).catch(() => {
        // The stream is already closed locally. A failed interrupt will surface
        // when the user reattaches instead of leaving the stop button stuck.
      })
    }
  }, [sessionId])


  // Reconnect to a turn still running for this session (after navigating away
  // and back). The controller replays from the last applied cursor so no
  // tokens are missed, builds the assistant message lazily on the first live
  // event, and re-hydrates the persisted session after a done — closing the
  // race where the turn finished between navigation away and back.
  const attachLive = chatStream.attach

  useEffect(() => {
    if (!sessionId) {
      setMessages([])
      setTitle('')
      setProjectDir('')
      setCtxUsed(0)
      setLoading(false)
      return
    }
    // A brand-new chat navigates to its own url mid-stream. The live messages
    // are already on screen; re-fetching now would find the turn not yet
    // persisted and wipe them. Skip the hydrate for that one session.
    if (sessionId === localSessionRef.current) {
      setLoading(false)
      return
    }
    setLoading(true)
    let cancelled = false
    let closeAttach: (() => void) | undefined
    get<SessionDetail>(`/sessions/${sessionId}`)
      .then((d) => {
        if (cancelled) return
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
  }, [sessionId, t, attachLive])

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
            navigate('/system/settings')
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
      if ((!text && attached.length === 0 && attachedDocs.length === 0) || streaming) return
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
      // Attach images/docs so Retry on a pre-hydrate failure can resend them.
      images: attached.length > 0 ? attached : undefined,
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
    foregroundActiveRef.current = true
    setLive({ turn: 1 })

    // Dispose any run that was still winding down (transport already aborted,
    // just silencing any in-flight callbacks) before starting a fresh one.
    foregroundRunRef.current?.dispose()
    foregroundOwnerRef.current = sessionIdRef.current
    const run = chatStream.startRun({
      assistantId,
      path: '/chat',
      body: {
        session_id: sessionIdRef.current ?? '',
        message,
        images: attached,
        role,
        // Per-chat model override; omitted when unset so the server falls
        // back to the configured default.
        ...(activeModelRef.current ? { model: activeModelRef.current } : {}),
        // Per-turn reasoning override; omitted when unset so the server falls
        // back to the configured default.
        ...(reasoning ? { reasoning_effort: reasoning } : {}),
        // Only meaningful when starting a new session; the server ignores it
        // once the session exists. Read from the ref so an auto-analyze turn
        // fired right after binding still carries the project.
        ...(projectDirRef.current && !sessionIdRef.current
          ? { project_dir: projectDirRef.current, index_rag: indexRagRef.current }
          : {}),
      },
      currentSessionId: () => sessionIdRef.current,
      onSession: (id) => {
        if (!id) return
        // Adopt the real session id at once so the next message posts to it
        // rather than opening another session.
        sessionIdRef.current = id
        // The run's owning session was undefined for a brand-new chat; update
        // it here so the sessionId-change effect below won't tear the run down
        // when the URL adoption navigate() fires.
        foregroundOwnerRef.current = id
        if (id !== sessionId) {
          // The server assigned this id — either a brand-new chat, or the one
          // in the url was stale/missing so a fresh session was created.
          // Point the url at the real session (and remember it so the hydrate
          // the navigation triggers does not overwrite the live messages).
          localSessionRef.current = id
          lastSession.set(id)
          navigate(`/c/${id}`, { replace: true })
        }
      },
      onDone: () => {
        // End-of-turn: stop streaming and clear per-run UI state immediately.
        // The run handle and owner ref stay live until onSettled: an async
        // hydrate is still in flight after done, and a route change between
        // now and its resolve MUST be able to dispose() this run so the
        // hydrate cannot overwrite a different session's transcript.
        setStreaming(false)
        setAskId(undefined)
        setLive((s) => ({ ...s, waiting: false }))
        foregroundActiveRef.current = false
        // The turn may have written project_info — refresh the sidebar.
        setSidebarRefresh((n) => n + 1)
      },
      onError: (err) => {
        // A server 'error' event may have already painted the bubble; a
        // subsequent transport failure MUST NOT overwrite it with a generic
        // wrapper of the same event.
        const source: ChatMessage['errorSource'] =
          err instanceof ApiError ? 'http' : 'transport'
        const payload = normalizeErrorPayload(pickErrorText(err))
        const retryPrompt = {
          content: text,
          images: attached.length > 0 ? attached : undefined,
          docs: attachedDocs.length > 0 ? attachedDocs : undefined,
        }
        setMessages((prev) =>
          prev.map((m) => {
            if (m.id !== assistantId) return m
            if (m.error) return m
            return { ...m, error: payload, errorSource: source, retryPrompt }
          }),
        )
        setStreaming(false)
        foregroundActiveRef.current = false
      },
      onFinal: () => {
        setStreaming(false)
        foregroundActiveRef.current = false
      },
      onCleanEof: () => {
        // Preserve any error the server already painted; only fill the gap
        // when the socket truly ended with no signal at all.
        const payload = normalizeErrorPayload(t('chat.streamEnded'))
        const retryPrompt = {
          content: text,
          images: attached.length > 0 ? attached : undefined,
          docs: attachedDocs.length > 0 ? attachedDocs : undefined,
        }
        setMessages((prev) =>
          prev.map((m) => {
            if (m.id !== assistantId) return m
            if (m.error) return m
            return { ...m, error: payload, errorSource: 'stream', retryPrompt }
          }),
        )
      },
      onSettled: () => {
        // Fires only from an ALIVE run (controller guards dispose from
        // echoing). Identity-check the ref so a hydrate that resolves late
        // — after a new send/route change already assigned a fresh run —
        // cannot wipe refs belonging to that fresh run.
        if (foregroundRunRef.current !== run) return
        abortRef.current = null
        foregroundRunRef.current = null
        foregroundOwnerRef.current = undefined
        localSessionRef.current = null
      },
    })
    foregroundRunRef.current = run
    // Legacy abort surface — stop() still calls this. dispose() aborts the
    // socket AND silences every pending callback (event / error / final /
    // rehydrate promise), so a switched-away session never mutates state.
    abortRef.current = () => run.dispose()
    },
    [role, reasoning, projectDir, streaming, sessionId, navigate, runCommand, chatStream, t],
  )

  const send = useCallback(() => {
    const text = input.trim()
    if ((!text && images.length === 0 && docs.length === 0) || streaming) return
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

  /** Resend the preceding user prompt (+ images/docs) for the errored turn.
   *  Prefers the message's own retryPrompt (captured at error creation) so a
   *  merged/reordered row still retries the correct prompt; falls back to a
   *  walk-backwards for server-persisted rows. History stays intact. */
  const retryTurn = useCallback(
    (assistantMessageId: string) => {
      // Synchronous ref guard beats the streaming-state re-render race.
      if (foregroundActiveRef.current || streaming) return
      const idx = messages.findIndex((m) => m.id === assistantMessageId)
      if (idx < 0) return
      const target = messages[idx]
      if (target.retryPrompt) {
        const p = target.retryPrompt
        sendText(p.content, p.images ?? [], p.docs ?? [])
        return
      }
      let user: ChatMessage | undefined
      for (let i = idx - 1; i >= 0; i--) {
        if (messages[i].role === 'user') {
          user = messages[i]
          break
        }
      }
      if (!user) return
      sendText(user.content, user.images ?? [], user.docs ?? [])
    },
    [messages, streaming, sendText],
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

    if (imgs.length > 0) {
      const read = await Promise.all(imgs.slice(0, 4).map(readDataURL))
      setImages((prev) => [...prev, ...read].slice(0, 4))
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
  }, [])

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
        placeholder={t('chat.placeholder')}
        sendLabel={t('chat.send')}
        stopLabel={t('chat.stop')}
        attachLabel={t('chat.attach')}
        roleSlot={
          <div className="flex min-w-0 items-center gap-1.5">
            <RolePicker value={role} onChange={pickRole} compact />
            <ModelPicker onModelChange={setActiveModel} />
            <ReasoningPicker value={reasoning} onChange={pickReasoning} compact />
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
                  onRetry={retryTurn}
                  retryDisabled={streaming}
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
                disabled={!value.trim() && images.length === 0}
                aria-label={sendLabel}
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
