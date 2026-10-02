import { call, onState } from './bridge.js'

const $ = (id) => document.getElementById(id)
const el = (tag, attrs = {}, ...kids) => {
  const n = document.createElement(tag)
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') n.className = v
    else if (k.startsWith('on')) n.addEventListener(k.slice(2), v)
    else if (v !== false && v != null) n.setAttribute(k, v === true ? '' : v)
  }
  for (const kid of kids.flat()) if (kid != null) n.append(kid)
  return n
}

const STATUS_TEXT = {
  online: 'Online',
  offline: 'Unreachable',
  running: 'Running',
  stopped: 'Not running',
  signed_out: 'Signed out',
  not_installed: 'Not installed',
  unknown: 'Checking…',
}

let state = null
let busy = false
let confirmRemove = ''
let repairing = ''
let tabTouched = false

function thisMachine() {
  return state && state.platform === 'desktop-macos' ? 'This Mac' : 'This computer'
}

function render() {
  if (!state) return
  const notice = $('notice')
  notice.hidden = !state.notice
  notice.textContent = state.notice || ''

  const opening = state.phase === 'opening'
  $('opening').hidden = !opening
  if (opening) $('opening-text').textContent = `Connecting to ${state.current_name || 'Antares'}…`

  renderRows()
  renderLocal()
  $('tab-local').textContent = thisMachine()
  $('foot').textContent = `Antares desktop ${state.version || ''} · ${state.machine || ''}`
}

function renderRows() {
  const rows = $('rows')
  rows.replaceChildren()
  const list = state.connections || []
  $('saved-meta').textContent = list.length ? `${list.length} saved` : ''
  if (!list.length) {
    rows.append(el('li', { class: 'empty' }, 'Nothing saved yet. Add a connection below.'))
    return
  }
  for (const c of list) {
    const where = c.mode === 'local'
      ? (state.local.running && state.local.url ? state.local.url : 'starts when you open it')
      : c.url
    const actions = el('div', { class: 'row-actions' })
    if (confirmRemove === c.id) {
      actions.append(
        el('button', { class: 'btn quiet', onclick: () => { confirmRemove = ''; render() } }, 'Keep'),
        el('button', { class: 'btn danger', onclick: () => remove(c.id) }, 'Remove'),
      )
    } else {
      actions.append(el('button', { class: 'btn quiet', title: 'Remove this connection', onclick: () => { confirmRemove = c.id; repairing = ''; render() } }, 'Remove'))
      if (c.status === 'signed_out') {
        actions.append(el('button', { class: 'btn primary', onclick: () => startRepair(c) }, 'Pair again'))
      } else {
        actions.append(el('button', { class: 'btn primary', disabled: busy, onclick: () => open(c.id) }, 'Open'))
      }
    }
    const li = el('li', { class: 'row' },
      el('span', { class: `dot ${c.status}`, title: STATUS_TEXT[c.status] || c.status }),
      el('div', { class: 'row-main' },
        el('div', { class: 'row-name' }, c.name, el('span', { class: 'chip' }, c.mode === 'local' ? 'Local' : 'Remote')),
        el('div', { class: 'row-sub' }, `${STATUS_TEXT[c.status] || c.status} · ${where}`),
      ),
      actions,
    )
    rows.append(li)
    if (repairing === c.id && c.mode === 'remote') {
      const input = el('input', { class: 'field', type: 'password', placeholder: 'Dashboard password', 'aria-label': `Password for ${c.name}` })
      const err = el('p', { class: 'error', role: 'alert' })
      const go = el('button', { class: 'btn primary' }, 'Pair')
      const form = el('form', { class: 'inline' }, input, go)
      form.addEventListener('submit', async (e) => {
        e.preventDefault()
        go.disabled = true
        try {
          await call('Repair', c.id, input.value)
          repairing = ''
          await open(c.id)
        } catch (ex) {
          err.textContent = ex.message
          go.disabled = false
        }
      })
      rows.append(el('li', {}, form, err))
      queueMicrotask(() => input.focus())
    }
  }
}

