import { describe, expect, test } from 'bun:test'
import {
  applyCursorDimension,
  cursorDimensionAvailability,
  cursorModelMatches,
  cursorModelSelectable,
  cursorReasoningDimension,
  cursorVariantDimensions,
  cursorVariantSummary,
  defaultCursorVariant,
  matchingCursorVariants,
  resolveCursorVariant,
  selectExactVariant,
  variantSelection,
} from './cursorModels.ts'

const modelFixture = {
  id: 'gpt-test',
  name: 'GPT Test',
  aliases: [],
  parameters: [
    { id: 'context', values: [{ value: '272k' }, { value: '1m' }] },
    { id: 'reasoning', values: [{ value: 'low' }, { value: 'max' }] },
    { id: 'fast', values: [{ value: 'false' }, { value: 'true' }] },
  ],
  variants: [
    {
      params: [
        { id: 'context', value: '272k' },
        { id: 'reasoning', value: 'max' },
        { id: 'fast', value: 'true' },
      ],
      displayName: 'GPT Test',
      isDefault: true,
    },
  ],
}

// Two reachable context values, each with its own reasoning ladder, so a filter
// can be proven to land on a real upstream variant instead of a synthesized one.
const multiVariantFixture = {
  id: 'gpt-5.6-sol',
  name: 'GPT 5.6 Sol',
  aliases: ['sol'],
  parameters: [
    {
      id: 'context',
      displayName: 'Context',
      values: [
        { value: '272k', displayName: '272K' },
        { value: '1m', displayName: '1M' },
      ],
    },
    {
      id: 'reasoning',
      displayName: 'Reasoning',
      values: [{ value: 'low' }, { value: 'max' }],
    },
  ],
  variants: [
    {
      params: [
        { id: 'context', value: '272k' },
        { id: 'reasoning', value: 'low' },
        { id: 'internal', value: 'off' },
      ],
      displayName: 'GPT 5.6 Sol',
      isDefault: true,
    },
    {
      params: [
        { id: 'context', value: '272k' },
        { id: 'reasoning', value: 'max' },
        { id: 'internal', value: 'off' },
      ],
      displayName: 'GPT 5.6 Sol (max)',
    },
    {
      params: [
        { id: 'context', value: '1m' },
        { id: 'reasoning', value: 'max' },
        { id: 'internal', value: 'on' },
      ],
      displayName: 'GPT 5.6 Sol (1M)',
    },
  ],
}

const autoSmartFixture = {
  id: 'auto-smart',
  name: 'Auto (smart)',
  aliases: ['auto'],
  parameters: [
    {
      id: 'optimize_for',
      displayName: 'Optimize for',
      values: [
        { value: 'speed', displayName: 'Speed' },
        { value: 'quality', displayName: 'Quality' },
      ],
    },
  ],
  variants: [
    {
      params: [{ id: 'optimize_for', value: 'speed' }],
      displayName: 'Auto (speed)',
      isDefault: true,
    },
    {
      params: [{ id: 'optimize_for', value: 'quality' }],
      displayName: 'Auto (quality)',
    },
  ],
}

