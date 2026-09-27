// Loads every dashboard route in a real browser and fails on console errors,
// unhandled exceptions, blank roots, redirects, or horizontal overflow.
//
// This exists because neither `tsc` nor `go vet` can see the two bugs that hurt
// most here: a hook called from the wrong place, and the server bouncing SPA
// routes to "./". Both shipped clean type checks and both broke the whole app.
//
// The route list is the shared manifest in web/src/lib/routeManifest.ts — same
// source that routes.ts consumes at runtime — so a new page cannot slip past
// smoke without also slipping past the app itself. Session-parameter aliases
// are materialized with SMOKE_FIXTURE_SESSION_ID; the caller (Makefile / Main)
// seeds that session row directly in SQLite before booting the server so
// /c/<id> loads a real conversation instead of a 404.
//
// smokeRedirects() lists every retired path (/cron, /config, …) and bare hub
// root (/agent) with the canonical path it must land on; each is visited too,
// so a dropped redirect fails here instead of silently bouncing to "/".
//
// The caller MUST supply SMOKE_BASE pointing at an isolated server it owns.
// SMOKE_TOKEN, when present, is written to localStorage as antares.token so
// the LoginGate resolves. SMOKE_PASSWORD (never stored) is exchanged for a
// real session via POST /api/auth/login before the route walk begins.
//
// Runs on Bun (native .ts import) — no ts-node fallback, no transpile step.
//
//   make smoke        build, serve, seed fixtures, and check every route
//   SMOKE_BASE=http://127.0.0.1:5173 bun scripts/smoke.mjs   against your own server
import { spawn } from 'node:child_process'
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'
import { pathToFileURL } from 'node:url'

// Bun imports .ts natively. No fallback: this script is invoked via `bun`
// from the Makefile and installing ts-node just for a smoke helper would be
// pointless drift.
const manifestModule = await import(
  pathToFileURL(new URL('../web/src/lib/routeManifest.ts', import.meta.url).pathname).href
)
const smokeTargets = manifestModule.smokeTargets
const smokeRedirects = manifestModule.smokeRedirects
const SMOKE_FIXTURE_SESSION_ID = manifestModule.SMOKE_FIXTURE_SESSION_ID
if (typeof smokeTargets !== 'function') {
  console.error('routeManifest.ts did not export smokeTargets()')
  process.exit(2)
}
if (typeof smokeRedirects !== 'function') {
  console.error('routeManifest.ts did not export smokeRedirects()')
  process.exit(2)
}

const BASE = process.env.SMOKE_BASE
if (!BASE) {
  console.error('SMOKE_BASE is required — point it at an isolated smoke server')
  console.error('the caller controls (Main / Makefile seeds the DB and gates).')
  process.exit(2)
}
const TOKEN = process.env.SMOKE_TOKEN ?? ''
const PASSWORD = process.env.SMOKE_PASSWORD ?? ''

// Standard Chromium locations across macOS / Linux / Windows. CHROME wins.
const CHROME_CANDIDATES = [
  process.env.CHROME,
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/Applications/Chromium.app/Contents/MacOS/Chromium',
  '/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary',
  '/usr/bin/google-chrome',
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser',
  '/home/enow/chrome-testing/chrome-linux64/chrome',
  'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  'C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe',
].filter(Boolean)
const CHROME = CHROME_CANDIDATES.find((p) => {
  try { return existsSync(p) } catch { return false }
}) ?? 'chromium'

const USER_DATA_DIR = mkdtempSync(join(tmpdir(), 'antares-smoke-chrome-'))
const CDP_TIMEOUT_MS = 30_000

const TARGETS = smokeTargets()
const REDIRECTS = smokeRedirects()
// Old bookmarks carry state (/vps?x=1#y); every redirect must keep it.
const REDIRECT_SUFFIX = '?smoke=1#smoke'
// Standalone gates redirect once auth is short-circuited: /login and /setup
// both bounce to "/" when a valid session/token is present. We still visit
// them so the surfaces themselves are exercised, but the finalPath check
// accepts either the literal route OR "/" for these two — never a blanket
// exemption, and any other landing is a failure.
const STANDALONE_GATES = new Set(['/login', '/setup'])
const STANDALONE_ACCEPTED = new Set(['/'])

