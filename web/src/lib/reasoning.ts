import type { ReasoningCapability, ReasoningValue } from '@/lib/models'

export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

const LEGACY_REASONING_KEY = 'antares:reasoning'
const REASONING_KEY_PREFIX = 'antares:reasoning:v2'

export type ReasoningCapabilityStatus = 'loading' | 'ready' | 'unavailable'

export interface ReasoningCapabilityState {
  status: ReasoningCapabilityStatus
  capability?: ReasoningCapability
}

export interface ReasoningControlResolution {
  options: ReasoningValue[]
  unsupported: boolean
  hint: 'loading' | 'unavailable' | 'unsupported' | 'mandatory' | 'auto' | 'provider-controlled'
}

export interface ReasoningModelTarget {
  provider: string
  model: string
}

export function reasoningOptions(capability?: ReasoningCapability): ReasoningValue[] {
  if (!capability) return [{ value: '', label: 'Auto' }]
  const values = capability.values.filter(
    (option) =>
      option.kind !== 'disable' ||
      (capability.can_disable && !capability.mandatory),
  )
  return [{ value: '', label: 'Auto' }, ...values]
}

export function resolveReasoningControl(
  value: string,
  state: ReasoningCapabilityState,
): ReasoningControlResolution {
  if (state.status !== 'ready') {
    const options = reasoningOptions()
    if (value) options.push({ value, label: value })
    return { options, unsupported: false, hint: state.status }
  }

  const options = reasoningOptions(state.capability)
  const unsupported =
    value !== '' && !options.some((option) => option.value === value)
  const hint = unsupported
    ? 'unsupported'
    : state.capability?.mandatory
      ? 'mandatory'
      : state.capability
        ? 'auto'
        : 'provider-controlled'
  return { options, unsupported, hint }
}

export function resolveReasoningModelTarget(
  modelRef: string,
  active: ReasoningModelTarget,
  configuredProviders: readonly string[],
): ReasoningModelTarget {
  const model = modelRef.trim()
  if (!model) return active

  const slash = model.indexOf('/')
  if (slash > 0 && slash < model.length - 1) {
    const candidate = model.slice(0, slash)
    if (
      configuredProviders.includes(candidate) ||
      candidate === active.provider ||
      candidate === 'google'
    ) {
      return { provider: candidate, model: model.slice(slash + 1) }
    }
  }
  return { provider: active.provider, model }
}

export function reasoningPreferenceKey(provider: string, model: string): string {
  return `${REASONING_KEY_PREFIX}:${encodeURIComponent(provider)}:${encodeURIComponent(model)}`
}

export function loadReasoningPreference(
  storage: StorageLike,
  provider: string,
  model: string,
  capability?: ReasoningCapability,
): { value: string; migrated: boolean } {
  const key = reasoningPreferenceKey(provider, model)
  const allowed = new Set(reasoningOptions(capability).map((option) => option.value))

  try {
    const scoped = storage.getItem(key)
    const legacy = storage.getItem(LEGACY_REASONING_KEY)
    if (legacy !== null) storage.removeItem(LEGACY_REASONING_KEY)

    if (scoped !== null) {
      // Missing metadata is not proof that an opaque value is invalid. Use Auto
      // for this request, but retain the preference so a later authoritative
      // capability lookup can restore it.
      if (!capability) return { value: '', migrated: false }
      if (scoped && allowed.has(scoped)) {
        return { value: scoped, migrated: false }
      }
      storage.removeItem(key)
      return { value: '', migrated: false }
    }

    if (legacy && allowed.has(legacy)) {
      storage.setItem(key, legacy)
      return { value: legacy, migrated: true }
    }
  } catch {
    // Storage is best-effort (private browsing and quotas may reject access).
  }

  return { value: '', migrated: false }
}

export function saveReasoningPreference(
  storage: StorageLike,
  provider: string,
  model: string,
  value: string,
): void {
  const key = reasoningPreferenceKey(provider, model)
  try {
    if (value) storage.setItem(key, value)
    else storage.removeItem(key)
  } catch {
    // Preference persistence must never prevent composing a message.
  }
}
