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

export interface ReasoningCapabilityTimers {
  setTimeout(callback: () => void, delayMs: number): unknown
  clearTimeout(handle: unknown): void
}

const reasoningCapabilityTimers: ReasoningCapabilityTimers = {
  setTimeout: (callback, delayMs) => globalThis.setTimeout(callback, delayMs),
  clearTimeout: (handle) =>
    globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
}

export function createReasoningCapabilityLoader(
  fetchInfo: FetchReasoningModelInfo,
  maxCacheEntries = 32,
): (target: ReasoningModelTarget) => Promise<ReasoningCapabilityState> {
  const cacheLimit = Math.max(0, Math.floor(maxCacheEntries))
  const cache = new Map<string, ReasoningCapabilityState>()
  const inFlight = new Map<string, Promise<ReasoningCapabilityState>>()

  return (target) => {
    const key = JSON.stringify([target.provider, target.model])
    const cached = cache.get(key)
    if (cached) {
      cache.delete(key)
      cache.set(key, cached)
      return Promise.resolve(cached)
    }
    const pending = inFlight.get(key)
    if (pending) return pending

    const request = fetchInfo(target)
      .then((info): ReasoningCapabilityState => {
        if (!info.found) return { status: 'unavailable' }
        const state: ReasoningCapabilityState = info.reasoning_capability
          ? { status: 'ready', capability: info.reasoning_capability }
          : { status: 'ready' }
        if (cacheLimit > 0) {
          cache.set(key, state)
          while (cache.size > cacheLimit) {
            const oldest = cache.keys().next().value
            if (oldest === undefined) break
            cache.delete(oldest)
          }
        }
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

export function createReasoningCapabilityScheduler(
  load: (target: ReasoningModelTarget) => Promise<ReasoningCapabilityState>,
  delayMs = 300,
  timers = reasoningCapabilityTimers,
): (
  target: ReasoningModelTarget,
  onResult: (state: ReasoningCapabilityState) => void,
) => () => void {
  let version = 0
  let pendingHandle: unknown
  let hasPendingTimer = false

  return (target, onResult) => {
    const requestVersion = ++version
    if (hasPendingTimer) {
      timers.clearTimeout(pendingHandle)
      hasPendingTimer = false
    }

    let handle: unknown
    handle = timers.setTimeout(() => {
      if (hasPendingTimer && pendingHandle === handle) {
        hasPendingTimer = false
      }
      if (requestVersion !== version) return
      void load(target).then(
        (state) => {
          if (requestVersion === version) onResult(state)
        },
        () => {},
      )
    }, delayMs)
    pendingHandle = handle
    hasPendingTimer = true

    return () => {
      if (requestVersion !== version) return
      version++
      if (hasPendingTimer && pendingHandle === handle) {
        timers.clearTimeout(handle)
        hasPendingTimer = false
      }
    }
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
  const [schedule] = useState(() =>
    createReasoningCapabilityScheduler(loadReasoningCapability),
  )

  useEffect(() => {
    if (!provider || !model) return
    return schedule({ provider, model }, (state) => setSnapshot({ key, state }))
  }, [key, model, provider, schedule])

  if (!provider || !model) return { status: 'unavailable' }
  return snapshot?.key === key ? snapshot.state : { status: 'loading' }
}