const VIEWPORTS = [
  { name: 'desktop', width: 1440, height: 900, mobile: false },
  { name: 'mobile', width: 390, height: 844, mobile: true },
]

let chrome
try {
  chrome = spawn(CHROME, [
    '--headless=new',
    '--disable-gpu',
    '--no-sandbox',
    '--disable-dev-shm-usage',
    '--hide-scrollbars',
    `--user-data-dir=${USER_DATA_DIR}`,
    // Port 0 lets Chrome pick a free port and write it to DevToolsActivePort
    // in the (fresh) profile dir. This avoids attaching to some other Chrome
    // already listening on a guessed port — the previous random-port scheme
    // could hit a running instance and quietly drive the user's real browser.
    '--remote-debugging-port=0',
    'about:blank',
  ], { stdio: 'ignore' })
} catch (err) {
  console.error(`failed to spawn ${CHROME}: ${err?.message ?? err}`)
  process.exit(2)
}
chrome.on('error', (err) => {
  console.error(`chrome process error: ${err?.message ?? err}`)
})

let cleaned = false
const cleanup = () => {
  if (cleaned) return
  cleaned = true
  try { chrome?.kill() } catch {}
  try { rmSync(USER_DATA_DIR, { recursive: true, force: true }) } catch {}
}
process.on('exit', cleanup)
process.on('SIGINT', () => { cleanup(); process.exit(130) })
process.on('SIGTERM', () => { cleanup(); process.exit(143) })

// Chrome writes "<port>\n<browserWsPath>" to DevToolsActivePort inside the
// user-data-dir once the CDP endpoint is ready. Read from there instead of
// guessing a port; also proves we're talking to *our* Chrome, not one that
// happened to be listening.
const activePortFile = join(USER_DATA_DIR, 'DevToolsActivePort')
let debugPort = 0
let wsURL = ''
{
  const deadline = Date.now() + CDP_TIMEOUT_MS
  while (Date.now() < deadline) {
    if (chrome.exitCode !== null) {
      console.error(`chrome exited early with code ${chrome.exitCode}`)
      cleanup()
      process.exit(1)
    }
    try {
      const raw = readFileSync(activePortFile, 'utf8')
      const [portLine, wsPath] = raw.split('\n')
      const port = Number(portLine)
      if (Number.isFinite(port) && port > 0) {
        debugPort = port
        if (wsPath && wsPath.startsWith('/')) {
          wsURL = `ws://127.0.0.1:${port}${wsPath}`
        } else {
          // Older Chromium builds omit the ws path; fall back to /json/version.
          const r = await fetch(`http://127.0.0.1:${port}/json/version`)
          wsURL = (await r.json()).webSocketDebuggerUrl
        }
        if (wsURL) break
      }
    } catch { /* file not written yet */ }
    await sleep(150)
  }
}
if (!wsURL) {
  console.error(`chrome did not expose CDP within ${CDP_TIMEOUT_MS}ms (DevToolsActivePort missing)`)
  cleanup()
  process.exit(1)
}

const ws = new WebSocket(wsURL)
await new Promise((res, rej) => {
  const t = setTimeout(() => rej(new Error('CDP websocket open timeout')), CDP_TIMEOUT_MS)
  ws.onopen = () => { clearTimeout(t); res() }
  ws.onerror = (e) => { clearTimeout(t); rej(e) }
})

let id = 0
const pending = new Map()
let events = []
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data)
  if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id) }
  else if (msg.method) events.push(msg)
}
const send = (method, params = {}, sessionId) => {
  const n = ++id
  return new Promise((res, rej) => {
    const t = setTimeout(() => {
      pending.delete(n)
      rej(new Error(`CDP timeout: ${method}`))
    }, CDP_TIMEOUT_MS)
    pending.set(n, (msg) => { clearTimeout(t); res(msg) })
    ws.send(JSON.stringify({ id: n, method, params, ...(sessionId ? { sessionId } : {}) }))
  })
}

const { result: target } = await send('Target.createTarget', { url: 'about:blank' })
const { result: attached } = await send('Target.attachToTarget', { targetId: target.targetId, flatten: true })
const sid = attached.sessionId
await send('Runtime.enable', {}, sid)
await send('Page.enable', {}, sid)
await send('Network.enable', {}, sid)

