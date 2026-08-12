import { useEffect, useState } from 'react'
import { get } from '@/lib/api'
import type { ReasoningCapability } from '@/lib/models'
import type {
  ReasoningCapabilityState,
  ReasoningModelTarget,
} from '@/lib/reasoning'

export interface ReasoningModelInfo {
  found: boolean
  reasoning_capability?: ReasoningCapability
}

export type FetchReasoningModelInfo = (
  target: ReasoningModelTarget,
) => Promise<ReasoningModelInfo>

export function createReasoningCapabilityLoader(
  fetchInfo: FetchReasoningModelInfo,
): (target: ReasoningModelTarget) => Promise<ReasoningCapabilityState> {
  const cache = new Map<string, ReasoningCapabilityState>()
  const inFlight = new Map<string, Promise<ReasoningCapabilityState>>()

  return (target) => {
    const key = JSON.stringify([target.provider, target.model])
    const cached = cache.get(key)
    if (cached) return Promise.resolve(cached)
    const pending = inFlight.get(key)
    if (pending) return pending

    const request = fetchInfo(target)
      .then((info): ReasoningCapabilityState => {
        if (!info.found) return { status: 'unavailable' }
        const state: ReasoningCapabilityState = info.reasoning_capability
          ? { status: 'ready', capability: info.reasoning_capability }
          : { status: 'ready' }
        cache.set(key, state)
        return state
      })
      .catch((): ReasoningCapabilityState => ({ status: 'unavailable' }))
      .finally(() => {
        inFlight.delete(key)
      })
    inFlight.set(key, request)
    return request
  }
}

const loadReasoningCapability = createReasoningCapabilityLoader((target) =>
  get<ReasoningModelInfo>(
    `/providers/${encodeURIComponent(target.provider)}/model-info?model=${encodeURIComponent(target.model)}`,
  ),
)

export function useReasoningCapability(
  target?: ReasoningModelTarget,
): ReasoningCapabilityState {
  const provider = target?.provider ?? ''
  const model = target?.model ?? ''
  const key = JSON.stringify([provider, model])
  const [snapshot, setSnapshot] = useState<{
    key: string
    state: ReasoningCapabilityState
  }>()

  useEffect(() => {
    if (!provider || !model) return
    let cancelled = false
    void loadReasoningCapability({ provider, model }).then((state) => {
      if (!cancelled) setSnapshot({ key, state })
    })
    return () => {
      cancelled = true
    }
  }, [key, model, provider])

  if (!provider || !model) return { status: 'unavailable' }
  return snapshot?.key === key ? snapshot.state : { status: 'loading' }
}
