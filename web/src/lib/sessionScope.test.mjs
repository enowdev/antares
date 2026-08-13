import { describe, expect, test } from 'bun:test'
import { createSessionScope } from './sessionScope.ts'
import { mergeApprovals, pendingApprovalsForSession, shouldReconnectAttach } from './chatEvents.ts'

/**
 * A stand-in for the router the chat page reads. The occurrence identity is the
 * emitted location object: a rerender keeps it, and every navigation mints a new
 * one — including a return to a session id that was already visited.
 */
function navigation(sessionId) {
  let current = { sessionId, epoch: {} }
  return {
    current: () => current,
    open(next) {
      current = { sessionId: next, epoch: {} }
      return current
    },
    rerender: () => ({ sessionId: current.sessionId, epoch: current.epoch }),
  }
}

/** The chat state a deferred callback could reach. */
const chatState = () => ({
  approvals: [],
  messages: [],
  title: '',
  streaming: false,
  error: null,
  cursorState: null,
  reconnects: 0,
})

const pendingFor = (sessionId, id) => ({
  id,
  session_id: sessionId,
  tool: 'shell',
  arguments: '{"cmd":"ls"}',
  message: '',
})

/** The `/approvals` response landing, as the approval refresh applies it. */
const landApprovals = (scope, state, sessionId, pending) =>
  scope.run(() => {
    state.approvals = pendingApprovalsForSession(state.approvals, pending, sessionId)
  })

/** One attach frame landing, as the stream handler applies an approval event. */
const landFrame = (scope, state, view) =>
  scope.run(() => {
    state.streaming = true
    state.messages = [...state.messages, { id: 'live_a', role: 'assistant' }]
    state.approvals = mergeApprovals(state.approvals, view)
  })

/** The end-of-turn history refresh landing. */
const landRefresh = (scope, state, detail) =>
  scope.run(() => {
    state.messages = detail.messages
    state.title = detail.title
    state.cursorState = detail.cursorState
  })

/** The attach error handler landing. */
const landAttachError = (scope, state, message) =>
  scope.run(() => {
    state.streaming = false
    state.error = message
  })

describe('scoping work to one route-open occurrence', () => {
  test('an ordinary rerender keeps work started by the open route alive', () => {
    const nav = navigation('A')
    const scope = createSessionScope(nav.current(), nav.current)
    // The chat page rebuilds the occurrence object on every render; only the
    // epoch it carries decides identity.
    nav.rerender()
    expect(scope.isCurrent()).toBe(true)
    expect(scope.run(() => {})).toBe(true)
  })

  test('work started by the previous route opening is stale', () => {
    const nav = navigation('A')
    const scopeA = createSessionScope(nav.current(), nav.current)
    nav.open('B')
    expect(scopeA.isCurrent()).toBe(false)
    expect(scopeA.run(() => {})).toBe(false)
  })

  test('B1 and A stay stale under B2 even though B1 and B2 name one session', () => {
    const nav = navigation('B')
    const b1 = nav.current()
    const scopeB1 = createSessionScope(b1, nav.current)
    const scopeA = createSessionScope(nav.open('A'), nav.current)
    const b2 = nav.open('B')

    // A session id alone cannot tell B1 from B2, which is the whole problem.
    expect(b1.sessionId).toBe(b2.sessionId)
    expect(scopeB1.isCurrent()).toBe(false)
    expect(scopeA.isCurrent()).toBe(false)
    expect(createSessionScope(b2, nav.current).isCurrent()).toBe(true)
  })

  test('a released scope writes nothing even while its occurrence is open', () => {
    const nav = navigation('A')
    const scope = createSessionScope(nav.current(), nav.current)
    // Effect cleanup and unmount both end the work without a navigation.
    scope.release()
    expect(scope.isCurrent()).toBe(false)
    expect(scope.run(() => {})).toBe(false)
  })

  test('a derived scope ends with its parent but never ends the parent', () => {
    const nav = navigation('A')
    const scope = createSessionScope(nav.current(), nav.current)
    const loop = scope.derive()
    loop.release()
    expect(loop.isCurrent()).toBe(false)
    expect(scope.isCurrent()).toBe(true)

    const other = scope.derive()
    scope.release()
    expect(other.isCurrent()).toBe(false)
  })

  test('a guarded callback is judged when it fires, not when it was wrapped', () => {
    const nav = navigation('A')
    const scope = createSessionScope(nav.current(), nav.current)
    const seen = []
    const deferred = scope.guard((value) => seen.push(value))

    deferred('while open')
    nav.open('B')
    deferred('after navigating')
    expect(seen).toEqual(['while open'])
  })
})

