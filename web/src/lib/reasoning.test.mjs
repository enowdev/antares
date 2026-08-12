import { describe, expect, test } from 'bun:test'
import {
  loadReasoningPreference,
  resolveReasoningControl,
  resolveReasoningModelTarget,
  reasoningOptions,
  reasoningPreferenceKey,
  saveReasoningPreference,
} from './reasoning.ts'
import {
  createReasoningCapabilityLoader,
  createReasoningCapabilityScheduler,
} from './reasoningCapability.ts'

describe('reasoning options', () => {
  test('preserve opaque values and mark only explicit disable', () => {
    const cap = {
      values: [
        { value: 'none', label: 'Off', kind: 'disable' },
        { value: 'extra-high', label: 'Extra High' },
      ],
      default: 'extra-high',
      mandatory: false,
      can_disable: true,
      source: 'live',
    }

    expect(reasoningOptions(cap).map((x) => x.value)).toEqual(['', 'none', 'extra-high'])
  })

  test('offer only Auto when a model has no capability metadata', () => {
    expect(reasoningOptions(undefined).map((x) => x.value)).toEqual([''])
  })

  test('omit explicit disable values for mandatory models', () => {
    const cap = {
      values: [
        { value: 'none', label: 'Off', kind: 'disable' },
        { value: 'MiXeD', label: 'Mixed' },
      ],
      mandatory: true,
      can_disable: false,
      source: 'static',
    }

    expect(reasoningOptions(cap).map((x) => x.value)).toEqual(['', 'MiXeD'])
  })
})

describe('reasoning preferences', () => {
  test('migrate a legacy preference once only when valid', () => {
    const storage = memoryStorage({ 'antares:reasoning': 'high' })
    const cap = capability(['low', 'high'])

    expect(loadReasoningPreference(storage, 'openai', 'gpt-5', cap)).toEqual({
      value: 'high',
      migrated: true,
    })
    expect(storage.getItem('antares:reasoning')).toBeNull()
    expect(storage.getItem(reasoningPreferenceKey('openai', 'gpt-5'))).toBe('high')
  })

  test('remove an invalid legacy preference after its single migration attempt', () => {
    const storage = memoryStorage({ 'antares:reasoning': 'HIGH' })

    expect(loadReasoningPreference(storage, 'openai', 'gpt-5', capability(['high']))).toEqual({
      value: '',
      migrated: false,
    })
    expect(storage.getItem('antares:reasoning')).toBeNull()
    expect(storage.getItem(reasoningPreferenceKey('openai', 'gpt-5'))).toBeNull()
  })

  test('sanitize an invalid scoped value without changing case', () => {
    const key = reasoningPreferenceKey('openai', 'gpt-5')
    const storage = memoryStorage({ [key]: 'HIGH' })

    expect(loadReasoningPreference(storage, 'openai', 'gpt-5', capability(['high']))).toEqual({
      value: '',
      migrated: false,
    })
    expect(storage.getItem(key)).toBeNull()
  })

  test('preserve a scoped opaque value while capability metadata is unknown', () => {
    const key = reasoningPreferenceKey('openai', 'gpt-5')
    const storage = memoryStorage({
      [key]: 'MiXeD',
      'antares:reasoning': 'high',
    })

    expect(loadReasoningPreference(storage, 'openai', 'gpt-5', undefined)).toEqual({
      value: '',
      migrated: false,
    })
    expect(storage.getItem(key)).toBe('MiXeD')
    expect(storage.getItem('antares:reasoning')).toBeNull()

    expect(loadReasoningPreference(storage, 'openai', 'gpt-5', capability(['MiXeD']))).toEqual({
      value: 'MiXeD',
      migrated: false,
    })
  })

  test('isolate encoded provider and model storage keys', () => {
    const storage = memoryStorage()
    const first = reasoningPreferenceKey('provider/one', 'model:alpha')
    const second = reasoningPreferenceKey('provider', 'one/model:alpha')

    expect(first).toBe('antares:reasoning:v2:provider%2Fone:model%3Aalpha')
    expect(second).toBe('antares:reasoning:v2:provider:one%2Fmodel%3Aalpha')
    expect(first).not.toBe(second)

    saveReasoningPreference(storage, 'provider/one', 'model:alpha', 'MiXeD')
    saveReasoningPreference(storage, 'provider', 'one/model:alpha', 'extra-high')

    expect(storage.getItem(first)).toBe('MiXeD')
    expect(storage.getItem(second)).toBe('extra-high')
  })

  test('store Auto as no explicit scoped override', () => {
    const key = reasoningPreferenceKey('openai', 'gpt-5')
    const storage = memoryStorage({ [key]: 'high' })

    saveReasoningPreference(storage, 'openai', 'gpt-5', '')

    expect(storage.getItem(key)).toBeNull()
  })
})

describe('reasoning capability state', () => {
  test('preserve the current opaque value while metadata is loading or unavailable', () => {
    for (const status of ['loading', 'unavailable']) {
      expect(resolveReasoningControl('MiXeD', { status })).toEqual({
        options: [
          { value: '', label: 'Auto' },
          { value: 'MiXeD', label: 'MiXeD' },
        ],
        unsupported: false,
        hint: status,
      })
    }
  })

  test('mark a value unsupported only after an authoritative result', () => {
    expect(resolveReasoningControl('legacy', { status: 'ready' })).toEqual({
      options: [{ value: '', label: 'Auto' }],
      unsupported: true,
      hint: 'unsupported',
    })
    expect(resolveReasoningControl('MiXeD', {
      status: 'ready',
      capability: capability(['MiXeD']),
    })).toEqual({
      options: [
        { value: '', label: 'Auto' },
        { value: 'MiXeD', label: 'MiXeD' },
      ],
      unsupported: false,
      hint: 'auto',
    })
  })
})

