// smoke-run orchestrates the full end-to-end dashboard smoke:
//
//   1. Allocate a fresh temp ANTARES_HOME + workspace so nothing under the
//      developer's real ~/.antares is read or written.
//   2. Bind three loopback ports:
//        - antares (dashboard + API)
//        - openai-compatible provider stub (deterministic /v1/models catalogue
//          plus an empty non-billing /v1/chat/completions in case something
//          the walk touches actually issues a request)
//   3. Run bin/smokefixture (cmd/smokefixture) to hash the synthetic
//      password, write config.yaml, and seed the SMOKE_FIXTURE_SESSION_ID
//      row through the real store.Open (schema stays honest).
//   4. Launch bin/antares --foreground with a scrubbed env — only
//      PATH / HOME / TMPDIR / SHELL / LANG / the ANTARES_* pointers survive;
//      inherited provider keys (OPENAI_API_KEY, ANTHROPIC_API_KEY, …) never
//      reach the child, so a stale key on the developer's box cannot silently
//      turn the smoke run into a real billable call.
//   5. Poll GET /api/health until 200, then hand SMOKE_BASE / SMOKE_TOKEN /
//      SMOKE_PASSWORD to scripts/smoke.mjs (the shared route walker owned by
//      the SmokeUpgrade worker) and forward its exit code.
//   6. On success, failure, SIGINT, SIGTERM, or thrown exception: terminate
//      the owned children (SIGTERM → wait → SIGKILL), await their exit, then
//      rm -rf the temp dirs we made. Nothing outside those dirs is touched.
//
// Bun-only. No new node_modules. All child processes come from
// node:child_process; the provider stub uses Bun.serve.

import { spawn } from 'node:child_process'
import { mkdtempSync, rmSync, existsSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'node:net'
import { setTimeout as sleep } from 'node:timers/promises'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '..')
const ANTARES_BIN = process.env.ANTARES_BIN
  ? resolve(process.env.ANTARES_BIN)
  : join(REPO, 'bin', 'antares')
// The fixture helper is its own binary (cmd/smokefixture): keeps the
// shipped `antares` CLI free of a "seed my smoke config" subcommand while
// still reusing config.Default / store.Open so schema drift is caught here
// the same day it lands in production.
const FIXTURE_BIN = process.env.SMOKE_FIXTURE_BIN
  ? resolve(process.env.SMOKE_FIXTURE_BIN)
  : join(REPO, 'bin', 'smokefixture')

if (!existsSync(ANTARES_BIN)) {
  console.error(`smoke-run: ${ANTARES_BIN} does not exist. Run \`make build\` first.`)
  process.exit(2)
}
if (!existsSync(FIXTURE_BIN)) {
  console.error(`smoke-run: ${FIXTURE_BIN} does not exist. Run \`make build\` first (it now builds cmd/smokefixture too).`)
  process.exit(2)
}

// Allowlisted env — inherited provider keys never reach the fixture or the
// server. TMPDIR is preserved because the smoke.mjs Chrome profile lands
// under mkdtemp(tmpdir()). SHELL/LANG keep the terminal tool from
// bootstrap-panicking if it inspects them; PATH is required to spawn
// Chromium; HOME is required for Chromium's own user-data-dir fallback if
// somehow it decides to write outside --user-data-dir.
function scrubbedEnv(extra) {
  const keep = ['PATH', 'HOME', 'TMPDIR', 'TEMP', 'TMP', 'SHELL', 'LANG', 'LC_ALL', 'LC_CTYPE', 'TZ', 'USER', 'LOGNAME']
  const base = {}
  for (const k of keep) {
    if (process.env[k] !== undefined) base[k] = process.env[k]
  }
  return { ...base, ...extra }
}

// ---- port allocation --------------------------------------------------------

