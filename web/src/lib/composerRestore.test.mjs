import { describe, expect, test } from 'bun:test'
import {
  baselineAfterSend,
  composerCanSend,
  restoreIsCurrent,
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
