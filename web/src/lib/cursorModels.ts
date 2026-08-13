/**
 * Pure helpers over the Cursor model catalogue.
 *
 * Cursor returns whole variants, and a variant's `params` array is the only
 * shape the API accepts. Every helper here therefore hands back a concrete
 * upstream variant (including params Cursor never lists in `parameters`) or
 * nothing at all — a combination Cursor did not return is never assembled from
 * the individual parameter values.
 */

export interface CursorParameterValue {
  value: string
  displayName?: string
}

export interface CursorParameter {
  id: string
  displayName?: string
  values: CursorParameterValue[]
}

export interface CursorVariantParam {
  id: string
  value: string
}

export interface CursorVariant {
  params: CursorVariantParam[]
  displayName: string
  description?: string
  isDefault?: boolean
}

export interface CursorModel {
  id: string
  name: string
  description?: string
  aliases: string[]
  parameters: CursorParameter[]
  variants: CursorVariant[]
}

export interface CursorDimensionValue {
  value: string
  label: string
}

export interface CursorDimension {
  id: string
  label: string
  values: CursorDimensionValue[]
}

/** Parameter ids Cursor uses for the reasoning-like axis, in priority order. */
export const REASONING_DIMENSION_IDS = ['reasoning', 'effort', 'thinking'] as const

/**
 * The upstream default variant, or null when the catalogue returned none. An
 * empty parameter list is not a substitute: it would be a selection Cursor
 * never offered.
 */
export function defaultCursorVariant(model: CursorModel): CursorVariant | null {
  const variants = model.variants ?? []
  return variants.find((variant) => variant.isDefault) ?? variants[0] ?? null
}

/** Whether the catalogue gives this model anything that can actually be run. */
export function cursorModelSelectable(model: CursorModel): boolean {
  return defaultCursorVariant(model) !== null
}

/** A variant's params as an id → value map, hidden params included. */
export function variantSelection(variant: CursorVariant): Record<string, string> {
  const selection: Record<string, string> = {}
  for (const param of variant.params ?? []) selection[param.id] = param.value
  return selection
}

export function variantParamValue(
  variant: CursorVariant,
  id: string,
): string | undefined {
  return (variant.params ?? []).find((param) => param.id === id)?.value
}

/** Every upstream variant whose params satisfy the given filter. */
export function matchingCursorVariants(
  model: CursorModel,
  selection: Record<string, string>,
): CursorVariant[] {
  const entries = Object.entries(selection)
  return (model.variants ?? []).filter((variant) => {
    const params = variantSelection(variant)
    return entries.every(([id, value]) => params[id] === value)
  })
}

/**
 * The single upstream variant a filter resolves to. Zero matches and ambiguous
 * matches both commit nothing, so a control can only ever apply a real variant.
 */
export function selectExactVariant(
  model: CursorModel,
  selection: Record<string, string>,
): CursorVariant | null {
  const matches = matchingCursorVariants(model, selection)
  return matches.length === 1 ? matches[0] : null
}

/** A selection as an id → value map, or null when an id repeats. */
function canonicalParamMap(
  params: CursorVariantParam[],
): Map<string, string> | null {
  const canonical = new Map<string, string>()
  for (const param of params ?? []) {
    if (!param.id || canonical.has(param.id)) return null
    canonical.set(param.id, param.value)
  }
  return canonical
}

/**
 * The upstream variant that carries exactly this stored selection, whatever
 * order it was stored in. A selection that no longer matches any variant
 * resolves to nothing: falling back to the default would silently run a
 * different model configuration than the conversation used.
 */
export function resolveCursorVariant(
  model: CursorModel,
  params: CursorVariantParam[],
): CursorVariant | null {
  const wanted = canonicalParamMap(params)
  if (!wanted) return null
  for (const variant of model.variants ?? []) {
    const candidate = canonicalParamMap(variant.params ?? [])
    if (!candidate || candidate.size !== wanted.size) continue
    let equal = true
    for (const [id, value] of wanted) {
      if (candidate.get(id) !== value) {
        equal = false
        break
      }
    }
    if (equal) return variant
  }
  return null
}

/**
 * One dimension the controls have narrowed, in the order it was chosen. The
 * filter is a view over the catalogue, never a selection in its own right: only
 * a filter that leaves exactly one upstream variant changes what will run.
 */
export interface CursorFilterEntry {
  id: string
  value: string
}

function filterSelection(filter: CursorFilterEntry[]): Record<string, string> {
  const selection: Record<string, string> = {}
  for (const entry of filter ?? []) selection[entry.id] = entry.value
  return selection
}

