import { useCallback, useEffect, useMemo, useRef } from 'react'
import { ApiError, get, streamGet, streamPost, type StreamEvent } from './api'
import {
  applyStreamEvent,
  createForegroundRun,
  createFrameBatcher,
  createStandingAttach,
  type EventCallbacks,
  type ForegroundRun,
  type FrameBatcher,
  type LiveStatus,
  type StreamEventLike,
  type UsageInfo,
} from './chatStreamController'
import {
  DEFAULT_MAX_LIVE_REASONING_CHARS,
  hydrate,
  carryRenderKeys,
  mergeHydratedWithLocalErrors,
  type ChatMessage,
  type SessionDetail,
} from './chatTranscript'

/**
 * Hook binding for the chat stream controller. It owns the RAF-backed frame
 * batcher and the standing-attach lifecycle, and exposes just what ChatPage
 * needs: an `applyEvent` for the foreground turn, `drain` for flushing at
 * end-of-turn, and `attach(sid, …)` for the reconnect loop.
 *
 * Callers pass the state setters. The hook never reads React state directly;
 * that keeps the controller unit-testable and stops a stale closure from
 * silently overwriting a fresh session.
 */
export interface UseChatStreamOptions {
  setMessages: (updater: (prev: ChatMessage[]) => ChatMessage[]) => void
  setLive: (updater: (s: LiveStatus) => LiveStatus) => void
  setAskId: (id: string | undefined) => void
  setTitle: (title: string) => void
  setUsage: (u: UsageInfo) => void
  setError: (message: string | undefined) => void
  setStreaming: (streaming: boolean) => void
  showReasoningRef: React.RefObject<boolean>
  maxLiveReasoningRef: React.RefObject<number>
  isForegroundActiveRef: React.RefObject<boolean>
  errorFallback: string
  authFailureMessage: string
  conversationFallback: string
}

export interface StartRunOptions {
  /** Optimistic assistant bubble id — the run applies every event to this id. */
  assistantId: string
  /** POST body handed to /chat. */
  body: unknown
  /** Resolves the current session id at each event (may change on adoption). */
  currentSessionId: () => string | undefined
  /** Called on server-assigned id / title — for URL adoption. */
  onSession?: (id: string, title?: string) => void
  /** End-of-turn `done` — clear composer/live/ask before hydrate fires. */
  onDone: () => void
  /** Transport error — clear streaming, surface message. */
  onError: (err: Error) => void
  /** Socket closed cleanly after `done` cleanup. */
  onFinal: () => void
  /** Fired exactly once after the run fully quiesces (including hydrate). */
  onSettled?: () => void
  /** Clean EOF without `done` — owners paint a terminal error. */
  onCleanEof?: () => void
  path?: string
}

export interface UseChatStream {
  applyEvent: (
    assistantId: string,
    event: StreamEvent,
    onSession?: (id: string, title?: string) => void,
  ) => void
  drain: () => void
  attach: (sid: string) => () => void
  /** Start a foreground /chat run. Callers hold the returned handle so a
   *  session switch (or error/done) can dispose it and silence late callbacks. */
  startRun: (opts: StartRunOptions) => ForegroundRun
}

