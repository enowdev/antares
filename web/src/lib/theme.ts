import { useEffect, useSyncExternalStore } from 'react'

export type Theme = 'dark' | 'light'

const KEY = 'antares.theme'
const listeners = new Set<() => void>()

function read(): Theme {
  try {
    const raw = localStorage.getItem(KEY)
    return raw && JSON.parse(raw) === 'light' ? 'light' : 'dark'
  } catch {
    return 'dark'
  }
}

let current: Theme = typeof window === 'undefined' ? 'dark' : read()

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark')
  document.documentElement.style.colorScheme = theme
}

export function setTheme(theme: Theme) {
  current = theme
  try {
    localStorage.setItem(KEY, JSON.stringify(theme))
  } catch {
    /* quota or private mode: the choice lasts for this page load */
  }
  applyTheme(theme)
  for (const l of listeners) l()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/**
 * The dashboard theme, shared by every caller: the mobile header button and
 * Settings › Appearance stay in step because they read one store instead of
 * each holding its own copy of localStorage.
 */
export function useTheme() {
  const theme = useSyncExternalStore(subscribe, () => current, () => 'dark' as Theme)
  useEffect(() => applyTheme(theme), [theme])
  return {
    theme,
    setTheme,
    toggleTheme: () => setTheme(current === 'dark' ? 'light' : 'dark'),
  }
}