describe('exact Cursor variants', () => {
  test('default variant keeps hidden params', () => {
    const model = {
      id: 'claude-opus-5',
      name: 'Claude Opus 5',
      aliases: [],
      parameters: [{ id: 'effort', values: [{ value: 'max' }] }],
      variants: [
        {
          params: [
            { id: 'cyber', value: 'false' },
            { id: 'effort', value: 'max' },
          ],
          displayName: 'Claude Opus 5',
          isDefault: true,
        },
      ],
    }
    expect(defaultCursorVariant(model).params).toEqual(model.variants[0].params)
  })

  test('filters never synthesize a missing combination', () => {
    expect(
      selectExactVariant(modelFixture, { context: '1m', reasoning: 'max', fast: 'true' }),
    ).toBeNull()
  })

  test('prefers the upstream default variant, then the first one', () => {
    expect(defaultCursorVariant(multiVariantFixture)).toBe(multiVariantFixture.variants[0])
    const noDefault = { ...multiVariantFixture, variants: multiVariantFixture.variants.slice(1) }
    expect(defaultCursorVariant(noDefault)).toBe(noDefault.variants[0])
  })

  test('a model the catalogue gave no variant for is not selectable', () => {
    const bare = { id: 'bare', name: 'Bare', aliases: [], parameters: [], variants: [] }
    // Sending an invented empty params array would be a selection Cursor never
    // offered, so there is nothing to select at all.
    expect(defaultCursorVariant(bare)).toBeNull()
    expect(selectExactVariant(bare, {})).toBeNull()
    expect(cursorModelSelectable(bare)).toBe(false)
    expect(cursorModelSelectable(multiVariantFixture)).toBe(true)
  })

  test('an exact match returns the upstream variant object itself', () => {
    const variant = selectExactVariant(multiVariantFixture, { context: '1m', reasoning: 'max' })
    expect(variant).toBe(multiVariantFixture.variants[2])
    expect(variant.params).toEqual([
      { id: 'context', value: '1m' },
      { id: 'reasoning', value: 'max' },
      { id: 'internal', value: 'on' },
    ])
  })

  test('an ambiguous filter commits nothing', () => {
    expect(matchingCursorVariants(multiVariantFixture, { context: '272k' })).toHaveLength(2)
    expect(selectExactVariant(multiVariantFixture, { context: '272k' })).toBeNull()
  })

  test('changing one dimension keeps the others and carries the hidden params', () => {
    const from = multiVariantFixture.variants[0]
    const next = applyCursorDimension(multiVariantFixture, from, 'reasoning', 'max')
    expect(next).toBe(multiVariantFixture.variants[1])
    expect(variantSelection(next)).toEqual({
      context: '272k',
      reasoning: 'max',
      internal: 'off',
    })

    const wider = applyCursorDimension(
      multiVariantFixture,
      multiVariantFixture.variants[1],
      'context',
      '1m',
    )
    expect(wider).toBe(multiVariantFixture.variants[2])
  })

  test('a value that would silently change another dimension commits nothing', () => {
    // Only 1M + max exists upstream, so moving context while reasoning is low
    // must not quietly raise reasoning too.
    expect(
      applyCursorDimension(multiVariantFixture, multiVariantFixture.variants[0], 'context', '1m'),
    ).toBeNull()
  })

  test('an unreachable dimension value commits nothing', () => {
    expect(
      applyCursorDimension(modelFixture, modelFixture.variants[0], 'context', '1m'),
    ).toBeNull()
  })

  test('a tie between variants commits nothing', () => {
    // Two variants share every visible dimension and differ only in a hidden
    // one, so "fast on" cannot identify a single upstream variant.
    const tied = {
      id: 'tied',
      name: 'Tied',
      aliases: [],
      parameters: [{ id: 'fast', values: [{ value: 'off' }, { value: 'on' }] }],
      variants: [
        { params: [{ id: 'fast', value: 'off' }], displayName: 'off', isDefault: true },
        {
          params: [
            { id: 'fast', value: 'on' },
            { id: 'internal', value: 'a' },
          ],
          displayName: 'on a',
        },
        {
          params: [
            { id: 'fast', value: 'on' },
            { id: 'internal', value: 'b' },
          ],
          displayName: 'on b',
        },
      ],
    }
    expect(applyCursorDimension(tied, tied.variants[0], 'fast', 'on')).toBeNull()
    expect(cursorDimensionAvailability(tied, tied.variants[0], 'fast')).toEqual(['off'])
  })

  test('availability marks exactly the values a control may commit', () => {
    expect(
      cursorDimensionAvailability(multiVariantFixture, multiVariantFixture.variants[0], 'context'),
    ).toEqual(['272k'])
    expect(
      cursorDimensionAvailability(multiVariantFixture, multiVariantFixture.variants[1], 'context'),
    ).toEqual(['272k', '1m'])
    expect(
      cursorDimensionAvailability(multiVariantFixture, multiVariantFixture.variants[0], 'reasoning'),
    ).toEqual(['low', 'max'])
  })
})

