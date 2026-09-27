import { useCallback, useEffect, useRef, useState } from 'react'
import { get } from './api'

export interface AsyncState<T> {
  data: T | undefined
  loading: boolean
  error: Error | undefined
  reload: () => void
  setData: (v: T) => void
}

/**
 * Fetch a JSON endpoint with loading/error state. `deps` re-triggers the fetch.
 * Keeps previous data while reloading so lists do not flash empty.
 */
export function useApi<T>(path: string | null, deps: unknown[] = []): AsyncState<T> {
  const [data, setData] = useState<T>()
  const [loading, setLoading] = useState(path !== null)
  const [error, setError] = useState<Error>()
  const [nonce, setNonce] = useState(0)
  const alive = useRef(true)

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  useEffect(() => {
    if (path === null) {
      setLoading(false)
      return
    }
    setLoading(true)
    get<T>(path)
      .then((v) => {
        if (alive.current) {
          setData(v)
          setError(undefined)
        }
      })
      .catch((e: Error) => {
        if (alive.current) setError(e)
      })
      .finally(() => {
        if (alive.current) setLoading(false)
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, nonce, ...deps])

  const reload = useCallback(() => setNonce((n) => n + 1), [])
  return { data, loading, error, reload, setData }
}

/** Poll an endpoint on an interval; pauses while the tab is hidden. A null path
 *  disables fetching (and polling) until it becomes non-null. */
export function usePoll<T>(path: string | null, intervalMs = 5000): AsyncState<T> {
  const state = useApi<T>(path)
  const reload = state.reload

  useEffect(() => {
    let timer: ReturnType<typeof setInterval> | undefined
    const start = () => {
      stop()
      timer = setInterval(() => {
        if (document.visibilityState === 'visible') reload()
      }, intervalMs)
    }
    const stop = () => {
      if (timer) clearInterval(timer)
      timer = undefined
    }
    start()
    document.addEventListener('visibilitychange', start)
    return () => {
      stop()
      document.removeEventListener('visibilitychange', start)
    }
  }, [reload, intervalMs])

  return state
}

const LOCAL_STORAGE_EVENT = 'antares:local-storage'

/**
 * Persisted state backed by localStorage. Every hook instance using the same
 * key stays in step: a write in one (the command palette's theme toggle)
 * updates the others (the sidebar's theme button) without a reload.
 */
export function useLocalStorage<T>(key: string, initial: T): [T, (v: T) => void] {
  const [value, setValue] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(key)
      return raw ? (JSON.parse(raw) as T) : initial
    } catch {
      return initial
    }
  })
  useEffect(() => {
    const onChange = (e: Event) => {
      const detail = (e as CustomEvent<{ key: string; value: T }>).detail
      if (detail?.key === key) setValue(detail.value)
    }
    window.addEventListener(LOCAL_STORAGE_EVENT, onChange)
    return () => window.removeEventListener(LOCAL_STORAGE_EVENT, onChange)
  }, [key])
  const update = useCallback(
    (v: T) => {
      setValue(v)
      try {
        localStorage.setItem(key, JSON.stringify(v))
      } catch {
        /* quota or private mode */
      }
      window.dispatchEvent(new CustomEvent(LOCAL_STORAGE_EVENT, { detail: { key, value: v } }))
    },
    [key],
  )
  return [value, update]
}

/** True when the viewport is at least `query` wide. */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() =>
    typeof window === 'undefined' ? false : window.matchMedia(query).matches,
  )
  useEffect(() => {
    const mq = window.matchMedia(query)
    const handler = (e: MediaQueryListEvent) => setMatches(e.matches)
    setMatches(mq.matches)
    mq.addEventListener('change', handler)
    return () => mq.removeEventListener('change', handler)
  }, [query])
  return matches
}

/**
 * NOTE: currently unused. The chat transcript does NOT use this — it is
 * virtualised with react-virtuoso and follows the bottom via `followOutput`
 * (see ChatPage). Do not wire this to a Virtuoso list: two things setting
 * scrollTop fight each other and the viewport judders. This remains for plain
 * (non-virtualised) scroll containers.
 *
 * Keep an element pinned to the bottom as `dep` grows (a streaming transcript),
 * but ONLY while the user is at the bottom. Scroll up and auto-follow stops so
 * you can read; scroll back to the bottom and it resumes — like a chat or a
 * terminal.
 *
 * Wire the returned `onScroll` onto the scroll container as a React handler and
 * `ref` on the same element:
 *   <div ref={ref} onScroll={onScroll} className="overflow-y-auto"> … </div>
 *
 * There is deliberately no "ignore our own programmatic scroll" flag: after we
 * scroll to the bottom the container simply IS at the bottom, so the handler
 * keeps `stick` true. The bug that flag caused was the opposite — it swallowed
 * the user's real scroll-up and never let following turn off.
 */
export function useStickyScroll<T>(dep: T) {
  const ref = useRef<HTMLDivElement>(null)
  // Follow the bottom? Starts true so a restored transcript opens at the newest
  // message. A ref, not state, so updating it never re-renders.
  const stick = useRef(true)
  const didInitial = useRef(false)

  const onScroll = useCallback(() => {
    const el = ref.current
    if (!el) return
    stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80
  }, [])

  useEffect(() => {
    const el = ref.current
    if (!el || !stick.current) return
    if (!didInitial.current) {
      // First paint (restored history): jump instantly, no animation.
      didInitial.current = true
      el.scrollTop = el.scrollHeight
      return
    }
    // Live updates: follow the newest content.
    el.scrollTop = el.scrollHeight
  }, [dep])

  return { ref, onScroll }
}