// Ask the kernel for a free port then release it. Racy in principle; the
// window is small and the smoke run is not concurrent with anything else on
// loopback in the CI job. Any collision surfaces as a bind failure the smoke
// caller can retry.
function pickPort() {
  return new Promise((res, rej) => {
    const s = createServer()
    s.on('error', rej)
    s.listen(0, '127.0.0.1', () => {
      const p = s.address().port
      s.close(() => res(p))
    })
  })
}

// ---- lifecycle bookkeeping --------------------------------------------------

const cleaners = []
let cleaning = false
function onExit(fn) { cleaners.push(fn) }
async function cleanup(reason) {
  if (cleaning) return
  cleaning = true
  if (reason) console.log(`smoke-run: cleaning up (${reason})`)
  for (const fn of cleaners.slice().reverse()) {
    try { await fn() } catch (e) { console.error(`  cleanup: ${e?.message ?? e}`) }
  }
}

function terminate(child, label) {
  return async () => {
    if (!child || child.exitCode !== null || child.signalCode) return
    const exited = new Promise((res) => child.once('exit', res))
    try { child.kill('SIGTERM') } catch {}
    const timeout = sleep(5000).then(() => 'timeout')
    const outcome = await Promise.race([exited, timeout])
    if (outcome === 'timeout') {
      console.error(`smoke-run: ${label} did not exit on SIGTERM; sending SIGKILL`)
      try { child.kill('SIGKILL') } catch {}
      await exited
    }
  }
}

function rmDir(path) {
  return () => { try { rmSync(path, { recursive: true, force: true }) } catch {} }
}

process.on('SIGINT', async () => { await cleanup('SIGINT'); process.exit(130) })
process.on('SIGTERM', async () => { await cleanup('SIGTERM'); process.exit(143) })
process.on('uncaughtException', async (err) => {
  console.error('smoke-run: uncaughtException', err)
  await cleanup('uncaughtException')
  process.exit(1)
})

// ---- provider stub ----------------------------------------------------------

// Deterministic openai-compatible surface so the dashboard's provider probe
// (and any accidental chat call) never leaves loopback. /v1/models returns a
// single fixed catalogue; every other path — including chat/completions,
// embeddings, images, tokenize — returns HTTP 500 and increments a counter
// the orchestrator asserts is zero at the end. The dashboard route walk is
// strictly read-only; a chat request would mean a page fired a background
// completion, which is exactly the surprise this catches. Faking a successful
// assistant turn would hide that.
function startProviderStub(port) {
  const modelName = 'smoke/gpt-smoke'
  const catalogue = {
    object: 'list',
    data: [{ id: modelName, object: 'model', created: 0, owned_by: 'smoke' }],
  }
  const rejected = []
  const stub = Bun.serve({
    hostname: '127.0.0.1',
    port,
    fetch(req) {
      const url = new URL(req.url)
      if (req.method === 'GET' && (url.pathname === '/v1/models' || url.pathname === '/models')) {
        return Response.json(catalogue)
      }
      rejected.push(`${req.method} ${url.pathname}`)
      return Response.json(
        { error: { message: 'smoke stub: read-only walk must not invoke the model', type: 'smoke_forbidden' } },
        { status: 500 },
      )
    },
  })
  onExit(async () => { try { stub.stop(true) } catch {} })
  return {
    modelName,
    base: `http://127.0.0.1:${port}/v1`,
    rejectedCalls: () => rejected.slice(),
  }
}

// ---- child helpers ----------------------------------------------------------

function runFixture(env, args) {
  return new Promise((res, rej) => {
    const child = spawn(FIXTURE_BIN, args, {
      env,
      cwd: REPO,
      stdio: ['ignore', 'inherit', 'inherit'],
    })
    child.on('error', rej)
    child.on('exit', (code, signal) => {
      if (code === 0) return res()
      rej(new Error(`smokefixture exited code=${code} signal=${signal ?? ''}`))
    })
  })
}