describe('reasoning model targets', () => {
  test('preserve aggregator model ids unless the prefix is a configured provider', () => {
    const active = { provider: 'openrouter', model: 'anthropic/claude-sonnet' }
    const providers = ['openrouter', 'openai']

    expect(resolveReasoningModelTarget('', active, providers)).toEqual(active)
    expect(resolveReasoningModelTarget('anthropic/claude-opus', active, providers)).toEqual({
      provider: 'openrouter',
      model: 'anthropic/claude-opus',
    })
    expect(resolveReasoningModelTarget('openai/gpt-5', active, providers)).toEqual({
      provider: 'openai',
      model: 'gpt-5',
    })
  })
})

describe('targeted reasoning capability loader', () => {
  test('deduplicates concurrent lookups and caches authoritative results', async () => {
    let calls = 0
    let resolveInfo
    const pending = new Promise((resolve) => {
      resolveInfo = resolve
    })
    const load = createReasoningCapabilityLoader(() => {
      calls++
      return pending
    })
    const target = { provider: 'openrouter', model: 'anthropic/claude-opus' }

    const first = load(target)
    const second = load({ ...target })
    expect(calls).toBe(1)

    const cap = capability(['MiXeD'])
    resolveInfo({ found: true, reasoning_capability: cap })
    expect(await first).toEqual({ status: 'ready', capability: cap })
    expect(await second).toEqual({ status: 'ready', capability: cap })
    expect(await load(target)).toEqual({ status: 'ready', capability: cap })
    expect(calls).toBe(1)
  })

  test('does not permanently cache unavailable metadata', async () => {
    let calls = 0
    const load = createReasoningCapabilityLoader(async () => {
      calls++
      return { found: false }
    })
    const target = { provider: 'openai', model: 'gpt-5' }

    expect(await load(target)).toEqual({ status: 'unavailable' })
    expect(await load(target)).toEqual({ status: 'unavailable' })
    expect(calls).toBe(2)
  })

  test('bounds authoritative results with least-recently-used eviction', async () => {
    const calls = []
    const load = createReasoningCapabilityLoader(async (target) => {
      calls.push(target.model)
      return { found: true }
    }, 2)
    const target = (model) => ({ provider: 'openrouter', model })

    await load(target('model-a'))
    await load(target('model-b'))
    await load(target('model-a'))
    await load(target('model-c'))
    await load(target('model-a'))
    await load(target('model-b'))

    expect(calls).toEqual(['model-a', 'model-b', 'model-c', 'model-b'])
  })
})

describe('targeted reasoning capability scheduler', () => {
  test('collapses rapid target changes to the final delayed lookup', async () => {
    const timers = manualTimers()
    const calls = []
    const results = []
    const schedule = createReasoningCapabilityScheduler(
      async (target) => {
        calls.push(target.model)
        return { status: 'ready' }
      },
      300,
      timers,
    )
    const target = (model) => ({ provider: 'openrouter', model })

    schedule(target('g'), (state) => results.push(state))
    timers.advance(100)
    schedule(target('gp'), (state) => results.push(state))
    timers.advance(100)
    schedule(target('gpt-5'), (state) => results.push(state))
    timers.advance(299)
    expect(calls).toEqual([])

    timers.advance(1)
    await flushPromises()
    expect(calls).toEqual(['gpt-5'])
    expect(results).toEqual([{ status: 'ready' }])
  })

  test('ignores completion from a superseded in-flight target', async () => {
    const timers = manualTimers()
    const calls = []
    const pending = new Map()
    const results = []
    const schedule = createReasoningCapabilityScheduler(
      (target) => {
        calls.push(target.model)
        return new Promise((resolve) => pending.set(target.model, resolve))
      },
      300,
      timers,
    )
    const target = (model) => ({ provider: 'openrouter', model })

    schedule(target('partial'), (state) => results.push(['partial', state]))
    timers.advance(300)
    schedule(target('final'), (state) => results.push(['final', state]))
    timers.advance(300)
    expect(calls).toEqual(['partial', 'final'])

    pending.get('partial')({ status: 'unavailable' })
    await flushPromises()
    expect(results).toEqual([])

    pending.get('final')({ status: 'ready' })
    await flushPromises()
    expect(results).toEqual([['final', { status: 'ready' }]])
  })
})

function memoryStorage(initial = {}) {
  const values = new Map(Object.entries(initial))
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  }
}

function capability(values) {
  return {
    values: values.map((value) => ({ value, label: value })),
    mandatory: false,
    can_disable: false,
    source: 'static',
  }
}

function manualTimers() {
  let now = 0
  let nextId = 0
  const tasks = new Map()

  return {
    setTimeout(callback, delayMs) {
      const id = ++nextId
      tasks.set(id, { at: now + delayMs, callback })
      return id
    },
    clearTimeout(id) {
      tasks.delete(id)
    },
    advance(ms) {
      now += ms
      for (;;) {
        const due = [...tasks.entries()]
          .filter(([, task]) => task.at <= now)
          .sort((left, right) => left[1].at - right[1].at || left[0] - right[0])
        if (due.length === 0) return
        const [id, task] = due[0]
        tasks.delete(id)
        task.callback()
      }
    },
  }
}

async function flushPromises() {
  await Promise.resolve()
  await Promise.resolve()
}
