import { useEffect, useRef, useState, type MouseEvent, type PointerEvent, type ReactNode } from 'react'
import { useT } from '../i18n'
import { clampPan, clampZoom, fit, zoomAtPoint, type View } from '../zoom'

type Gesture =
  | { kind: 'swipe'; x: number; y: number }
  | { kind: 'pan'; x: number; y: number; start: View }
  | { kind: 'pinch'; distance: number; start: View; mid: { x: number; y: number } }
  | null

const fromBar = (target: EventTarget | null) => target instanceof Element && !!target.closest('.zoom-bar')

// ZoomStage lets one picture be zoomed and moved: pinch, double-click or
// double-tap, Ctrl + wheel, the + − 0 keys and the zoom bar; zoomed in, it
// is dragged around. At fit, a sideways swipe goes to the previous or next
// file. Only the picture zooms, never the page. The viewer keys it per
// file, so every file starts at fit.
export function ZoomStage({ children, onPrev, onNext }: { children: ReactNode; onPrev?: () => void; onNext?: () => void }) {
  const t = useT()
  const stage = useRef<HTMLDivElement>(null)
  const content = useRef<HTMLDivElement>(null)
  const pointers = useRef(new Map<number, { x: number; y: number }>())
  const gesture = useRef<Gesture>(null)
  const [view, setView] = useState<View>(fit)
  const viewRef = useRef(view)
  viewRef.current = view

  // The picture's size at fit (the transform doesn't change offset sizes)
  // and the stage's, for keeping it on screen.
  const show = (v: View) => {
    const pic = content.current?.firstElementChild as HTMLElement | null
    setView(clampPan(v,
      { w: pic?.offsetWidth ?? 0, h: pic?.offsetHeight ?? 0 },
      { w: stage.current?.clientWidth ?? 0, h: stage.current?.clientHeight ?? 0 }))
  }

  function local(clientX: number, clientY: number) {
    const r = stage.current!.getBoundingClientRect()
    return { x: clientX - r.left - r.width / 2, y: clientY - r.top - r.height / 2 }
  }

  function zoomTo(scale: number, at = { x: 0, y: 0 }) {
    show(zoomAtPoint(viewRef.current, clampZoom(scale), at))
  }

  // Ctrl + wheel (and trackpad pinch, which browsers send as Ctrl + wheel)
  // needs a listener that isn't passive, or the whole page zooms too.
  // Safari's own pinch gesture is stopped for the same reason.
  useEffect(() => {
    const el = stage.current!
    const wheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return
      e.preventDefault()
      const at = local(e.clientX, e.clientY)
      zoomTo(viewRef.current.scale * Math.exp(-e.deltaY / 300), at)
    }
    const gestureStart = (e: Event) => e.preventDefault()
    el.addEventListener('wheel', wheel, { passive: false })
    el.addEventListener('gesturestart', gestureStart)
    return () => {
      el.removeEventListener('wheel', wheel)
      el.removeEventListener('gesturestart', gestureStart)
    }
  }, [])

  useEffect(() => {
    const keys = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null
      if (e.ctrlKey || e.metaKey || e.altKey || el?.closest('input, textarea, [contenteditable="true"]')) return
      if (e.key === '+' || e.key === '=') zoomTo(viewRef.current.scale + 0.5)
      else if (e.key === '-') zoomTo(viewRef.current.scale - 0.5)
      else if (e.key === '0') show(fit)
    }
    document.addEventListener('keydown', keys)
    return () => document.removeEventListener('keydown', keys)
  })

  function down(e: PointerEvent<HTMLDivElement>) {
    if (fromBar(e.target) || (e.pointerType === 'mouse' && e.button !== 0)) return
    e.currentTarget.setPointerCapture(e.pointerId)
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY })
    const ps = [...pointers.current.values()]
    if (ps.length === 1) {
      gesture.current = viewRef.current.scale > 1
        ? { kind: 'pan', x: e.clientX, y: e.clientY, start: viewRef.current }
        : { kind: 'swipe', x: e.clientX, y: e.clientY }
    } else if (ps.length === 2) {
      const [a, b] = ps
      gesture.current = {
        kind: 'pinch',
        distance: Math.hypot(a.x - b.x, a.y - b.y) || 1,
        start: viewRef.current,
        mid: local((a.x + b.x) / 2, (a.y + b.y) / 2),
      }
    }
  }

  function move(e: PointerEvent<HTMLDivElement>) {
    if (!pointers.current.has(e.pointerId)) return
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY })
    const g = gesture.current
    if (g?.kind === 'pinch' && pointers.current.size === 2) {
      const [a, b] = [...pointers.current.values()]
      const scale = clampZoom((g.start.scale * Math.hypot(a.x - b.x, a.y - b.y)) / g.distance)
      show(zoomAtPoint(g.start, scale, g.mid))
    } else if (g?.kind === 'pan') {
      show({ ...g.start, x: g.start.x + e.clientX - g.x, y: g.start.y + e.clientY - g.y })
    }
  }

  function up(e: PointerEvent<HTMLDivElement>) {
    if (!pointers.current.delete(e.pointerId)) return
    const g = gesture.current
    if (pointers.current.size > 0) {
      // From a pinch to one finger: carry on moving the picture.
      const [p] = [...pointers.current.values()]
      gesture.current = { kind: 'pan', x: p.x, y: p.y, start: viewRef.current }
      return
    }
    gesture.current = null
    if (g?.kind === 'swipe' && e.type === 'pointerup') {
      const dx = e.clientX - g.x
      const dy = e.clientY - g.y
      if (Math.abs(dx) > 70 && Math.abs(dx) > Math.abs(dy) * 1.5) (dx < 0 ? onNext : onPrev)?.()
    }
  }

  function double(e: MouseEvent<HTMLDivElement>) {
    if (fromBar(e.target)) return
    if (viewRef.current.scale > 1) show(fit)
    else zoomTo(2, local(e.clientX, e.clientY))
  }

  const zoomed = view.scale > 1
  return (
    <div ref={stage} className={'zoom-stage' + (zoomed ? ' zoom-stage--zoomed' : '')}
      onPointerDown={down} onPointerMove={move} onPointerUp={up} onPointerCancel={up} onDoubleClick={double}>
      <div ref={content} className="zoom-stage__content"
        style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})` }}>
        {children}
      </div>
      <div className="zoom-bar" role="group" aria-label={t('zoom.label')}>
        <button type="button" aria-label={t('zoom.out')} disabled={!zoomed} onClick={() => zoomTo(view.scale - 0.5)}>−</button>
        <span>{Math.round(view.scale * 100)}%</span>
        <button type="button" aria-label={t('zoom.in')} disabled={view.scale >= 3} onClick={() => zoomTo(view.scale + 0.5)}>+</button>
        <button type="button" disabled={!zoomed} onClick={() => show(fit)}>{t('zoom.fit')}</button>
      </div>
    </div>
  )
}
