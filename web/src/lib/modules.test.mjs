import { describe, expect, test } from 'bun:test'
import {
  MODULE_IDS,
  PRESET_IDS,
  PRESET_MODULES,
  presetFor,
  resolveModules,
  toModuleList,
} from './modules.ts'

describe('module resolution', () => {
  test('absent config means every module is on, so upgrades hide nothing', () => {
    expect([...resolveModules(null)]).toEqual([...MODULE_IDS])
    expect([...resolveModules(undefined)]).toEqual([...MODULE_IDS])
  })

  test('an empty list is a deliberate choice and stays empty', () => {
    expect(resolveModules([]).size).toBe(0)
  })

  test('unknown ids are dropped', () => {
    expect([...resolveModules(['automation', 'bogus', 7])]).toEqual(['automation'])
  })

  test('a malformed value fails open to every module', () => {
    expect(resolveModules('automation').size).toBe(MODULE_IDS.length)
  })
})

describe('presets', () => {
  test('every preset maps to known modules', () => {
    for (const id of PRESET_IDS) {
      for (const m of PRESET_MODULES[id]) expect(MODULE_IDS).toContain(m)
    }
  })

  test('each preset round-trips through presetFor', () => {
    for (const id of PRESET_IDS) {
      expect(presetFor(new Set(PRESET_MODULES[id]))).toBe(id)
    }
  })

  test('a set matching no preset is custom', () => {
    expect(presetFor(new Set(['security']))).toBe('custom')
  })

  test('toModuleList orders by MODULE_IDS', () => {
    expect(toModuleList(new Set(['studio', 'automation']))).toEqual(['automation', 'studio'])
  })
})