export function useChatStream(opts: UseChatStreamOptions): UseChatStream {
  const optsRef = useRef(opts)
  optsRef.current = opts

  const batcher = useMemo<FrameBatcher>(
    () =>
      createFrameBatcher({
        schedule: {
          schedule: (fn) => requestAnimationFrame(fn),
          cancel: (h) => cancelAnimationFrame(h),
        },
        setMessages: (updater) => optsRef.current.setMessages(updater),
        maxLiveReasoningChars: () =>
          optsRef.current.maxLiveReasoningRef.current ?? DEFAULT_MAX_LIVE_REASONING_CHARS,
      }),
    [],
  )

  // Cancel a pending frame on unmount so the last delta never lingers.
  useEffect(() => () => batcher.dispose(), [batcher])

  const buildCallbacks = useCallback(
    (
      assistantId: string,
      onSession?: (id: string, title?: string) => void,
    ): EventCallbacks => ({
      patchAssistant: (fn) => batcher.enqueuePatch(assistantId, fn),
      appendDelta: (segment, delta) => batcher.enqueueDelta(assistantId, segment, delta),
      setLive: (updater) => optsRef.current.setLive(updater),
      setAskId: (id) => optsRef.current.setAskId(id),
      setUsage: (u) => optsRef.current.setUsage(u),
      setTitle: (title) => optsRef.current.setTitle(title),
      onSession,
      showReasoning: () => optsRef.current.showReasoningRef.current ?? true,
      errorMessage: () => optsRef.current.errorFallback,
    }),
    [batcher],
  )

  const applyEvent = useCallback(
    (assistantId: string, event: StreamEvent, onSession?: (id: string, title?: string) => void) => {
      applyStreamEvent(event as StreamEventLike, buildCallbacks(assistantId, onSession))
    },
    [buildCallbacks],
  )

  const attach = useCallback(
    (sid: string) => {
      const standing = createStandingAttach(sid, {
        isForegroundActive: () => optsRef.current.isForegroundActiveRef.current ?? false,
        ensureAssistant: () => {
          const id = `live_${Date.now()}_a`
          optsRef.current.setMessages((prev) => [
            ...prev,
            { id, role: 'assistant', content: '' },
          ])
          optsRef.current.setStreaming(true)
          optsRef.current.setLive(() => ({ turn: 1 }))
          return id
        },
        onDone: () => {
          batcher.drain()
          optsRef.current.setStreaming(false)
        },
        onSessionHydrated: (detail) => {
          // A same-session refresh must not erase client-only errors — a
          // transport/HTTP/EOF failure never round-trips through the server.
          const next = hydrate(detail)
          optsRef.current.setMessages((prev) =>
            carryRenderKeys(mergeHydratedWithLocalErrors(next, prev), prev),
          )
          optsRef.current.setTitle(detail.session.title || optsRef.current.conversationFallback)
        },
        onAuthFailure: () => {
          optsRef.current.setStreaming(false)
          optsRef.current.setError(optsRef.current.authFailureMessage)
        },
        onEvent: (id, event) => {
          applyStreamEvent(
            event,
            buildCallbacks(id, (_sessionId, title) => {
              if (title) optsRef.current.setTitle(title)
            }),
          )
        },
        fetchSession: (target) => get<SessionDetail>(`/sessions/${target}`),
        transport: (path, onEvent, onError) =>
          streamGet(path, onEvent as (e: StreamEvent) => void, (err) => onError(err)),
        setTimer: (fn, ms) => window.setTimeout(fn, ms),
        clearTimer: (h) => window.clearTimeout(h),
        isAuthError: (err) => err instanceof ApiError && err.status === 401,
      })
      return () => standing.dispose()
    },
    [batcher, buildCallbacks],
  )

  const startRun = useCallback(
    (runOpts: StartRunOptions): ForegroundRun => {
      return createForegroundRun({
        assistantId: runOpts.assistantId,
        path: runOpts.path ?? '/chat',
        body: runOpts.body,
        currentSessionId: runOpts.currentSessionId,
        // Session adoption dispatches exactly here — the reducer's own
        // session-case is intentionally left unwired below so a single
        // `session` event does not fire adoption twice (which turned every
        // brand-new-chat handshake into two navigate() calls).
        onSession: (id, title) => {
          if (title) optsRef.current.setTitle(title)
          runOpts.onSession?.(id, title)
        },
        drainFrames: () => batcher.drain(),
        onEvent: (event) => {
          // Reasoning burst / text delta / tool_* etc. — routed through the
          // same reducer + frame batcher as the standing attach so replays and
          // live turns render identically. NO onSession is passed: adoption
          // already fired above.
          applyStreamEvent(event, buildCallbacks(runOpts.assistantId, undefined))
        },
        onDone: () => runOpts.onDone(),
        onError: (err) => runOpts.onError(err),
        onFinal: () => runOpts.onFinal(),
        onCleanEof: () => runOpts.onCleanEof?.(),
        onSettled: () => runOpts.onSettled?.(),
        onSessionHydrated: (detail) => {
          const next = hydrate(detail)
          optsRef.current.setMessages((prev) =>
            carryRenderKeys(mergeHydratedWithLocalErrors(next, prev), prev),
          )
          optsRef.current.setTitle(detail.session.title || optsRef.current.conversationFallback)
        },
        fetchSession: (sid) => get<SessionDetail>(`/sessions/${sid}`),
        transport: (path, body, onEvent, onError, onDone) =>
          streamPost(path, body, onEvent as (e: StreamEvent) => void, onError, onDone),
      })
    },
    [batcher, buildCallbacks],
  )

  return { applyEvent, drain: batcher.drain, attach, startRun }
}
