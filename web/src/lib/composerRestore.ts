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
  const { active, modelId, current, pendingDefault, lastChat } = input
  if (active && modelId) {
    // A restore is on its way for this session's own model.
    if (current?.kind === 'cursor' && current.model.id !== modelId) {
      return { owner: 'restored', action: 'set', target: null }
    }
    return { owner: 'restored', action: 'keep' }
  }
  // This session does not run on Cursor, or names nothing exact enough to run.
  if (current?.kind === 'cursor') {
    return { owner: 'free', action: 'set', target: pendingDefault ?? lastChat ?? null }
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
