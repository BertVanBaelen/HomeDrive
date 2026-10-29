import { useSyncExternalStore } from 'react'

// A tiny history router: the app only has a handful of routes.
//
//   /             My Files (top of the personal drive)
//   /d/<id>       the top of a shared drive
//   /shared       Shared with me (on a phone also the shared drives)
//   /family       v1's family drive: goes to the first shared drive
//   /f/<id>       a folder on any drive
//   /s/<token>    a public link's page, for visitors
//   /recent, /starred, /trash, /search?q=…
//   /storage (/backup, the admins' page in v1, goes there)
//   /account and /users (admins)
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
  | { name: 'storage' }
  | { name: 'account' }
  | { name: 'users' }
  | { name: 'backup' }
  | { name: 'login' }
  | { name: 'setup' }
  | { name: 'recover' }
  | { name: 'welcome' }
  | { name: 'save' }
  | { name: 'link'; token: string; nodeId: string | null }

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
    case '/storage':
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
  // A folder inside the link is ?n=<id>, so the address stays one link.
  const s = /^\/s\/([A-Za-z0-9_-]{22})$/.exec(path)
  if (s) {
    const n = url.searchParams.get('n')
    return { name: 'link', token: s[1], nodeId: n && /^[0-9a-f-]{36}$/.test(n) ? n : null }
  }
  return { name: 'files', folderId: null }
}

export function folderPath(id: string): string {
  return `/f/${id}`
}

export function drivePath(id: string): string {
  return `/d/${id}`
}

export function linkPath(token: string, nodeId?: string): string {
  return `/s/${token}${nodeId ? `?n=${nodeId}` : ''}`
}
