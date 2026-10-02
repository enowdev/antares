import { call, onState } from './bridge.js'

const $ = (id) => document.getElementById(id)

function render(st) {
  if (!st) return
  const name = st.current_name || 'Antares'
  const local = (st.connections || []).find((c) => c.id === st.current && c.mode === 'local')
  $('what').textContent = local
    ? `Antares on this machine stopped answering. Retry starts it again; the window comes back where you were.`
    : `Lost contact with ${name}. Trying again every few seconds; the window comes back where you were.`
}

$('retry').addEventListener('click', async () => {
  const btn = $('retry')
  btn.disabled = true
  btn.textContent = 'Retrying…'
  $('error').textContent = ''
  try {
    await call('Retry')
  } catch (e) {
    $('error').textContent = e.message
  } finally {
    btn.disabled = false
    btn.textContent = 'Retry now'
  }
})

$('switch').addEventListener('click', () => call('SwitchConnection'))

onState(render)
document.querySelector('main').focus({ preventScroll: true })
render(await call('State'))
