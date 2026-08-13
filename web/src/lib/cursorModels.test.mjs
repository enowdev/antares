import { describe, expect, test } from 'bun:test'
import {
  cursorFilterCommit,
  cursorFilterFromVariant,
  cursorFilterMatches,
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
  withCursorFilter,
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

})

// Two variants that share no axis value: reaching one from the other means
// moving both dimensions, which one-axis-at-a-time filtering cannot express.
const diagonalFixture = {
  id: 'diagonal',
  name: 'Diagonal',
  aliases: [],
  parameters: [
    { id: 'context', values: [{ value: 'short' }, { value: 'long' }] },
    { id: 'reasoning', values: [{ value: 'low' }, { value: 'max' }] },
  ],
  variants: [
    {
      params: [
        { id: 'context', value: 'short' },
        { id: 'reasoning', value: 'low' },
        { id: 'internal', value: 'cheap' },
      ],
      displayName: 'Short · low',
      isDefault: true,
    },
    {
      params: [
        { id: 'context', value: 'long' },
        { id: 'reasoning', value: 'max' },
        { id: 'internal', value: 'rich' },
      ],
      displayName: 'Long · max',
    },
  ],
}

const tiedFixture = {
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

describe('filtering variants with staged controls', () => {
  test('a filter starts as the committed variant, hidden params excluded', () => {
    expect(
      cursorFilterFromVariant(multiVariantFixture, multiVariantFixture.variants[2]),
    ).toEqual([
      { id: 'context', value: '1m' },
      { id: 'reasoning', value: 'max' },
    ])
  })

  test('a diagonal variant is reachable, and keeps its hidden params exactly', () => {
    const from = cursorFilterFromVariant(diagonalFixture, diagonalFixture.variants[0])
    // The older filter entry gives way to the choice just made, rather than
    // making the only other configuration unreachable.
    const next = withCursorFilter(diagonalFixture, from, 'context', 'long')
    expect(next).toEqual([{ id: 'context', value: 'long' }])
    const committed = cursorFilterCommit(diagonalFixture, next)
    expect(committed).toBe(diagonalFixture.variants[1])
    expect(variantSelection(committed)).toEqual({
      context: 'long',
      reasoning: 'max',
      internal: 'rich',
    })
  })

  test('the other axis is reachable from the same starting point', () => {
    const from = cursorFilterFromVariant(diagonalFixture, diagonalFixture.variants[0])
    const next = withCursorFilter(diagonalFixture, from, 'reasoning', 'max')
    expect(cursorFilterCommit(diagonalFixture, next)).toBe(diagonalFixture.variants[1])
  })

  test('a filter that still matches several variants commits nothing yet', () => {
    const partial = [{ id: 'context', value: '272k' }]
    expect(cursorFilterMatches(multiVariantFixture, partial)).toHaveLength(2)
    expect(cursorFilterCommit(multiVariantFixture, partial)).toBeNull()

    const narrowed = withCursorFilter(multiVariantFixture, partial, 'reasoning', 'max')
    expect(narrowed).toEqual([
      { id: 'context', value: '272k' },
      { id: 'reasoning', value: 'max' },
    ])
    expect(cursorFilterCommit(multiVariantFixture, narrowed)).toBe(
      multiVariantFixture.variants[1],
    )
  })

  test('variants that differ only in a hidden param are never broken by a guess', () => {
    const filter = withCursorFilter(tiedFixture, [], 'fast', 'on')
    expect(cursorFilterMatches(tiedFixture, filter)).toHaveLength(2)
    expect(cursorFilterCommit(tiedFixture, filter)).toBeNull()
  })

  test('a filter no variant satisfies matches nothing and commits nothing', () => {
    const impossible = [
      { id: 'context', value: '1m' },
      { id: 'reasoning', value: 'low' },
    ]
    expect(cursorFilterMatches(multiVariantFixture, impossible)).toEqual([])
    expect(cursorFilterCommit(multiVariantFixture, impossible)).toBeNull()
  })

  test('staging never produces a filter that matches nothing', () => {
    const from = cursorFilterFromVariant(multiVariantFixture, multiVariantFixture.variants[0])
    for (const dimension of ['context', 'reasoning']) {
      for (const value of ['272k', '1m', 'low', 'max']) {
        const next = withCursorFilter(multiVariantFixture, from, dimension, value)
        if (next.some((entry) => entry.id === dimension && entry.value === value)) {
          expect(cursorFilterMatches(multiVariantFixture, next).length).toBeGreaterThan(0)
        }
      }
    }
  })

  test('choosing the same dimension twice replaces rather than repeats it', () => {
    const first = withCursorFilter(multiVariantFixture, [], 'reasoning', 'low')
    const second = withCursorFilter(multiVariantFixture, first, 'reasoning', 'max')
    expect(second).toEqual([{ id: 'reasoning', value: 'max' }])
  })

  test('a value no variant offers is refused instead of emptying the filter', () => {
    const from = cursorFilterFromVariant(modelFixture, modelFixture.variants[0])
    expect(withCursorFilter(modelFixture, from, 'context', '1m')).toEqual(from)
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
