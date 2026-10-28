import { useSyncExternalStore } from 'react'

// A tiny history router: the app only has a handful of routes.
//
//   /             My Files (top of the personal drive)
//   /family       the family drive
//   /f/<id>       a folder on any drive
//   /trash        trash
//   /login, /setup, /save

const listeners = new Set<() => void>()

function subscribe(fn: () => void) {
  listeners.add(fn)
  window.addEventListener('popstate', fn)
  return () => {
    listeners.delete(fn)
    window.removeEventListener('popstate', fn)
  }
}

export function usePath(): string {
  return useSyncExternalStore(subscribe, () => location.pathname)
}

export function navigate(path: string, replace = false) {
  if (path === location.pathname + location.search) return
  if (replace) history.replaceState(null, '', path)
  else history.pushState(null, '', path)
  listeners.forEach((fn) => fn())
}

export type Route =
  | { name: 'files'; folderId: string | null }
  | { name: 'family' }
  | { name: 'trash' }
  | { name: 'login' }
  | { name: 'setup' }
  | { name: 'save' }

export function parseRoute(path: string): Route {
  if (path === '/login') return { name: 'login' }
  if (path === '/setup') return { name: 'setup' }
  if (path === '/save') return { name: 'save' }
  if (path === '/trash') return { name: 'trash' }
  if (path === '/family') return { name: 'family' }
  const m = /^\/f\/([0-9a-f-]{36})$/.exec(path)
  if (m) return { name: 'files', folderId: m[1] }
  return { name: 'files', folderId: null }
}

export function folderPath(id: string): string {
  return `/f/${id}`
}
