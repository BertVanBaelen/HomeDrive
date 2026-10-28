// Homedrive service worker.
//
// It does two things:
// 1. Receives files from the Android share sheet (manifest share_target),
//    keeps them in IndexedDB and opens the save page.
// 2. Caches the app shell so the PWA starts without a network.
//
// It never caches /api/ responses or file contents: a lost phone must not
// hold documents.
import { putShare } from '../shared/shareStore'

declare const self: ServiceWorkerGlobalScope
declare const __BUILD_ID__: string

const CACHE_PREFIX = 'homedrive-shell-'
const SHELL_CACHE = CACHE_PREFIX + __BUILD_ID__

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(SHELL_CACHE)
      .then((cache) => cache.add('/'))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys.filter((k) => k.startsWith(CACHE_PREFIX) && k !== SHELL_CACHE).map((k) => caches.delete(k)),
        ),
      )
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return

  if (req.method === 'POST' && url.pathname === '/share-target') {
    event.respondWith(receiveShare(req))
    return
  }
  if (req.method !== 'GET' || url.pathname.startsWith('/api/')) return

  if (req.mode === 'navigate') {
    event.respondWith(shell(req))
  } else if (url.pathname.startsWith('/assets/')) {
    event.respondWith(asset(req))
  }
})

function savePage(query: string): Response {
  return Response.redirect(new URL('/save?' + query, self.location.origin).href, 303)
}

async function receiveShare(req: Request): Promise<Response> {
  try {
    const form = await req.formData()
    const files = form.getAll('files').filter((f): f is File => f instanceof File && f.size > 0)
    const field = (name: string) => {
      const v = form.get(name)
      return typeof v === 'string' ? v : ''
    }
    const id = await putShare({
      files,
      title: field('title'),
      text: field('text'),
      url: field('url'),
      receivedAt: Date.now(),
    })
    return savePage('share=' + encodeURIComponent(id))
  } catch {
    return savePage('status=failed')
  }
}

// Navigations: network first, so a logged-out or updated app is never stale;
// the cached shell only serves when offline. All app routes render the same
// index.html, so it's cached under '/'.
async function shell(req: Request): Promise<Response> {
  const cache = await caches.open(SHELL_CACHE)
  try {
    const res = await fetch(req)
    if (res.ok && res.type === 'basic' && res.headers.get('Content-Type')?.startsWith('text/html')) {
      await cache.put('/', res.clone())
    }
    return res
  } catch {
    return (await cache.match('/')) ?? Response.error()
  }
}

// Hashed build assets never change, so the cache can answer first.
async function asset(req: Request): Promise<Response> {
  const cache = await caches.open(SHELL_CACHE)
  const hit = await cache.match(req)
  if (hit) return hit
  const res = await fetch(req)
  if (res.ok) await cache.put(req, res.clone())
  return res
}