const evaluate = async (expression) => {
  const { result } = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }, sid)
  return result?.result?.value
}

// Chat resumes to the last-visited session via sessionStorage['antares:last-session'],
// which means our own /c/smoke-session visit poisons subsequent '/' and '/login'
// checks — they redirect to /c/smoke-session and look like REDIRECT failures
// when it's really the product's resume path doing exactly what it should.
// Wipe just that key on every new document so canonical routes are tested in
// isolation; other tab state (auth token, ui prefs) is preserved.
const CLEAR_RESUME = `try { sessionStorage.removeItem('antares:last-session'); } catch {}`
await send('Page.addScriptToEvaluateOnNewDocument', { source: CLEAR_RESUME }, sid)

// Real auth. SMOKE_TOKEN is treated as an existing session token and just
// dropped into localStorage; SMOKE_PASSWORD is exchanged for one via the
// backend's /api/auth/login route, exactly like the LoginGate does. The
// password never touches storage and never appears in log output — only the
// resulting HTTP status is checked.
const origin = new URL(BASE).origin
if (TOKEN || PASSWORD) {
  // Land on the app origin so window.fetch and localStorage target the right
  // security context. about:blank has no origin.
  await send('Page.navigate', { url: origin + '/' }, sid)
  // Wait for a document to exist (Chrome accepts Runtime.evaluate on the
  // preload frame, but localStorage needs a real origin).
  for (let i = 0; i < 40; i++) {
    const ready = await evaluate('document.readyState === "complete" || document.readyState === "interactive"')
    if (ready) break
    await sleep(100)
  }

  if (TOKEN) {
    await send('Runtime.evaluate', {
      expression: `localStorage.setItem('antares.token', ${JSON.stringify(TOKEN)})`,
    }, sid)
    await send('Page.addScriptToEvaluateOnNewDocument', {
      source: `try { localStorage.setItem('antares.token', ${JSON.stringify(TOKEN)}); } catch {}`,
    }, sid)
  }

  if (PASSWORD) {
    // Post credentials from inside the page so cookies/CORS behave exactly
    // like a real browser login. The password is embedded once, in the
    // evaluate expression, and never logged.
    const loginExpr = `
      (async () => {
        const r = await fetch('/api/auth/login', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          credentials: 'include',
          body: JSON.stringify({ password: ${JSON.stringify(PASSWORD)} }),
        });
        let token = '';
        try {
          const j = await r.clone().json();
          token = j?.token ?? j?.session ?? '';
        } catch {}
        return { status: r.status, token };
      })()
    `
    const login = await evaluate(loginExpr)
    if (!login || login.status < 200 || login.status >= 300) {
      console.error(`login failed: HTTP ${login?.status ?? '?'}`)
      cleanup()
      process.exit(1)
    }
    if (login.token) {
      await send('Runtime.evaluate', {
        expression: `localStorage.setItem('antares.token', ${JSON.stringify(login.token)})`,
      }, sid)
      await send('Page.addScriptToEvaluateOnNewDocument', {
        source: `try { localStorage.setItem('antares.token', ${JSON.stringify(login.token)}); } catch {}`,
      }, sid)
    }
  }
}

const setViewport = (v) => send('Emulation.setDeviceMetricsOverride', {
  width: v.width,
  height: v.height,
  deviceScaleFactor: 1,
  mobile: v.mobile,
}, sid)

