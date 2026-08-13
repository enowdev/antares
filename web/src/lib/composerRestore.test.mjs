import { describe, expect, test } from 'bun:test'
import {
  baselineAfterSend,
  composerCanSend,
  ownershipResolutionAfterCompletion,
  restoreIsCurrent,
  sessionOpenIsCurrent,
  sessionTargetOwner,
  shouldAdoptDefaultTarget,
  stopStreamKind,
  targetAfterCursorHydration,
  targetChangeAllowed,
} from './composerRestore.ts'

const chatTarget = {
  kind: 'chat',
  provider: 'openai',
  model: 'gpt-5.6',
  name: 'GPT 5.6',
  providerLabel: 'OpenAI',
}

const otherChatTarget = { ...chatTarget, model: 'gpt-5.5', name: 'GPT 5.5' }

const cursorTarget = (id) => ({
  kind: 'cursor',
  model: { id, name: id, aliases: [], parameters: [], variants: [] },
  variant: { params: [], displayName: id },
})

describe('session-scoped restoration', () => {
  test('only the newest hydration may apply its result', () => {
    expect(restoreIsCurrent(4, 4)).toBe(true)
    // Session A's catalogue answer arriving after session B opened.
    expect(restoreIsCurrent(4, 5)).toBe(false)
  })
})

describe('automatic chat defaults', () => {
  test('a default never lands while a session is still hydrating', () => {
    expect(shouldAdoptDefaultTarget({ owner: 'pending', hasTarget: false })).toBe(false)
  })

  test('a default never replaces a restored Cursor target', () => {
    expect(shouldAdoptDefaultTarget({ owner: 'restored', hasTarget: true })).toBe(false)
    expect(shouldAdoptDefaultTarget({ owner: 'restored', hasTarget: false })).toBe(false)
  })

  test('a default fills an empty composer once hydration is done', () => {
    expect(shouldAdoptDefaultTarget({ owner: 'free', hasTarget: false })).toBe(true)
  })

  test('a default never overwrites a target that is already chosen', () => {
    expect(shouldAdoptDefaultTarget({ owner: 'free', hasTarget: true })).toBe(false)
  })
})

describe('ownership derived from the exact route-open occurrence', () => {
  const opened = (sessionId, routerKey) => ({
    sessionId,
    // The object identity is the epoch. routerKey is diagnostic only: browser
    // POP may emit a fresh open occurrence for the same history entry/key.
    epoch: { routerKey },
  })
  const unresolved = { open: null, owner: 'free' }

  test('an existing session is pending on initial load before effects run', () => {
    const initialA = opened('A', 'default')
    const owner = sessionTargetOwner({ open: initialA, resolved: unresolved })
    expect(owner).toBe('pending')
    expect(composerCanSend({ owner, streaming: false })).toBe(false)
  })

  test('a normal A to B navigation is pending before B hydration runs', () => {
    const openA = opened('A', 'a-entry')
    const openB = opened('B', 'b-entry')
    expect(
      sessionTargetOwner({
        open: openB,
        resolved: { open: openA, owner: 'restored' },
      }),
    ).toBe('pending')
    expect(sessionOpenIsCurrent(openA, openB)).toBe(false)
  })

  test('B1 to A to B2 stays pending even though both B visits have the same id', () => {
    const openB1 = opened('B', 'b-entry')
    const openA = opened('A', 'a-entry')
    // A POP can reopen the same router entry, so even the diagnostic key may
    // repeat; the emitted location object still gives B2 a distinct epoch.
    const openB2 = opened('B', 'b-entry')
    const resolvedB1 = { open: openB1, owner: 'restored' }

    expect(sessionTargetOwner({ open: openA, resolved: resolvedB1 })).toBe('pending')
    expect(sessionTargetOwner({ open: openB2, resolved: resolvedB1 })).toBe('pending')
  })

  test('stale B1 and intervening A completions cannot resolve or mutate B2', () => {
    const openB1 = opened('B', 'b-entry')
    const openA = opened('A', 'a-entry')
    const openB2 = opened('B', 'b-entry')
    const resolvedB2 = { open: openB2, owner: 'restored' }

    expect(sessionOpenIsCurrent(openB1, openB2)).toBe(false)
    expect(sessionOpenIsCurrent(openA, openB2)).toBe(false)
    expect(
      ownershipResolutionAfterCompletion({
        current: openB2,
        previous: resolvedB2,
        completed: openB1,
        owner: 'free',
      }),
    ).toBe(resolvedB2)
    expect(
      ownershipResolutionAfterCompletion({
        current: openB2,
        previous: resolvedB2,
        completed: openA,
        owner: 'free',
      }),
    ).toBe(resolvedB2)
  })

  test('only the current B2 completion resolves B2', () => {
    const openB2 = opened('B', 'b-entry')
    const resolvedB2 = ownershipResolutionAfterCompletion({
      current: openB2,
      previous: unresolved,
      completed: openB2,
      owner: 'restored',
    })
    expect(sessionOpenIsCurrent(openB2, openB2)).toBe(true)
    expect(resolvedB2).toEqual({ open: openB2, owner: 'restored' })
    expect(
      sessionTargetOwner({
        open: openB2,
        resolved: resolvedB2,
      }),
    ).toBe('restored')
    expect(
      sessionTargetOwner({
        open: openB2,
        resolved: { open: openB2, owner: 'free' },
      }),
    ).toBe('free')
  })

  test('a no-session new chat remains free despite a stale resolution', () => {
    const newChat = opened('', 'new-chat')
    const staleA = opened('A', 'a-entry')
    expect(sessionTargetOwner({ open: newChat, resolved: unresolved })).toBe('free')
    expect(
      sessionTargetOwner({
        open: newChat,
        resolved: { open: staleA, owner: 'restored' },
      }),
    ).toBe('free')
  })

  test('mid-stream server-id adoption waits for the adopted route occurrence', () => {
    const newChat = opened('', 'draft-entry')
    const preNavigationAdoption = {
      open: { sessionId: 'B', epoch: newChat.epoch },
      owner: 'restored',
    }
    const adoptedB = opened('B', 'assigned-entry')

    expect(sessionTargetOwner({ open: newChat, resolved: unresolved })).toBe('free')
    expect(
      sessionTargetOwner({ open: adoptedB, resolved: preNavigationAdoption }),
    ).toBe('pending')
    expect(sessionOpenIsCurrent(preNavigationAdoption.open, adoptedB)).toBe(false)
    expect(
      sessionTargetOwner({
        open: adoptedB,
        resolved: { open: adoptedB, owner: 'restored' },
      }),
    ).toBe('restored')
  })
})

