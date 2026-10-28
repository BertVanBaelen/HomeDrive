import { api, type Details, type Node, type SortKey } from './api'
import { navigate } from './router'

// Opening a folder zooms into it: its thumbnail grows to fill the file area
// and fades, the items shown in the thumbnail fly from their mini tiles to
// their places, and the rest come out of the folder's centre. Going back up,
// the folder you left shrinks back into its card. Short and without bounce;
// nothing moves for people who ask for reduced motion.

const duration = 420
const easing = 'cubic-bezier(0.2, 0.8, 0.2, 1)'

export function reducedMotion(): boolean {
  return window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
}

interface Zoom {
  folderId: string
  from: DOMRect
  minis: Map<string, DOMRect>
}

let zoom: Zoom | null = null
let left: { folderId: string; at: number } | null = null

// A folder fetched while the zoom starts, so its view can draw at once.
interface Prefetched {
  details: Details
  items: Node[]
  sort: string
}
const prefetched = new Map<string, Prefetched>()

const sortKey = (key: SortKey, desc: boolean) => `${key}:${desc}`

// takePrefetched hands a folder view the contents fetched for its zoom, if
// they were fetched with the same sort.
export function takePrefetched(folderId: string, key: SortKey, desc: boolean): Prefetched | null {
  const p = prefetched.get(folderId)
  prefetched.delete(folderId)
  return p && p.sort === sortKey(key, desc) ? p : null
}

// openFolder notes where the folder's card is, fetches the folder, then
// navigates. The zoom itself plays when the new view has its cards.
export async function openFolder(folderId: string, href: string, key: SortKey, desc: boolean) {
  const card = document.querySelector<HTMLElement>(`.grid [data-node="${CSS.escape(folderId)}"]`)
  if (card && !reducedMotion()) {
    const thumb = card.querySelector<HTMLElement>('[data-thumb]') ?? card
    zoom = {
      folderId,
      from: thumb.getBoundingClientRect(),
      minis: new Map(
        [...thumb.querySelectorAll<HTMLElement>('[data-mini]')].map((el) => [el.dataset.mini!, el.getBoundingClientRect()]),
      ),
    }
    card.classList.add('is-opening')
  }
  const fetched = Promise.all([api.node(folderId), api.children(folderId, key, desc)])
    .then(([details, items]) => {
      prefetched.set(folderId, { details, items, sort: sortKey(key, desc) })
    })
    .catch(() => {})
  // Don't keep the user waiting on a slow connection: go after 600 ms
  // whatever happens; the view then loads as usual.
  await Promise.race([fetched, new Promise((r) => setTimeout(r, 600))])
  navigate(href)
}

// leaveFolder is called as a folder view goes away, for the zoom out.
export function leaveFolder(folderId: string) {
  left = { folderId, at: Date.now() }
}

// playFolderMotion runs the zoom in (when this folder was just opened from
// its card) or the zoom out (when we came up from one of its folders). It
// must run after the cards are in the DOM and before the browser paints.
// It reports whether it zoomed in, so the caller can skip the usual
// entrance of the cards.
export function playFolderMotion(folderId: string, files: HTMLElement): boolean {
  const z = zoom
  zoom = null
  const l = left
  left = null
  if (reducedMotion()) return false
  if (z && z.folderId === folderId) {
    zoomIn(z, files)
    return true
  }
  if (l && Date.now() - l.at < 2000) {
    const card = files.querySelector<HTMLElement>(`.grid [data-node="${CSS.escape(l.folderId)}"]`)
    if (card) zoomOut(card)
  }
  return false
}

function zoomIn(z: Zoom, files: HTMLElement) {
  window.scrollTo({ top: 0 })
  const area = files.getBoundingClientRect()
  const target = { left: area.left, top: area.top, width: area.width, height: Math.min(area.height, window.innerHeight - area.top) }

  // The folder's thumbnail grows into the file area and fades.
  const ghost = document.createElement('div')
  ghost.className = 'zoom-ghost'
  Object.assign(ghost.style, {
    left: `${z.from.left}px`,
    top: `${z.from.top}px`,
    width: `${z.from.width}px`,
    height: `${z.from.height}px`,
  })
  document.body.appendChild(ghost)
  ghost.animate(
    [
      { transform: 'none', opacity: 0.9 },
      {
        transform: `translate(${target.left - z.from.left}px, ${target.top - z.from.top}px) scale(${target.width / z.from.width}, ${Math.max(target.height, 1) / z.from.height})`,
        opacity: 0,
      },
    ],
    { duration, easing },
  ).onfinish = () => ghost.remove()

  const cx = z.from.left + z.from.width / 2
  const cy = z.from.top + z.from.height / 2
  let rest = 0
  for (const el of files.querySelectorAll<HTMLElement>('.grid [data-node]')) {
    const r = el.getBoundingClientRect()
    if (r.top > window.innerHeight) continue // off screen: nothing to see
    const mini = z.minis.get(el.dataset.node!)
    if (mini) {
      // Grow from its tile inside the folder thumbnail.
      el.animate(
        [
          {
            transformOrigin: '0 0',
            transform: `translate(${mini.left - r.left}px, ${mini.top - r.top}px) scale(${mini.width / r.width}, ${mini.height / r.height})`,
          },
          { transformOrigin: '0 0', transform: 'none' },
        ],
        { duration, easing, fill: 'backwards' },
      )
    } else {
      // Come out of the folder's centre, slightly after the first four.
      el.animate(
        [
          { transform: `translate(${cx - (r.left + r.width / 2)}px, ${cy - (r.top + r.height / 2)}px) scale(0.3)`, opacity: 0 },
          { transform: 'none', opacity: 1 },
        ],
        { duration, easing, delay: 60 + Math.min(rest++, 8) * 15, fill: 'backwards' },
      )
    }
  }
}

function zoomOut(card: HTMLElement) {
  card.animate(
    [
      { transform: 'scale(1.6)', opacity: 0 },
      { transform: 'none', opacity: 1 },
    ],
    { duration, easing, fill: 'backwards' },
  )
}