function renderLocal() {
  const l = state.local || {}
  const dot = $('local-dot')
  const sub = $('local-sub')
  const btn = $('add-local')
  dot.className = 'dot ' + (!l.installed && !l.running ? 'not_installed' : l.running ? 'running' : 'stopped')
  const src = { bundled: 'bundled with this app', path: 'on your PATH', 'local-bin': 'in ~/.local/bin' }[l.source] || ''
  if (!l.installed && !l.running) {
    sub.textContent = 'The antares command was not found. Install Antares, or use a build of this app that bundles it.'
  } else {
    const parts = []
    parts.push(l.installed ? `Installed ${src}` : 'Installed')
    parts.push(l.running ? `running at ${l.url}` : 'not running (it starts when you open it)')
    sub.textContent = parts.join(' · ')
  }
  $('local-title').textContent = `Antares on ${thisMachine() === 'This Mac' ? 'this Mac' : 'this computer'}`
  btn.textContent = l.saved ? `${thisMachine()} is already saved` : `Add ${thisMachine()}`
  btn.disabled = busy || l.saved || !l.installed
}

async function refresh() {
  try {
    state = await call('Refresh')
    render()
  } catch (e) {
    console.error(e)
  }
}

async function open(id) {
  busy = true
  render()
  try {
    await call('Open', id)
  } catch {
    // The notice from State explains it.
  } finally {
    busy = false
    state = await call('State')
    render()
  }
}

async function remove(id) {
  confirmRemove = ''
  try {
    await call('Remove', id)
  } catch (e) {
    alert(e.message)
  }
  state = await call('State')
  render()
}

function startRepair(c) {
  confirmRemove = ''
  if (c.mode === 'local') {
    call('Repair', c.id, '').then(() => open(c.id)).catch((e) => {
      state.notice = e.message
      render()
    })
    return
  }
  repairing = repairing === c.id ? '' : c.id
  render()
}

function selectTab(which) {
  const remote = which === 'remote'
  $('tab-remote').setAttribute('aria-selected', String(remote))
  $('tab-local').setAttribute('aria-selected', String(!remote))
  // Roving tabindex: only the selected tab is in the tab order.
  $('tab-remote').tabIndex = remote ? 0 : -1
  $('tab-local').tabIndex = remote ? -1 : 0
  $('pane-remote').hidden = !remote
  $('pane-local').hidden = remote
}

$('tab-remote').addEventListener('click', () => { tabTouched = true; selectTab('remote') })
$('tab-local').addEventListener('click', () => { tabTouched = true; selectTab('local') })
document.querySelector('.tabs').addEventListener('keydown', (e) => {
  if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
  tabTouched = true
  const toLocal = $('tab-remote').getAttribute('aria-selected') === 'true'
  selectTab(toLocal ? 'local' : 'remote')
  $(toLocal ? 'tab-local' : 'tab-remote').focus()
})

$('pane-remote').addEventListener('submit', async (e) => {
  e.preventDefault()
  const f = e.currentTarget
  const btn = f.querySelector('button[type=submit]')
  const err = $('remote-error')
  err.textContent = ''
  btn.disabled = true
  btn.textContent = 'Checking the server…'
  try {
    const conn = await call('AddRemote', f.name.value, f.url.value, f.password.value)
    f.reset()
    await open(conn.id)
  } catch (ex) {
    err.textContent = ex.message
  } finally {
    btn.disabled = false
    btn.textContent = 'Connect'
  }
})

$('add-local').addEventListener('click', async () => {
  const err = $('local-error')
  err.textContent = ''
  busy = true
  render()
  try {
    const conn = await call('AddLocal')
    busy = false
    await open(conn.id)
  } catch (ex) {
    err.textContent = ex.message
  } finally {
    busy = false
    state = await call('State')
    render()
  }
})

onState((s) => {
  state = s
  render()
})

selectTab('remote')
document.querySelector('main').focus({ preventScroll: true })
state = await call('State')
render()
// First run on a machine with antares: start on the local tab.
if (!tabTouched && state.local && !state.local.saved && state.connections.length === 0 && state.local.installed) selectTab('local')
refresh()
setInterval(() => { if (!document.hidden) refresh() }, 15000)
