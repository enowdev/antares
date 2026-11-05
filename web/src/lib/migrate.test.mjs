import { describe, expect, test } from 'bun:test'
import {
  allowedResolutions,
  backupName,
  buildChoices,
  defaultKeepResolution,
  groupPlan,
  initialSelection,
  missingInputs,
} from './migrate.ts'

const items = [
  { id: 'skill:web', category: 'skill', title: 'web', status: 'ready', selected: true },
  { id: 'provider:openrouter', category: 'provider', title: 'OpenRouter', status: 'ready', selected: true },
  { id: 'provider:anthropic', category: 'provider', title: 'Anthropic', status: 'conflict', selected: false },
  { id: 'provider:deepseek', category: 'provider', title: 'DeepSeek', status: 'needs_input', input: 'api_key', selected: false },
  { id: 'channel:dingtalk', category: 'channel', title: 'DingTalk', status: 'unsupported', selected: false },
  { id: 'soul:main', category: 'soul', title: 'SOUL.md', status: 'conflict', selected: false },
]

describe('resolutions', () => {
  test('rename only for provider/skill/mcp/role, append only for text files', () => {
    expect(allowedResolutions('provider')).toEqual(['skip', 'replace', 'rename'])
    expect(allowedResolutions('role')).toContain('rename')
    expect(allowedResolutions('soul')).toEqual(['skip', 'replace', 'append'])
    expect(allowedResolutions('user_md')).toContain('append')
    expect(allowedResolutions('model')).toEqual(['skip', 'replace'])
    expect(allowedResolutions('channel')).toEqual(['skip', 'replace'])
  })

  test('ticking a conflict picks the least destructive keep', () => {
    expect(defaultKeepResolution('skill')).toBe('rename')
    expect(defaultKeepResolution('agents_md')).toBe('append')
    expect(defaultKeepResolution('channel')).toBe('replace')
  })
})

describe('selection', () => {
  test('defaults follow item.selected and skip unsupported items', () => {
    const sel = initialSelection(items)
    expect(sel['provider:openrouter'].selected).toBe(true)
    expect(sel['provider:anthropic'].selected).toBe(false)
    expect(sel['channel:dingtalk']).toBeUndefined()
  })

  test('a re-plan keeps earlier choices by id', () => {
    const prev = { 'provider:anthropic': { selected: true, resolution: 'rename', input: '' } }
    const sel = initialSelection(items, prev)
    expect(sel['provider:anthropic']).toEqual(prev['provider:anthropic'])
    expect(sel['skill:web'].selected).toBe(true)
  })

  test('groups follow category order with counts', () => {
    const groups = groupPlan(items, initialSelection(items))
    expect(groups.map((g) => g.category)).toEqual(['provider', 'soul', 'skill', 'channel'])
    const prov = groups[0]
    expect(prov.items.length).toBe(3)
    expect(prov.selectable).toBe(3)
    expect(prov.selected).toBe(1)
    expect(groups[3].selectable).toBe(0)
  })
})

describe('choices', () => {
  test('only ticked supported items, with resolution and input where relevant', () => {
    const sel = initialSelection(items)
    sel['provider:anthropic'] = { selected: true, resolution: 'rename', input: '' }
    sel['provider:deepseek'] = { selected: true, resolution: 'skip', input: '  sk-test  ' }
    sel['soul:main'] = { selected: true, resolution: 'append', input: '' }
    expect(buildChoices(items, sel)).toEqual([
      { id: 'skill:web' },
      { id: 'provider:openrouter' },
      { id: 'provider:anthropic', resolution: 'rename' },
      { id: 'provider:deepseek', input: 'sk-test' },
      { id: 'soul:main', resolution: 'append' },
    ])
  })

  test('ticked needs_input without a value is reported missing', () => {
    const sel = initialSelection(items)
    sel['provider:deepseek'].selected = true
    expect(missingInputs(items, sel).map((i) => i.id)).toEqual(['provider:deepseek'])
  })
})

test('backupName strips the parent path', () => {
  expect(backupName('/home/u/.antares/backups/migrate-hermes-20261003T101500Z')).toBe(
    'migrate-hermes-20261003T101500Z',
  )
  expect(backupName('migrate-hermes-1')).toBe('migrate-hermes-1')
})