/** The upstream variants a filter still allows. */
export function cursorFilterMatches(
  model: CursorModel,
  filter: CursorFilterEntry[],
): CursorVariant[] {
  return matchingCursorVariants(model, filterSelection(filter))
}

/**
 * The one variant a filter identifies, or null while it still allows several
 * (or none). Variants that differ only in params the catalogue does not show
 * therefore never get chosen for the user.
 */
export function cursorFilterCommit(
  model: CursorModel,
  filter: CursorFilterEntry[],
): CursorVariant | null {
  const matches = cursorFilterMatches(model, filter)
  return matches.length === 1 ? matches[0] : null
}

/** The filter a committed variant corresponds to: its visible dimensions. */
export function cursorFilterFromVariant(
  model: CursorModel,
  variant: CursorVariant,
): CursorFilterEntry[] {
  const params = variantSelection(variant)
  const filter: CursorFilterEntry[] = []
  for (const dimension of cursorVariantDimensions(model)) {
    const value = params[dimension.id]
    if (value !== undefined) filter.push({ id: dimension.id, value })
  }
  return filter
}

/**
 * Narrow a filter with one more choice. The newest choice always survives;
 * older ones give way to it when they cannot hold together, which is what makes
 * a variant that shares no value with the current one reachable without ever
 * inventing a combination the catalogue does not offer. A value no variant
 * carries at all changes nothing.
 */
export function withCursorFilter(
  model: CursorModel,
  filter: CursorFilterEntry[],
  dimensionId: string,
  value: string,
): CursorFilterEntry[] {
  if (cursorFilterMatches(model, [{ id: dimensionId, value }]).length === 0) {
    return filter
  }
  // Oldest first, with the new choice last and any older take on the same
  // dimension removed.
  let staged = [
    ...(filter ?? []).filter((entry) => entry.id !== dimensionId),
    { id: dimensionId, value },
  ]
  // Drop the least recent choices until the catalogue can satisfy the rest.
  while (staged.length > 1 && cursorFilterMatches(model, staged).length === 0) {
    staged = staged.slice(1)
  }
  return staged
}

/**
 * The controls a model can offer: catalogue-declared parameters, narrowed to
 * the values real variants use. Params a variant carries but the catalogue does
 * not declare stay hidden and travel with the variant.
 */
export function cursorVariantDimensions(model: CursorModel): CursorDimension[] {
  const used = new Map<string, Set<string>>()
  for (const variant of model.variants ?? []) {
    for (const param of variant.params ?? []) {
      const values = used.get(param.id) ?? new Set<string>()
      values.add(param.value)
      used.set(param.id, values)
    }
  }

  const dimensions: CursorDimension[] = []
  for (const parameter of model.parameters ?? []) {
    const available = used.get(parameter.id)
    if (!available) continue
    const values = (parameter.values ?? [])
      .filter((value) => available.has(value.value))
      .map((value) => ({ value: value.value, label: value.displayName || value.value }))
    if (values.length === 0) continue
    dimensions.push({
      id: parameter.id,
      label: parameter.displayName || parameter.id,
      values,
    })
  }
  return dimensions
}

/** The reasoning-like axis, when the connected catalogue exposes one. */
export function cursorReasoningDimension(model: CursorModel): CursorDimension | null {
  const dimensions = cursorVariantDimensions(model)
  for (const id of REASONING_DIMENSION_IDS) {
    const found = dimensions.find((dimension) => dimension.id === id)
    if (found) return found
  }
  return null
}

/** Every dimension except the reasoning-like one (Context, Fast, …). */
export function cursorOtherDimensions(model: CursorModel): CursorDimension[] {
  const reasoning = cursorReasoningDimension(model)
  return cursorVariantDimensions(model).filter(
    (dimension) => dimension.id !== reasoning?.id,
  )
}

/** A compact "Context 1M · Reasoning max" line for the visible dimensions. */
export function cursorVariantSummary(
  model: CursorModel,
  variant: CursorVariant,
): string {
  const selection = variantSelection(variant)
  return cursorVariantDimensions(model)
    .map((dimension) => {
      const value = selection[dimension.id]
      if (value === undefined) return ''
      const label =
        dimension.values.find((option) => option.value === value)?.label ?? value
      return `${dimension.label} ${label}`
    })
    .filter(Boolean)
    .join(' · ')
}

/** Search a Cursor model by id, display name, alias, or its provider label. */
export function cursorModelMatches(model: CursorModel, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  const haystack = [
    model.id,
    model.name,
    ...(model.aliases ?? []),
    'cursor',
    'cursor cloud agent',
  ]
  return haystack.some((entry) => entry.toLowerCase().includes(q))
}
