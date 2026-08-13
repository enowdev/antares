/**
 * The composer's restoration decisions, kept away from React so the rules that
 * protect a send from targeting the wrong place are testable on their own.
 *
 * Three asynchronous sources race for one execution target: the picker's
 * mount-time active-model lookup, a session's durable Cursor state, and the
 * user. Only the user always wins; the other two are ordered by which session
 * is open and whether that session's state has been read yet.
 */

import type { ChatTarget, ComposerTarget, CursorOptionsValue, CursorRunBaseline } from '@/lib/composerTargets'

/**
 * Who owns the composer's target right now:
 * - `pending`: a session is being hydrated and its state has the final say;
 * - `restored`: durable state named the target, so no default may replace it;
 * - `free`: nothing owns it, so an automatic chat default may fill it.
 */
export type TargetOwner = 'pending' | 'restored' | 'free'

/** Whether an asynchronous restoration still belongs to the open session. */
export function restoreIsCurrent(captured: number, current: number): boolean {
  return captured === current
}

/** The session ownership was last resolved for, and what it resolved to. */
export interface SessionOwnershipResolution {
  sessionId: string | null
  owner: TargetOwner
}

/**
 * Who owns the target for the session currently open, derived rather than
 * remembered. An existing conversation is pending until something has resolved
 * ownership *for that exact id*, which is true on its first render, across a
 * route change, and while a slower answer for the previous session is still in
 * flight. A new chat owns itself, so it is never blocked.
 */
export function sessionTargetOwner(input: {
  sessionId?: string
  resolved: SessionOwnershipResolution
}): TargetOwner {
  const sessionId = input.sessionId ?? ''
  if (!sessionId) return 'free'
  if (input.resolved.sessionId !== sessionId) return 'pending'
  return input.resolved.owner
}

/**
 * Whether the picker's automatic active-model default may be adopted. It never
 * competes with a session's own state, and never replaces a chosen target.
 */
export function shouldAdoptDefaultTarget(state: {
  owner: TargetOwner
  hasTarget: boolean
}): boolean {
  return state.owner === 'free' && !state.hasTarget
}

/**
 * Whether the composer may submit. A session whose target is still being
 * resolved has no answer to "where does this go?", and guessing would post a
 * Cursor conversation's turn to the chat model instead.
 */
export function composerCanSend(state: {
  owner: TargetOwner
  streaming: boolean
}): boolean {
  return state.owner !== 'pending' && !state.streaming
}

export interface HydrationTargetInput {
  /** Whether durable state still points this session at Cursor. */
  active: boolean
  /** The model durable state names, if it names a usable one. */
  modelId?: string
  current: ComposerTarget | null
  /** A picker default that arrived while the session was hydrating. */
  pendingDefault: ChatTarget | null
  /** The last chat target this tab used, if any. */
  lastChat: ChatTarget | null
  /** Whether the user picked the current target after hydration began. */
  userChose?: boolean
}

export type HydrationTargetDecision =
  | { owner: TargetOwner; action: 'keep' }
  | { owner: TargetOwner; action: 'set'; target: ComposerTarget | null }

/**
 * What the composer's target should become when a session's durable state
 * arrives. A Cursor target left over from another conversation is dropped
 * before the replacement loads: sending in that window must never reach a
 * Cursor model this session never used.
 */
export function targetAfterCursorHydration(
  input: HydrationTargetInput,
): HydrationTargetDecision {
  const { active, modelId, current, pendingDefault, lastChat, userChose } = input
  // Someone chose deliberately while the session was loading; that outranks
  // anything the session itself would have restored.
  if (userChose) return { owner: 'free', action: 'keep' }
  if (active && modelId) {
    // A restore is on its way for this session's own model.
    if (current?.kind === 'cursor' && current.model.id !== modelId) {
      return { owner: 'restored', action: 'set', target: null }
    }
    return { owner: 'restored', action: 'keep' }
  }
  // This session does not run on Cursor, or names nothing exact enough to run.
  // Its Cursor target goes, and the composer needs a chat target to fall back
  // to — including when the session switch already emptied it.
  const fallback = pendingDefault ?? lastChat ?? null
  if (current?.kind === 'cursor') {
    return { owner: 'free', action: 'set', target: fallback }
  }
  if (current === null && fallback) {
    return { owner: 'free', action: 'set', target: fallback }
  }
  return { owner: 'free', action: 'keep' }
}

/**
 * Which semantics Stop must use. The stream that is actually running decides —
 * the picker may have moved on since it started — and an attached run falls
 * back to what the session's durable state says it is.
 */
export function stopStreamKind(
  started: 'chat' | 'cursor' | null,
  cursorActive: boolean,
): 'chat' | 'cursor' {
  return started ?? (cursorActive ? 'cursor' : 'chat')
}

/** The target may only change while nothing is streaming. */
export function targetChangeAllowed(streaming: boolean): boolean {
  return !streaming
}

/**
 * The run a follow-up would continue after a send attempt. A request the server
 * refused (busy session, rate limit, auth, stale model) started nothing, so the
 * previous baseline stands and the new-agent warning stays truthful.
 */
export function baselineAfterSend(input: {
  previous: CursorRunBaseline | null
  attempted: CursorOptionsValue
  accepted: boolean
}): CursorRunBaseline | null {
  return input.accepted
    ? { options: input.attempted, reuseValid: true }
    : input.previous
}