// Any network request that leaves the smoke origin is treated as an outbound
// LLM/API call — the smoke server MUST be self-contained. Loopback (127.*,
// localhost, ::1) is trusted because the fixture's openai-compatible stub
// lives there; SMOKE_ALLOW_HOSTS is a comma-separated allow-list for anything
// else Main wants to permit (e.g. a stub bound to a named host).
const outboundHosts = new Set()
const baseHost = new URL(BASE).host
const extraAllowedHosts = new Set(
  (process.env.SMOKE_ALLOW_HOSTS ?? '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean),
)
const isLoopbackHost = (h) =>
  h === 'localhost' || h.startsWith('127.') || h === '[::1]' || h === '::1'

// `expected` is where the browser must end up: the route itself for a
// canonical path, or the destination for a legacy redirect. `suffix` (a query
// and hash) is appended to the request and must survive the redirect intact.
const routeCheck = async (route, viewport, expected = route, suffix = '') => {
  events = []
  outboundHosts.clear()
  const problems = []
  const url = BASE + route + suffix

  try {
    await send('Page.navigate', { url }, sid)
  } catch (e) {
    return [`NAVIGATE: ${e.message}`]
  }

  let textLen = 0
  let finalPath = ''
  for (let i = 0; i < 60; i++) {
    await sleep(200)
    textLen = (await evaluate('document.getElementById("root")?.innerText?.trim().length ?? 0')) ?? 0
    if (textLen > 20) break
  }
  await sleep(500) // let late effects surface
  finalPath = (await evaluate('location.pathname')) ?? ''

  for (const e of events) {
    if (e.method === 'Runtime.exceptionThrown') {
      const d = e.params.exceptionDetails
      problems.push('EXCEPTION: ' + (d.exception?.description || d.text || '').split('\n')[0])
    } else if (e.method === 'Runtime.consoleAPICalled' && e.params.type === 'error') {
      const text = e.params.args.map((a) => a.value ?? a.description ?? '').join(' ')
      if (/Failed to load resource|net::ERR|Failed to fetch/.test(text)) continue
      problems.push('CONSOLE: ' + text.split('\n')[0].slice(0, 200))
    } else if (e.method === 'Network.requestWillBeSent') {
      try {
        const host = new URL(e.params.request.url).host
        if (!host) continue
        if (host === baseHost) continue
        if (isLoopbackHost(host)) continue
        if (extraAllowedHosts.has(host)) continue
        outboundHosts.add(host)
      } catch {}
    }
  }

  if (textLen <= 20) problems.push(`BLANK: #root rendered ${textLen} chars`)

  // Landing check: literal match, or — for the two standalone gates — one of
  // the explicitly permitted post-auth destinations. No blanket exemption:
  // /login bouncing to /sessions would still be a failure.
  const gateOK = STANDALONE_GATES.has(route) && STANDALONE_ACCEPTED.has(finalPath)
  if (finalPath !== expected && !gateOK) {
    problems.push(`REDIRECT: expected ${expected} got ${finalPath}`)
  }
  if (suffix) {
    const kept = (await evaluate('location.search + location.hash')) ?? ''
    if (kept !== suffix) problems.push(`REDIRECT: dropped query/hash, expected ${suffix} got ${kept || '(none)'}`)
  }

  // Horizontal overflow: catches layouts that break out of the mobile viewport.
  const overflow = await evaluate(
    'Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) - window.innerWidth'
  )
  if (typeof overflow === 'number' && overflow > 1) {
    problems.push(`OVERFLOW: ${overflow}px past viewport (${viewport.width}w)`)
  }

  if (outboundHosts.size) {
    problems.push(`OUTBOUND: ${[...outboundHosts].join(',')}`)
  }

  return problems
}

let failures = 0
let checks = 0
console.log(`smoke  base=${BASE}  chrome=${CHROME}  cdp=${debugPort}  fixture=${SMOKE_FIXTURE_SESSION_ID}`)
for (const viewport of VIEWPORTS) {
  await setViewport(viewport)
  console.log(`\n== ${viewport.name} ${viewport.width}x${viewport.height} ==`)
  for (const route of TARGETS) {
    checks++
    const problems = await routeCheck(route, viewport)
    if (problems.length) {
      failures++
      console.log(`FAIL ${route}`)
      for (const p of problems) console.log(`       ${p}`)
    } else {
      console.log(`ok   ${route}`)
    }
  }
  for (const { from, to } of REDIRECTS) {
    checks++
    const problems = await routeCheck(from, viewport, to, REDIRECT_SUFFIX)
    if (problems.length) {
      failures++
      console.log(`FAIL ${from} -> ${to}`)
      for (const p of problems) console.log(`       ${p}`)
    } else {
      console.log(`ok   ${from} -> ${to}`)
    }
  }
}

ws.close()
cleanup()
console.log(
  failures
    ? `\n${failures}/${checks} route check(s) with problems`
    : `\nAll ${checks} route checks clean across ${VIEWPORTS.length} viewports`,
)
process.exit(failures ? 1 : 0)
