import type { ReasoningCapability, ReasoningValue } from '@/lib/models'

export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

const LEGACY_REASONING_KEY = 'antares:reasoning'
const REASONING_KEY_PREFIX = 'antares:reasoning:v2'

export function reasoningOptions(capability?: ReasoningCapability): ReasoningValue[] {
  if (!capability) return [{ value: '', label: 'Auto' }]
  const values = capability.values.filter(
    (option) =>
      option.kind !== 'disable' ||
      (capability.can_disable && !capability.mandatory),
  )
  return [{ value: '', label: 'Auto' }, ...values]
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
