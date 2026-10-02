// Bridge to the Go side (package main, type Shell). Inside the app the Wails
// runtime is served at /wails/runtime.js. Opened as a plain file (design
// previews, screenshots) it falls back to demo data: add ?demo=<scenario>.
const SERVICE = 'main.Shell.'

let runtime = null
try {
  runtime = await import('/wails/runtime.js')
} catch {
  runtime = null
}

const demo = new URLSearchParams(location.search).get('demo')

export async function call(method, ...args) {
  if (runtime && !demo) {
    try {
      return await runtime.Call.ByName(SERVICE + method, ...args)
    } catch (err) {
      throw new Error(cleanError(err))
    }
  }
  const { demoCall } = await import('./demo.js')
  return demoCall(demo || 'list', method, ...args)
}

export function onState(cb) {
  if (runtime && !demo) runtime.Events.On('antares:state', (ev) => cb(ev.data))
}

function cleanError(err) {
  let msg = err && err.message ? err.message : String(err)
  try {
    const parsed = JSON.parse(msg)
    if (parsed && parsed.message) msg = parsed.message
  } catch {}
  return msg
}
