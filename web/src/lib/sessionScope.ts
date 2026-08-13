/**
 * Scoping asynchronous chat work to the route opening that started it.
 *
 * A session id is not an opening of that session: leaving conversation B for A
 * and returning to B gives two openings that share every textual identifier.
 * Work in flight from the first one — a pending-approval response, an attach
 * frame, a retry timer, an end-of-turn history refresh, a cleanup callback —
 * must therefore be judged by the occurrence it belongs to, at the moment it
 * tries to write, rather than by the session id it names, by an `alive` flag
 * that effect cleanup flips later, or by a counter another opening can reuse.
 */

import { sessionOpenIsCurrent, type SessionOpenOccurrence } from './composerRestore'

export interface SessionScope {
  /** The route opening this work was started for. */
  readonly open: SessionOpenOccurrence
  /** Whether writes are still allowed: same opening, and not released. */
  isCurrent(): boolean
  /** End the scope without a navigation — effect cleanup, or unmount. */
  release(): void
  /** Perform a state write only while the scope holds. Reports whether it ran. */
  run(write: () => void): boolean
  /** Wrap a deferred callback (promise, stream, timer) in the same guard. */
  guard<A extends unknown[]>(callback: (...args: A) => void): (...args: A) => void
  /**
   * A scope for one operation inside this one, such as a standing attachment.
   * It ends when the operation is closed or when this scope ends, so a single
   * check covers both the route moving on and the work being torn down.
   */
  derive(): SessionScope
}

/**
 * Scope work to `open`, judged against whatever route opening is current when
 * a callback finally runs. `currentOpen` is read at call time on purpose: the
 * whole point is that the answer changes while the work is in flight.
 */
export function createSessionScope(
  open: SessionOpenOccurrence,
  currentOpen: () => SessionOpenOccurrence,
): SessionScope {
  return scopeWhile(open, () => sessionOpenIsCurrent(open, currentOpen()))
}

function scopeWhile(open: SessionOpenOccurrence, holds: () => boolean): SessionScope {
  let released = false
  const isCurrent = () => !released && holds()
  const run = (write: () => void): boolean => {
    if (!isCurrent()) return false
    write()
    return true
  }
  return {
    open,
    isCurrent,
    release: () => {
      released = true
    },
    run,
    guard:
      <A extends unknown[]>(callback: (...args: A) => void) =>
      (...args: A) => {
        run(() => callback(...args))
      },
    derive: () => scopeWhile(open, isCurrent),
  }
}
