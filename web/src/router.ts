import { useEffect, useRef, useSyncExternalStore } from 'react'

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
//   /login, /login/homeassistant (add-on option), /setup, /recover, /welcome, /save

const listeners = new Set<() => void>()
const activeLayers = new Set<string>()

// History. Every entry this app makes carries hd in history.state: doc
// identifies this page load (entries from another load of the app can't be
// trusted to be where the trail says), i is the entry's position, and layer
// counts the overlays (dialogs, the viewer) open on top of the page. The
// trail in sessionStorage remembers which path each position shows, so
// "back to the folder above" can be a real Back when that folder is right
// behind, and the share page can return to Home without growing history.

interface Hd {
  doc: string
  i: number
  layer: number
  token?: string
}

interface Trail {
  doc: string
  paths: Record<string, string>
}

const trailKey = 'hd.trail'
const trailSpan = 200

function randomId(): string {
  return Math.random().toString(36).slice(2) + Date.now().toString(36)
}

function hdState(): Hd | null {
  const hd = (history.state as { hd?: Partial<Hd> } | null)?.hd
  if (!hd || typeof hd.doc !== 'string' || typeof hd.i !== 'number' || typeof hd.layer !== 'number') return null
  return hd as Hd
}

function readTrail(): Trail | null {
  try {
    const t = JSON.parse(sessionStorage.getItem(trailKey) ?? 'null')
    return t && typeof t.doc === 'string' && t.paths && typeof t.paths === 'object' ? t : null
  } catch {
    return null
  }
}

function writeTrail(t: Trail) {
  try {
    sessionStorage.setItem(trailKey, JSON.stringify(t))
  } catch {
    // Private browsing can disable session storage; Back still works.
  }
}

// record notes that position i shows path. A new entry (push) ends what
// was ahead of it.
function record(hd: Hd, path: string, push: boolean) {
  const t = readTrail()
  const paths = t && t.doc === hd.doc ? t.paths : {}
  paths[hd.i] = path
  for (const k of Object.keys(paths)) {
    const n = Number(k)
    if ((push && n > hd.i) || n < hd.i - trailSpan) delete paths[k]
  }
  writeTrail({ doc: hd.doc, paths })
}

function here(): string {
  return location.pathname + location.search
}

function current(): Hd {
  let hd = hdState()
  if (!hd) {
    hd = { doc: randomId(), i: 0, layer: 0 }
    history.replaceState({ ...(history.state ?? {}), hd }, '', location.href)
    record(hd, here(), true)
  }
  return hd
}

current()

// A reload or a Forward into an old overlay entry has no React overlay that
// can answer it. Turn that entry into an ordinary page entry, otherwise the
// next navigate() mistakes it for a live layer and replaces instead of
// pushing.
function normalizeOrphanLayer() {
  const hd = hdState()
  if (!hd || hd.layer === 0 || (hd.token && activeLayers.has(hd.token))) return
  // Forward can resurrect the editor entry after it was closed while the
  // viewer beneath it is still mounted. Don't rewrite a still-live stack:
  // step straight back to its active lower layer.
  if (activeLayers.size) {
    history.back()
    return
  }
  const next: Hd = { doc: hd.doc, i: hd.i, layer: 0 }
  history.replaceState({ hd: next }, '', location.href)
  record(next, here(), false)
}

normalizeOrphanLayer()
window.addEventListener('popstate', () => queueMicrotask(normalizeOrphanLayer))

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

// navigate opens path. From an overlay, the overlay's entry becomes the new
// page, so Back from there doesn't stop at a page without its overlay.
export function navigate(path: string, replace = false) {
  if (path === here()) return
  const cur = current()
  if (replace || cur.layer > 0) {
    const hd: Hd = { doc: cur.doc, i: cur.i, layer: 0 }
    history.replaceState({ hd }, '', path)
    record(hd, path, false)
  } else {
    const hd: Hd = { doc: cur.doc, i: cur.i + 1, layer: 0 }
    history.pushState({ hd }, '', path)
    record(hd, path, true)
  }
  listeners.forEach((fn) => fn())
}

// pathBehind is the path of the entry n steps back, when it is known.
function pathBehind(cur: Hd, n: number): string | undefined {
  const t = readTrail()
  return t && t.doc === cur.doc ? t.paths[cur.i - n] : undefined
}

// goUp goes to target, the folder above: a real Back when it is right
// behind, else in place of this entry.
export function goUp(target: string) {
  const cur = current()
  if (cur.layer === 0 && pathBehind(cur, 1) === target) history.back()
  else navigate(target, true)
}

// goTop goes to path (Home) by going back to it when it is behind, else in
// place of this entry, so history doesn't grow.
export function goTop(path = '/') {
  const cur = current()
  for (let n = 1; n <= Math.min(cur.i, trailSpan); n++) {
    if (pathBehind(cur, n) === path) {
      history.go(-n)
      return
    }
  }
  navigate(path, true)
}

// useBackLayer gives an overlay (dialog, viewer) an entry in history while
// open, so Back closes it before it leaves the page. Closed another way, it
// takes its entry away again. Changing key after a Back gives the overlay a
// new entry, for overlays that may stay open after all.
export function useBackLayer(open: boolean, close: () => void, key = 0) {
  const closeRef = useRef(close)
  closeRef.current = close
  useEffect(() => {
    if (!open) return
    const cur = current()
    const depth = cur.layer + 1
    const token = randomId()
    const hd: Hd = { doc: cur.doc, i: cur.i + 1, layer: depth, token }
    activeLayers.add(token)
    history.pushState({ hd }, '')
    record(hd, here(), true)
    let popped = false
    const pop = () => {
      if ((hdState()?.layer ?? 0) < depth) {
        popped = true
        activeLayers.delete(token)
        closeRef.current()
      }
    }
    window.addEventListener('popstate', pop)
    return () => {
      window.removeEventListener('popstate', pop)
      activeLayers.delete(token)
      if (!popped && hdState()?.token === token) history.back()
    }
  }, [open, key])
}

// closeBackLayer closes the overlay on top through history when it has an
// entry there, else directly.
export function closeBackLayer(close: () => void) {
  if ((hdState()?.layer ?? 0) > 0) history.back()
  else close()
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
  | { name: 'homeassistantLogin' }
  | { name: 'setup' }
  | { name: 'recover' }
  | { name: 'welcome' }
  | { name: 'save' }
  | { name: 'link'; token: string; nodeId: string | null }

export function parseRoute(full: string): Route {
  const url = new URL(full, location.origin)
  const path = url.pathname
  if (path === '/login/homeassistant') return { name: 'homeassistantLogin' }
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