describe('sending while a session is still hydrating', () => {
  test('a session whose target is not yet known cannot submit at all', () => {
    // Both the send button and Enter go through this gate, so neither route
    // can post a Cursor conversation's turn to /chat.
    expect(composerCanSend({ owner: 'pending', streaming: false })).toBe(false)
  })

  test('a resolved session may submit', () => {
    expect(composerCanSend({ owner: 'free', streaming: false })).toBe(true)
    expect(composerCanSend({ owner: 'restored', streaming: false })).toBe(true)
  })

  test('a streaming turn still blocks a second submit', () => {
    expect(composerCanSend({ owner: 'free', streaming: true })).toBe(false)
    expect(composerCanSend({ owner: 'restored', streaming: true })).toBe(false)
  })
})

describe('the target a hydrated session should hold', () => {
  test('an active Cursor session keeps the composer while its exact variant loads', () => {
    expect(
      targetAfterCursorHydration({
        active: true,
        modelId: 'gpt-5.6-sol',
        current: cursorTarget('gpt-5.6-sol'),
        pendingDefault: chatTarget,
        lastChat: chatTarget,
      }),
    ).toEqual({ owner: 'restored', action: 'keep' })
  })

  test('a Cursor target from another session is dropped before its replacement loads', () => {
    expect(
      targetAfterCursorHydration({
        active: true,
        modelId: 'claude-opus-5',
        current: cursorTarget('gpt-5.6-sol'),
        pendingDefault: null,
        lastChat: null,
      }),
    ).toEqual({ owner: 'restored', action: 'set', target: null })
  })

  test('an ordinary session replaces a leftover Cursor target with a chat one', () => {
    expect(
      targetAfterCursorHydration({
        active: false,
        modelId: undefined,
        current: cursorTarget('gpt-5.6-sol'),
        pendingDefault: chatTarget,
        lastChat: otherChatTarget,
      }),
    ).toEqual({ owner: 'free', action: 'set', target: chatTarget })
  })

  test('the last chat target is used when no default has arrived yet', () => {
    expect(
      targetAfterCursorHydration({
        active: false,
        modelId: undefined,
        current: cursorTarget('gpt-5.6-sol'),
        pendingDefault: null,
        lastChat: otherChatTarget,
      }),
    ).toEqual({ owner: 'free', action: 'set', target: otherChatTarget })
  })

  test('with no chat target known the Cursor target is still cleared', () => {
    expect(
      targetAfterCursorHydration({
        active: false,
        modelId: undefined,
        current: cursorTarget('gpt-5.6-sol'),
        pendingDefault: null,
        lastChat: null,
      }),
    ).toEqual({ owner: 'free', action: 'set', target: null })
  })

  test('durable state that names no decodable model does not keep Cursor selected', () => {
    expect(
      targetAfterCursorHydration({
        active: true,
        modelId: '',
        current: cursorTarget('gpt-5.6-sol'),
        pendingDefault: chatTarget,
        lastChat: null,
      }),
    ).toEqual({ owner: 'free', action: 'set', target: chatTarget })
  })

  test('an ordinary session leaves an existing chat target alone', () => {
    expect(
      targetAfterCursorHydration({
        active: false,
        modelId: undefined,
        current: chatTarget,
        pendingDefault: otherChatTarget,
        lastChat: otherChatTarget,
      }),
    ).toEqual({ owner: 'free', action: 'keep' })
  })

  test('an ordinary session installs a chat target when the composer holds none', () => {
    // The session switch already cleared the previous Cursor target, so there
    // is nothing left to replace — the fallback still has to be installed.
    expect(
      targetAfterCursorHydration({
        active: false,
        modelId: undefined,
        current: null,
        pendingDefault: chatTarget,
        lastChat: otherChatTarget,
      }),
    ).toEqual({ owner: 'free', action: 'set', target: chatTarget })
    expect(
      targetAfterCursorHydration({
        active: false,
        modelId: undefined,
        current: null,
        pendingDefault: null,
        lastChat: otherChatTarget,
      }),
    ).toEqual({ owner: 'free', action: 'set', target: otherChatTarget })
  })

  test('an undecodable selection installs a chat target over an empty composer', () => {
    expect(
      targetAfterCursorHydration({
        active: true,
        modelId: '',
        current: null,
        pendingDefault: chatTarget,
        lastChat: null,
      }),
    ).toEqual({ owner: 'free', action: 'set', target: chatTarget })
  })

  test('an empty composer with nothing to fall back on stays empty', () => {
    expect(
      targetAfterCursorHydration({
        active: false,
        modelId: undefined,
        current: null,
        pendingDefault: null,
        lastChat: null,
      }),
    ).toEqual({ owner: 'free', action: 'keep' })
  })

  test('a choice made after hydration began is never overwritten', () => {
    const chosen = cursorTarget('claude-opus-5')
    expect(
      targetAfterCursorHydration({
        active: false,
        modelId: undefined,
        current: chosen,
        pendingDefault: chatTarget,
        lastChat: chatTarget,
        userChose: true,
      }),
    ).toEqual({ owner: 'free', action: 'keep' })
    expect(
      targetAfterCursorHydration({
        active: true,
        modelId: 'gpt-5.6-sol',
        current: chatTarget,
        pendingDefault: null,
        lastChat: chatTarget,
        userChose: true,
      }),
    ).toEqual({ owner: 'free', action: 'keep' })
  })
})

