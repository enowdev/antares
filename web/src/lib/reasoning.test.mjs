import { describe, expect, test } from 'bun:test'
import {
  loadReasoningPreference,
  reasoningOptions,
  reasoningPreferenceKey,
  saveReasoningPreference,
} from './reasoning.ts'

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