describe('deferred B1 and A work cannot mutate B2', () => {
  /** B1 opens, A intervenes, B2 opens; every scope is captured on the way. */
  const b1ToAToB2 = () => {
    const nav = navigation('B')
    const scopeB1 = createSessionScope(nav.current(), nav.current)
    const scopeA = createSessionScope(nav.open('A'), nav.current)
    const scopeB2 = createSessionScope(nav.open('B'), nav.current)
    return { nav, scopeB1, scopeA, scopeB2 }
  }

  test('a pending-approval response from B1 or A never reaches B2', () => {
    const { scopeB1, scopeA, scopeB2 } = b1ToAToB2()
    const state = chatState()
    landApprovals(scopeB2, state, 'B', [pendingFor('B', 'live')])
    const settled = state.approvals

    // B1 asked for the same session, so a session-id filter would let it in.
    expect(landApprovals(scopeB1, state, 'B', [pendingFor('B', 'stale-b1')])).toBe(false)
    expect(landApprovals(scopeA, state, 'A', [pendingFor('A', 'stale-a')])).toBe(false)
    expect(state.approvals).toBe(settled)
    expect(settled.map((a) => a.id)).toEqual(['live'])
  })

  test("B2's own approval refresh and cancel poll still merge", () => {
    const { scopeB2 } = b1ToAToB2()
    const state = chatState()
    expect(landApprovals(scopeB2, state, 'B', [pendingFor('B', 'first')])).toBe(true)
    // The cancel poll runs the same refresh every two seconds while the server
    // holds the request open.
    expect(landApprovals(scopeB2, state, 'B', [pendingFor('B', 'first'), pendingFor('B', 'second')])).toBe(true)
    expect(state.approvals.map((a) => a.id)).toEqual(['first', 'second'])
  })

  test('an SSE frame from B1 cannot append to B2 or raise its streaming flag', () => {
    const { scopeB1, scopeB2 } = b1ToAToB2()
    const state = chatState()
    const frame = { id: 'appr_1', tool: 'shell', arguments: '{}', message: '' }

    expect(landFrame(scopeB1.derive(), state, frame)).toBe(false)
    expect(state.messages).toEqual([])
    expect(state.streaming).toBe(false)
    expect(state.approvals).toEqual([])
    // B2's own attachment still renders.
    expect(landFrame(scopeB2.derive(), state, frame)).toBe(true)
    expect(state.messages).toHaveLength(1)
    expect(state.streaming).toBe(true)
  })

  test("an attach error from B1 cannot set B2's error or clear its streaming", () => {
    const { scopeB1, scopeB2 } = b1ToAToB2()
    const state = chatState()
    landFrame(scopeB2.derive(), state, { id: 'a', tool: 't', arguments: '{}', message: '' })

    expect(landAttachError(scopeB1.derive(), state, 'login expired')).toBe(false)
    expect(state.error).toBeNull()
    expect(state.streaming).toBe(true)
  })

  test('a retry timer from B1 cannot restart a reconnect loop over B2', () => {
    const { scopeB1, scopeB2 } = b1ToAToB2()
    const state = chatState()
    const loopB1 = scopeB1.derive()
    const loopB2 = scopeB2.derive()
    const connect = (loop) => {
      if (!shouldReconnectAttach({ alive: loop.isCurrent(), detached: false })) return
      state.reconnects += 1
    }

    scopeB1.guard(() => connect(loopB1))()
    expect(state.reconnects).toBe(0)
    // B2 keeps its standing attachment.
    scopeB2.guard(() => connect(loopB2))()
    expect(state.reconnects).toBe(1)
    // A local Cursor Stop still holds B2's own loop open but idle.
    expect(shouldReconnectAttach({ alive: loopB2.isCurrent(), detached: true })).toBe(false)
    expect(shouldReconnectAttach({ alive: loopB2.isCurrent(), detached: false })).toBe(true)
  })

  test("a final history refresh from B1 cannot rewrite B2's transcript", () => {
    const { scopeB1, scopeB2 } = b1ToAToB2()
    const state = chatState()
    landRefresh(scopeB2.derive(), state, {
      messages: [{ id: 'b2_1' }],
      title: 'B2',
      cursorState: { active: true },
    })

    expect(
      landRefresh(scopeB1.derive(), state, {
        messages: [{ id: 'b1_1' }],
        title: 'B1',
        cursorState: { active: false },
      }),
    ).toBe(false)
    expect(state.messages).toEqual([{ id: 'b2_1' }])
    expect(state.title).toBe('B2')
    expect(state.cursorState).toEqual({ active: true })
  })

  test("a cleanup or finally callback from B1 cannot touch B2's run state", () => {
    const { scopeB1, scopeB2 } = b1ToAToB2()
    const state = chatState()
    landFrame(scopeB2.derive(), state, { id: 'a', tool: 't', arguments: '{}', message: '' })
    const loopB1 = scopeB1.derive()

    // The attach's `finally` both stops the spinner and schedules the next
    // connection; neither may happen for a route that has been left.
    const cleanup = scopeB1.guard(() => {
      state.streaming = false
      state.reconnects += 1
    })
    loopB1.release()
    cleanup()
    expect(state.streaming).toBe(true)
    expect(state.reconnects).toBe(0)
  })

  test('a new chat that adopts a server id leaves pre-adoption work stale', () => {
    const nav = navigation('')
    const draft = createSessionScope(nav.current(), nav.current)
    expect(draft.isCurrent()).toBe(true)
    // The turn streams on, and the page navigates to the id the server assigned.
    nav.open('B')
    expect(draft.isCurrent()).toBe(false)
    expect(createSessionScope(nav.current(), nav.current).isCurrent()).toBe(true)
  })
})