async function waitForHealth(base, timeoutMs) {
  const deadline = Date.now() + timeoutMs
  let lastErr = ''
  while (Date.now() < deadline) {
    try {
      const r = await fetch(`${base}/api/health`, { headers: { 'user-agent': 'smoke-run/health' } })
      if (r.ok) return
      lastErr = `HTTP ${r.status}`
    } catch (e) {
      lastErr = e?.message ?? String(e)
    }
    await sleep(200)
  }
  throw new Error(`server did not become healthy within ${timeoutMs}ms (${lastErr})`)
}

// ---- main -------------------------------------------------------------------

async function main() {
  const antaresHome = mkdtempSync(join(tmpdir(), 'antares-smoke-home-'))
  const workspace = join(antaresHome, 'workspace')
  onExit(rmDir(antaresHome))

  const [serverPort, stubPort] = await Promise.all([pickPort(), pickPort()])
  const stub = startProviderStub(stubPort)

  // Synthetic credentials. The password is only ever passed to the fixture
  // (hashed into config) and to smoke.mjs via SMOKE_PASSWORD; the token is
  // written into config.server.auth_token and handed to smoke.mjs via
  // SMOKE_TOKEN so the LoginGate resolves without hitting /api/auth/login
  // twice.
  const password = 'smoke-password-' + Math.random().toString(36).slice(2, 10)
  const token = 'smoke-token-' + Math.random().toString(36).slice(2, 12)

  const baseEnv = scrubbedEnv({
    ANTARES_HOME: antaresHome,
    ANTARES_PORT: String(serverPort),
    ANTARES_HOST: '127.0.0.1',
  })

  await runFixture(baseEnv, [
    '--home', antaresHome,
    '--host', '127.0.0.1',
    '--port', String(serverPort),
    '--password', password,
    '--token', token,
    '--model-base', stub.base,
    '--model', stub.modelName,
    '--workspace', workspace,
    '--session-id', 'smoke-session',
  ])

  const server = spawn(ANTARES_BIN, ['--foreground'], {
    env: baseEnv,
    cwd: REPO,
    stdio: ['ignore', 'inherit', 'inherit'],
  })
  onExit(terminate(server, 'antares'))
  server.on('exit', (code, signal) => {
    if (!cleaning) {
      console.error(`smoke-run: antares exited early code=${code} signal=${signal}`)
    }
  })

  const base = `http://127.0.0.1:${serverPort}`
  try {
    await waitForHealth(base, 30_000)
  } catch (e) {
    await cleanup('health-timeout')
    console.error(`smoke-run: ${e.message}`)
    process.exit(1)
  }

  const smokeChild = spawn(process.execPath, ['scripts/smoke.mjs'], {
    env: scrubbedEnv({
      SMOKE_BASE: base,
      SMOKE_TOKEN: token,
      SMOKE_PASSWORD: password,
      // Allow the stub's host in case something requests /v1/models from the
      // dashboard side rather than the server proxying it. (Currently unused;
      // future-proofs the outbound-host check in smoke.mjs.)
      SMOKE_ALLOW_HOSTS: `127.0.0.1:${stubPort}`,
      CHROME: process.env.CHROME ?? '',
    }),
    cwd: REPO,
    stdio: ['ignore', 'inherit', 'inherit'],
  })
  onExit(terminate(smokeChild, 'smoke.mjs'))

  const walkExit = await new Promise((res) => smokeChild.on('exit', (code, signal) => {
    res(code ?? (signal ? 128 : 1))
  }))

  // The dashboard route walk is strictly read-only: any request that reached
  // the provider stub past /v1/models means a page fired a background chat /
  // embedding / image call, which is a real bug the walk was supposed to
  // catch. Fail the whole smoke even if every route rendered "ok".
  const stray = stub.rejectedCalls()
  if (stray.length > 0) {
    console.error(`smoke-run: read-only walk hit the model ${stray.length} time(s):`)
    for (const call of stray) console.error(`  ${call}`)
  }

  await cleanup('done')
  process.exit(walkExit || (stray.length > 0 ? 1 : 0))
}

main().catch(async (err) => {
  console.error('smoke-run:', err?.stack ?? err)
  await cleanup('error')
  process.exit(1)
})
