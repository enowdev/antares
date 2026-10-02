// Demo data for previewing the pages outside the app (?demo=list|empty|reconnect).
const base = {
  platform: 'desktop-macos', machine: 'MacBook Pro', version: '0.1.0',
  current: '', current_name: '', phase: 'connections', notice: '',
  local: { installed: true, binary: '/Applications/Antares.app/Contents/Resources/antares', source: 'bundled', running: true, url: 'http://127.0.0.1:8787', version: 'v0.5.0', saved: true },
  connections: [
    { id: 'conn_1', name: 'This Mac', mode: 'local', url: '', status: 'running', last: true },
    { id: 'conn_2', name: 'VPS', mode: 'remote', url: 'https://antares.example.com', status: 'online' },
    { id: 'conn_3', name: 'Home server', mode: 'remote', url: 'http://homebox.tail1234.ts.net:8787', status: 'signed_out' },
    { id: 'conn_4', name: 'Office', mode: 'remote', url: 'https://office.example.org', status: 'offline' },
  ],
}

const scenarios = {
  list: base,
  empty: { ...base, connections: [], local: { ...base.local, running: false, saved: false } },
  notice: { ...base, notice: 'Home server: signed out — this device was removed on the server; pair again' },
  reconnect: { ...base, current: 'conn_2', current_name: 'VPS', phase: 'reconnecting' },
}

export async function demoCall(scenario, method) {
  const st = scenarios[scenario] || base
  if (method === 'State' || method === 'Refresh') return st
  if (method === 'AddRemote') throw new Error('this server is too old for the desktop app — update Antares there')
  return null
}
