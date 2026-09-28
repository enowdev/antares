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
  /** Render identity for the virtualised list, when it differs from `id`. A
   *  hydrate swaps local ids for server ids; carrying the key over keeps the
   *  row mounted and its measured height, so the transcript does not jump.
   *  Never sent to the server. */
  key?: string
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
  /** Rendered JSON block, one field: `{"error":"..."}`. Set for a terminal
   *  turn failure from any source (server `error`, transport, HTTP, EOF). */
  error?: string
  /** Internal-only origin tag. Kept OFF the rendered/copied payload; drives
   *  the same-session hydrate merge that preserves unpersisted (client-only)
   *  errors, and lets the caller distinguish server-persisted rows. */
  errorSource?: 'server' | 'transport' | 'http' | 'stream'
  /** Explicit prompt to resend on Retry, captured at error-creation time.
   *  Takes precedence over the walk-backwards fallback so a merge that moves
   *  the row past later turns still retries the correct prompt. */
  retryPrompt?: { content: string; images?: string[]; docs?: { path: string; name: string }[] }
  images?: string[]
  // Non-image attachments shown as chips under the user's message.
  docs?: { path: string; name: string }[]
}

/** Coerce any raw error carrier into `{"error":"..."}` pretty-printed with a
 *  two-space indent. Strips every other field so `source`/`status`/`stack`
 *  never leak on screen. A nested object in `.error` is JSON-serialized so
 *  the payload never renders `[object Object]`. */
export function normalizeErrorPayload(raw: unknown): string {
  let text = ''
  if (typeof raw === 'string') {
    text = raw
    const trimmed = raw.trim()
    if (trimmed.startsWith('{')) {
      try {
        const parsed: unknown = JSON.parse(trimmed)
        if (parsed && typeof parsed === 'object' && 'error' in parsed) {
          text = coerceErrorField(parsed.error)
        }
      } catch {
        /* keep original string */
      }
    }
  } else if (raw != null) {
    if (typeof raw === 'object' && 'error' in raw) text = coerceErrorField(raw.error)
    else text = String(raw)
  }
  return JSON.stringify({ error: text }, null, 2)
}

function coerceErrorField(v: unknown): string {
  if (v == null) return ''
  if (typeof v === 'string') return v
  if (typeof v === 'object') {
    try {
      return JSON.stringify(v)
    } catch {
      return ''
    }
  }
  return String(v)
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

export interface SessionDetail {
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
    // Free-form JSON blob. The server carries tool-result flags (is_error)
    // here, so hydrate can restore the error badge that the live tool_result
    // event set — otherwise a page reload would drop it.
    meta?: { is_error?: boolean; [k: string]: unknown } | null
  }>
}

/** Rebuild view models from the persisted message log.
 *
 *  One turn is one assistant view, the same shape the live stream builds: a
 *  turn with tool iterations is persisted as one assistant row per model call,
 *  and those rows fold into a single message whose segments run in order.
 *  Keeping the live and hydrated shapes identical is what lets the end-of-turn
 *  refresh replace the transcript without changing how many rows it has. */
