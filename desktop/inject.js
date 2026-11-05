// Injected by the shell after every navigation (Wails WebviewWindowOptions.JS).
// On a dashboard page: links and window.open to other origins go to the
// system browser, and the current path is reported so a reconnect can return
// to it. Talks to Go through window._wails.invoke (Wails' raw message bridge).
(() => {
  if (window.__antaresShell) return
  window.__antaresShell = true

  const send = (msg) => {
    try {
      if (window._wails && window._wails.invoke) window._wails.invoke(msg)
      else if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.external) window.webkit.messageHandlers.external.postMessage(msg)
      else if (window.chrome && window.chrome.webview) window.chrome.webview.postMessage(msg)
    } catch (e) {}
  }

  // An absolute URL to hand to the system browser, or null to stay here.
  const outside = (href) => {
    let u
    try { u = new URL(href, location.href) } catch (e) { return null }
    if (u.protocol === 'mailto:') return u.href
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return null
    return u.origin === location.origin ? null : u.href
  }

  document.addEventListener('click', (e) => {
    if (e.button !== 0 && e.type === 'click') return
    const a = e.target && e.target.closest ? e.target.closest('a[href]') : null
    if (!a) return
    const ext = outside(a.href)
    if (ext) {
      e.preventDefault()
      e.stopImmediatePropagation()
      send('antares:open:' + ext)
      return
    }
    // The webview opens no second windows: a same-origin new-tab link
    // (no download) opens here instead of doing nothing.
    if (a.target === '_blank' && !a.hasAttribute('download') && !e.defaultPrevented) {
      e.preventDefault()
      location.assign(a.href)
    }
  }, true)

  const nativeOpen = window.open
  window.open = function (href, target, features) {
    if (href) {
      const ext = outside(String(href))
      if (ext) {
        send('antares:open:' + ext)
        return null
      }
      try { location.assign(new URL(String(href), location.href).href) } catch (e) {}
      return null
    }
    return nativeOpen.apply(window, arguments)
  }

  if (location.protocol === 'wails:') return

  const report = () => send('antares:path:' + location.pathname + location.search + location.hash)
  for (const k of ['pushState', 'replaceState']) {
    const orig = history[k]
    history[k] = function () {
      const r = orig.apply(this, arguments)
      report()
      return r
    }
  }
  window.addEventListener('popstate', report)
  window.addEventListener('hashchange', report)
  report()
})()
