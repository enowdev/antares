/** Typed client for the Antares HTTP API. */

/**
 * True when an error is the "set a dashboard password first" gate. The marker
 * in the body distinguishes it from other 428 answers — Cursor reports a
 * missing integration credential with the same status but its own message.
 */
export function isDashboardPasswordRequired(e: unknown): boolean {
  if (!(e instanceof ApiError) || e.status !== 428) return false
  const body = e.body
  if (typeof body !== 'object' || body === null || !('error' in body)) return true
  return (body as { error: unknown }).error === 'dashboard_password_required'
}

export class ApiError extends Error {
  status: number
  body?: unknown

  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

const TOKEN_KEY = 'antares.token'

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? ''
}

export function setToken(token: string) {
  if (token) localStorage.setItem(TOKEN_KEY, token)
  else localStorage.removeItem(TOKEN_KEY)
}

function authHeaders(): Record<string, string> {
  const token = getToken()
  return token ? { Authorization: `Bearer ${token}` } : {}
}

/**
 * Turn a non-2xx response into an ApiError that keeps the parsed body. The
 * status and the server's own `error` string are what the UI needs to explain a
 * 409 busy session, a 429 with retry-after, an auth failure, or a stale model.
 */
async function responseError(res: Response): Promise<ApiError> {
  const text = await res.text().catch(() => '')
  let body: unknown = text
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      /* keep raw text */
    }
  }
  const message =
    typeof body === 'object' && body !== null && 'error' in body
      ? String((body as { error: unknown }).error)
      : typeof body === 'string' && body.trim() !== ''
        ? body
        : res.statusText || `HTTP ${res.status}`
  return new ApiError(res.status, message, body)
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(`/api${path}`, {
    ...init,
    // Include the dashboard login cookie on every request.
    credentials: 'include',
    headers: {
      ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      ...authHeaders(),
      ...(init.headers as Record<string, string>),
    },
  })

  // A dashboard-login 401 means the session expired or was never established;
  // bounce to the login screen unless we are already on it or authenticating.
  if (res.status === 401 && !path.startsWith('/auth/')) {
    if (typeof window !== 'undefined' && window.location.pathname !== '/login') {
      window.location.assign('/login')
    }
  }

  if (!res.ok) throw await responseError(res)

  const text = await res.text()
  let body: unknown = text
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      /* keep raw text */
    }
  }
  return body as T
}

export const get = <T,>(path: string) => api<T>(path)
export const post = <T,>(path: string, data?: unknown) =>
  api<T>(path, { method: 'POST', body: data === undefined ? undefined : JSON.stringify(data) })
export const put = <T,>(path: string, data?: unknown) =>
  api<T>(path, { method: 'PUT', body: data === undefined ? undefined : JSON.stringify(data) })
export const del = <T,>(path: string) => api<T>(path, { method: 'DELETE' })

/** Fetch a file endpoint (with auth) and trigger a browser download. */
/**
 * Build a same-origin URL to an API GET endpoint with the auth token in the
 * query string. Use for <img src>, <video>, and download links — places that
 * cannot set an Authorization header. The token middleware accepts ?token=.
 */
export function authedUrl(path: string): string {
  const token = getToken()
  if (!token) return `/api${path}`
  return `/api${path}${path.includes('?') ? '&' : '?'}token=${encodeURIComponent(token)}`
}

export async function downloadFile(path: string, filename: string): Promise<void> {
  const res = await fetch(`/api${path}`, { headers: { ...authHeaders() } })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new ApiError(res.status, text || res.statusText)
  }
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

/* ---------- Streaming ---------- */

export interface StreamEvent {
  type: string
  [key: string]: unknown
}

/**
 * POST a request and consume the server-sent event stream it returns.
 * Returns an abort function.
 */
export function streamPost(
  path: string,
  data: unknown,
  onEvent: (event: StreamEvent) => void,
  onError?: (err: Error) => void,
  onDone?: () => void,
): () => void {
  const controller = new AbortController()

  ;(async () => {
    try {
      const res = await fetch(`/api${path}`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify(data),
        signal: controller.signal,
      })
      // A refused turn answers with the same JSON error envelope as `api`, so
      // the composer can tell a busy session from a rate limit or a stale
      // model instead of showing a bare status line.
      if (!res.ok) throw await responseError(res)
      if (!res.body) throw new ApiError(res.status, 'the response had no body')

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })

        // SSE frames are separated by a blank line.
        let idx: number
        while ((idx = buffer.indexOf('\n\n')) !== -1) {
          const frame = buffer.slice(0, idx)
          buffer = buffer.slice(idx + 2)
          const payload = frame
            .split('\n')
            .filter((l) => l.startsWith('data:'))
            .map((l) => l.slice(5).replace(/^ /, ''))
            .join('\n')
          if (!payload || payload === '[DONE]') continue
          try {
            onEvent(JSON.parse(payload) as StreamEvent)
          } catch {
            /* ignore malformed frame */
          }
        }
      }
      onDone?.()
    } catch (err) {
      if ((err as Error).name === 'AbortError') {
        onDone?.()
        return
      }
      onError?.(err as Error)
    }
  })()

  return () => controller.abort()
}

/**
 * Subscribe to a GET SSE endpoint (attach, logs, swarm, …).
 *
 * Uses fetch (not EventSource) so we can send the dashboard session cookie
 * (`credentials: 'include'`) and an Authorization header. EventSource cannot
 * set headers and after a daemon restart used to 401-loop when only a stale
 * in-memory login map existed — the cookie was valid but attach failed.
 */
export function streamGet(
  path: string,
  onEvent: (event: StreamEvent) => void,
  onError?: (err: Error) => void,
  onDone?: () => void,
): () => void {
  const controller = new AbortController()
  const token = getToken()
  // Keep ?token= for allowlisted stream paths when a bearer is configured;
  // cookie auth alone is enough for password-locked dashboards.
  const url = `/api${path}${path.includes('?') ? '&' : '?'}${token ? `token=${encodeURIComponent(token)}` : ''}`

  ;(async () => {
    try {
      const res = await fetch(url, {
        method: 'GET',
        credentials: 'include',
        headers: { ...authHeaders(), Accept: 'text/event-stream' },
        signal: controller.signal,
      })
      if (!res.ok) throw await responseError(res)
      if (!res.body) throw new ApiError(res.status, 'the response had no body')

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })

        let idx: number
        while ((idx = buffer.indexOf('\n\n')) !== -1) {
          const frame = buffer.slice(0, idx)
          buffer = buffer.slice(idx + 2)
          // Ignore SSE comments (keepalives: ": keepalive").
          const payload = frame
            .split('\n')
            .filter((l) => l.startsWith('data:'))
            .map((l) => l.slice(5).replace(/^ /, ''))
            .join('\n')
          if (!payload || payload === '[DONE]') continue
          try {
            onEvent(JSON.parse(payload) as StreamEvent)
          } catch {
            /* ignore malformed frame */
          }
        }
      }
      onDone?.()
    } catch (err) {
      if ((err as Error).name === 'AbortError') {
        onDone?.()
        return
      }
      onError?.(err as Error)
    }
  })()

  return () => controller.abort()
}
