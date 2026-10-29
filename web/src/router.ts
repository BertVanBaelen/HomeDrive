import { useSyncExternalStore } from 'react'

// A tiny history router: the app only has a handful of routes.
//
//   /             My Files (top of the personal drive)
//   /d/<id>       the top of a shared drive
//   /shared       the shared drives (the phone's Shared tab)
//   /family       v1's family drive: goes to the first shared drive
//   /f/<id>       a folder on any drive
//   /recent, /starred, /trash, /search?q=…
//   /account, /users and /backup (admins)
//   /login, /setup, /recover, /welcome, /save

const listeners = new Set<() => void>()

function subscribe(fn: () => void) {
  listeners.add(fn)
  window.addEventListener('popstate', fn)
  return () => {
    listeners.delete(fn)
    window.removeEventListener('popstate', fn)
  }
}

// usePath is the current path with its query string.
export function usePath(): string {
  return useSyncExternalStore(subscribe, () => location.pathname + location.search)
}

export function navigate(path: string, replace = false) {
  if (path === location.pathname + location.search) return
  if (replace) history.replaceState(null, '', path)
  else history.pushState(null, '', path)
  listeners.forEach((fn) => fn())
}

export type ListName = 'recent' | 'starred' | 'search'

export type Route =
  | { name: 'files'; folderId: string | null }
  | { name: 'drive'; driveId: string }
  | { name: 'shared' }
  | { name: 'family' }
  | { name: 'list'; list: ListName; q: string }
  | { name: 'trash' }
  | { name: 'account' }
  | { name: 'users' }
  | { name: 'backup' }
  | { name: 'login' }
  | { name: 'setup' }
  | { name: 'recover' }
  | { name: 'welcome' }
  | { name: 'save' }

export function parseRoute(full: string): Route {
  const url = new URL(full, location.origin)
  const path = url.pathname
  switch (path) {
    case '/login':
    case '/setup':
    case '/recover':
    case '/welcome':
    case '/save':
    case '/trash':
    case '/shared':
    case '/family':
    case '/account':
    case '/users':
    case '/backup':
      return { name: path.slice(1) } as Route
    case '/recent':
    case '/starred':
      return { name: 'list', list: path.slice(1) as ListName, q: '' }
    case '/search':
      return { name: 'list', list: 'search', q: url.searchParams.get('q') ?? '' }
  }
  const m = /^\/f\/([0-9a-f-]{36})$/.exec(path)
  if (m) return { name: 'files', folderId: m[1] }
  const d = /^\/d\/([0-9a-f-]{36})$/.exec(path)
  if (d) return { name: 'drive', driveId: d[1] }
  return { name: 'files', folderId: null }
}

export function folderPath(id: string): string {
  return `/f/${id}`
}

export function drivePath(id: string): string {
  return `/d/${id}`
}