export function hydrate(detail: SessionDetail): ChatMessage[] {
  const out: ChatMessage[] = []
  const pending = new Map<string, ToolCallView>()
  // The assistant view the current turn's rows fold into; any user row
  // (hidden or not) or system row starts the next turn.
  let turn: ChatMessage | null = null

  for (const m of detail.messages) {
    // Hidden messages (e.g. an injected sub-agent result) are context for the
    // model, not something to render — the agent's continuation shows instead.
    // A hidden user row still opens a new turn, as the live stream does.
    if (m.hidden) {
      if (m.role === 'user') turn = null
      continue
    }
    if (m.role === 'tool') {
      const call = pending.get(m.tool_call_id ?? '')
      if (call) {
        call.result = m.content
        call.running = false
        // The agent persists tool errors as message.meta.is_error (see
        // internal/agent.persistToolResult). Without threading it here a page
        // reload dropped the destructive-red badge that the live stream set.
        if (m.meta && m.meta.is_error) call.isError = true
      }
      continue
    }
    const isErrorRow = m.role === 'assistant' && !!(m.meta && m.meta.is_error)
    const msg: ChatMessage = {
      id: m.id,
      role: m.role,
      // A persisted terminal-error row carries the JSON error payload in its
      // `content` field. Route it to `error` (so the transcript renders the
      // JSON block) and blank the content field so it never falls through the
      // Markdown path or gets copied by the assistant Copy button.
      content: isErrorRow ? '' : m.content,
      reasoning: m.reasoning || undefined,
      createdAt: m.created_at,
      tokensIn: m.tokens_in,
      tokensOut: m.tokens_out,
    }
    if (isErrorRow) {
      msg.error = normalizeErrorPayload(m.content)
      msg.errorSource = 'server'
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
    if (msg.role === 'assistant') {
      if (turn) {
        foldInto(turn, msg)
        continue
      }
      turn = msg
    } else {
      turn = null
    }
    out.push(msg)
  }
  return out
}

/** Append one persisted assistant row to its turn's view. Usage is summed, as
 *  the live `usage` event reports the turn's running total. */
function foldInto(turn: ChatMessage, row: ChatMessage) {
  const join = (a?: string, b?: string) => [a, b].filter((v) => v && v.trim() !== '').join('\n\n')
  turn.content = join(turn.content, row.content)
  turn.reasoning = join(turn.reasoning, row.reasoning) || undefined
  const segments = [...(turn.segments ?? []), ...(row.segments ?? [])]
  turn.segments = segments.length > 0 ? segments : undefined
  if (row.toolCalls) turn.toolCalls = [...(turn.toolCalls ?? []), ...row.toolCalls]
  if (row.images) turn.images = [...(turn.images ?? []), ...row.images]
  turn.tokensIn = (turn.tokensIn ?? 0) + (row.tokensIn ?? 0)
  turn.tokensOut = (turn.tokensOut ?? 0) + (row.tokensOut ?? 0)
  if (row.createdAt) turn.createdAt = row.createdAt
  if (row.error) {
    turn.error = row.error
    turn.errorSource = row.errorSource
  }
}

/**
 * Carry render keys from the transcript on screen to its refreshed copy.
 *
 * Rows keep their key when the id is unchanged. The rest — the turn that just
 * finished, still under local ids on screen and under server ids in `next` —
 * are paired from the newest end by role, so each keeps the key it was
 * rendered under. Without this every row of that turn remounts, the list's
 * per-index height cache no longer matches, and the viewport jumps.
 */
export function carryRenderKeys(next: ChatMessage[], prev: ChatMessage[]): ChatMessage[] {
  if (prev.length === 0 || next.length === 0) return next
  const keyOf = (m: ChatMessage) => m.key ?? m.id
  const prevById = new Map(prev.map((m) => [m.id, m]))
  const nextIds = new Set(next.map((m) => m.id))
  const out = next.map((m) => {
    const same = prevById.get(m.id)
    if (!same || keyOf(same) === m.id) return m
    return { ...m, key: keyOf(same) }
  })
  const spare = prev.filter((m) => !nextIds.has(m.id))
  let j = spare.length - 1
  for (let i = out.length - 1; i >= 0 && j >= 0; i--) {
    if (prevById.has(out[i].id)) continue
    if (out[i].role !== spare[j].role) break
    out[i] = { ...out[i], key: keyOf(spare[j]) }
    j--
  }
  return out
}

/**
 * Merge a freshly hydrated transcript with client-only error rows the caller
 * appended before hydration ran. A transport / HTTP / clean-EOF failure is
 * never persisted server-side, so a naive setMessages(hydrate) would erase
 * both the failure and the optimistic user prompt that produced it.
 *
 * For each orphan assistant error row (errorSource != 'server', id absent
 * from hydrated), we also carry the immediately-preceding prev user row when
 * that user row is itself absent from hydrated — i.e. the POST never landed
 * server-side, so the prompt only exists in local state. Retry then walks
 * backwards from the error to that user row exactly as it would from a
 * persisted turn. Pairs land at the tail; the error's retryPrompt (set at
 * creation time) is the source of truth for what Retry resends.
 */
export function mergeHydratedWithLocalErrors(
  hydrated: ChatMessage[],
  prev: ChatMessage[],
): ChatMessage[] {
  const seen = new Set(hydrated.map((m) => m.id))
  const carryover: ChatMessage[] = []
  for (let i = 0; i < prev.length; i++) {
    const m = prev[i]
    if (
      m.role !== 'assistant' ||
      m.error == null ||
      m.errorSource == null ||
      m.errorSource === 'server' ||
      seen.has(m.id)
    ) {
      continue
    }
    const priorUser = i > 0 ? prev[i - 1] : undefined
    if (priorUser && priorUser.role === 'user' && !seen.has(priorUser.id)) {
      carryover.push(priorUser)
      seen.add(priorUser.id)
    }
    carryover.push(m)
    seen.add(m.id)
  }
  if (carryover.length === 0) return hydrated
  return [...hydrated, ...carryover]
}