describe('stop semantics', () => {
  test('the stream that was started decides, not the picker', () => {
    expect(stopStreamKind('cursor', false)).toBe('cursor')
    expect(stopStreamKind('chat', true)).toBe('chat')
  })

  test('an attached run falls back to the session durable target', () => {
    expect(stopStreamKind(null, true)).toBe('cursor')
    expect(stopStreamKind(null, false)).toBe('chat')
  })

  test('the target cannot be changed while a turn is streaming', () => {
    expect(targetChangeAllowed(true)).toBe(false)
    expect(targetChangeAllowed(false)).toBe(true)
  })
})

describe('follow-up baseline', () => {
  const options = { model: { id: 'sol' }, variant: { params: [] }, mode: 'agent' }
  const previous = { options: { model: { id: 'opus' }, variant: { params: [] } }, reuseValid: true }

  test('a refused request never becomes the run a follow-up would continue', () => {
    expect(baselineAfterSend({ previous, attempted: options, accepted: false })).toBe(previous)
    expect(baselineAfterSend({ previous: null, attempted: options, accepted: false })).toBeNull()
  })

  test('an accepted stream adopts what it was sent with', () => {
    expect(baselineAfterSend({ previous, attempted: options, accepted: true })).toEqual({
      options,
      reuseValid: true,
    })
  })
})