describe('Cursor variant dimensions', () => {
  test('exposes only declared parameters that real variants use', () => {
    expect(cursorVariantDimensions(multiVariantFixture).map((d) => d.id)).toEqual([
      'context',
      'reasoning',
    ])
  })

  test('drops declared values no variant offers', () => {
    const dimensions = cursorVariantDimensions(modelFixture)
    expect(dimensions.map((d) => d.id)).toEqual(['context', 'reasoning', 'fast'])
    expect(dimensions[0].values).toEqual([{ value: '272k', label: '272k' }])
  })

  test('uses catalogue display names for dimensions and values', () => {
    const [context] = cursorVariantDimensions(multiVariantFixture)
    expect(context.label).toBe('Context')
    expect(context.values).toEqual([
      { value: '272k', label: '272K' },
      { value: '1m', label: '1M' },
    ])
  })

  test('optimize_for appears only when the connected catalogue returns it', () => {
    expect(cursorVariantDimensions(autoSmartFixture).map((d) => d.id)).toEqual(['optimize_for'])
    expect(cursorVariantDimensions(multiVariantFixture).map((d) => d.id)).not.toContain(
      'optimize_for',
    )
  })

  test('finds the reasoning-like axis and leaves the rest as plain dimensions', () => {
    expect(cursorReasoningDimension(multiVariantFixture)?.id).toBe('reasoning')
    expect(cursorReasoningDimension(autoSmartFixture)).toBeNull()
    expect(
      cursorReasoningDimension({
        ...autoSmartFixture,
        parameters: [{ id: 'effort', values: [{ value: 'max' }] }],
        variants: [{ params: [{ id: 'effort', value: 'max' }], displayName: 'e' }],
      })?.id,
    ).toBe('effort')
  })

  test('summarizes a variant from its visible params', () => {
    expect(cursorVariantSummary(multiVariantFixture, multiVariantFixture.variants[2])).toBe(
      'Context 1M · Reasoning max',
    )
  })
})

describe('restoring a stored selection', () => {
  test('matches the one variant carrying exactly those params', () => {
    const variant = resolveCursorVariant(multiVariantFixture, [
      { id: 'context', value: '1m' },
      { id: 'reasoning', value: 'max' },
      { id: 'internal', value: 'on' },
    ])
    expect(variant).toBe(multiVariantFixture.variants[2])
  })

  test('ignores the order the params were stored in', () => {
    const variant = resolveCursorVariant(multiVariantFixture, [
      { id: 'internal', value: 'off' },
      { id: 'reasoning', value: 'low' },
      { id: 'context', value: '272k' },
    ])
    expect(variant).toBe(multiVariantFixture.variants[0])
  })

  test('a partial or unknown selection never falls back to the default variant', () => {
    expect(
      resolveCursorVariant(multiVariantFixture, [{ id: 'context', value: '272k' }]),
    ).toBeNull()
    expect(resolveCursorVariant(multiVariantFixture, [])).toBeNull()
    expect(
      resolveCursorVariant(multiVariantFixture, [
        { id: 'context', value: '1m' },
        { id: 'reasoning', value: 'low' },
        { id: 'internal', value: 'on' },
      ]),
    ).toBeNull()
    expect(
      resolveCursorVariant(multiVariantFixture, [
        { id: 'context', value: '1m' },
        { id: 'reasoning', value: 'max' },
        { id: 'internal', value: 'on' },
        { id: 'extra', value: 'yes' },
      ]),
    ).toBeNull()
  })

  test('a duplicated parameter id is not a resolvable selection', () => {
    expect(
      resolveCursorVariant(multiVariantFixture, [
        { id: 'context', value: '272k' },
        { id: 'context', value: '1m' },
        { id: 'reasoning', value: 'low' },
        { id: 'internal', value: 'off' },
      ]),
    ).toBeNull()
  })

  test('a variant-less model resolves nothing at all', () => {
    const bare = { id: 'bare', name: 'Bare', aliases: [], parameters: [], variants: [] }
    expect(resolveCursorVariant(bare, [])).toBeNull()
    expect(resolveCursorVariant(bare, [{ id: 'reasoning', value: 'max' }])).toBeNull()
  })

  test('stored values are matched case-sensitively', () => {
    expect(
      resolveCursorVariant(multiVariantFixture, [
        { id: 'context', value: '272K' },
        { id: 'reasoning', value: 'low' },
        { id: 'internal', value: 'off' },
      ]),
    ).toBeNull()
  })
})

describe('Cursor model search', () => {
  test('matches id, display name, alias, and the Cursor provider label', () => {
    expect(cursorModelMatches(multiVariantFixture, 'sol')).toBe(true)
    expect(cursorModelMatches(multiVariantFixture, 'GPT 5.6')).toBe(true)
    expect(cursorModelMatches(multiVariantFixture, 'gpt-5.6-SOL')).toBe(true)
    expect(cursorModelMatches(multiVariantFixture, 'cursor')).toBe(true)
    expect(cursorModelMatches(multiVariantFixture, 'claude')).toBe(false)
  })

  test('an empty query matches everything', () => {
    expect(cursorModelMatches(autoSmartFixture, '   ')).toBe(true)
  })
})
