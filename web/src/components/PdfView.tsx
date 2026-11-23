import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { getDocument, GlobalWorkerOptions, type PDFDocumentProxy } from 'pdfjs-dist'
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import { useT } from '../i18n'

// pdf.js parses the file in a worker served from our own origin, so the
// Content-Security-Policy stays as strict as for the rest of the app. This
// module is loaded only when a PDF is opened.
GlobalWorkerOptions.workerSrc = workerUrl

const maxWidth = 900
const zoomMin = 0.5
const zoomMax = 3
// Browsers (iPhones first) refuse canvases much larger than this; past it,
// a page is drawn at a lower resolution instead of not at all.
const maxCanvasPixels = 16_000_000

// PdfView shows every page of a PDF, one below the other, with a zoom bar.
// Pages are drawn when they come near the screen, sharp at every zoom.
// Zooming keeps the point under the pointer (or the middle) in place.
export default function PdfView({ url }: { url: string }) {
  const t = useT()
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null)
  const [failed, setFailed] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  const [zoom, setZoom] = useState(1)
  const anchor = useRef<{ x: number; y: number; ratio: number } | null>(null)
  const pointers = useRef(new Map<number, { x: number; y: number }>())
  const pinch = useRef<{ distance: number; zoom: number; mid: { x: number; y: number } } | null>(null)

  useEffect(() => {
    const task = getDocument({ url, withCredentials: true })
    task.promise.then(setDoc, () => setFailed(true))
    return () => {
      task.destroy()
    }
  }, [url])

  // Follow the available width (window resizes, info drawer opening).
  useEffect(() => {
    const el = box.current
    if (!el) return
    const ro = new ResizeObserver(() => setWidth(Math.min(maxWidth, el.clientWidth - 24)))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  function zoomBy(f: (z: number) => number, at?: { x: number; y: number }) {
    const el = box.current
    setZoom((z) => {
      const n = Math.max(zoomMin, Math.min(zoomMax, Math.round(f(z) * 100) / 100))
      if (el && n !== z) {
        const point = at ?? { x: el.clientWidth / 2, y: el.clientHeight / 2 }
        if (anchor.current) anchor.current.ratio *= n / z
        else anchor.current = { ...point, ratio: n / z }
      }
      return n
    })
  }

  // Keep the anchor point where it was once the pages have their new size.
  useLayoutEffect(() => {
    const el = box.current
    const a = anchor.current
    anchor.current = null
    if (!el || !a) return
    el.scrollLeft = (el.scrollLeft + a.x) * a.ratio - a.x
    el.scrollTop = (el.scrollTop + a.y) * a.ratio - a.y
  }, [zoom])

  // Ctrl + wheel (and trackpad pinch), Safari gestures and two-finger
  // touch pinch zoom the document, not the browser page. Native listeners
  // are not passive.
  useEffect(() => {
    const el = box.current
    if (!el) return
    const wheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return
      e.preventDefault()
      const r = el.getBoundingClientRect()
      zoomBy((z) => z * Math.exp(-e.deltaY / 300), { x: e.clientX - r.left, y: e.clientY - r.top })
    }
    const point = (clientX: number, clientY: number) => {
      const r = el.getBoundingClientRect()
      return { x: clientX - r.left, y: clientY - r.top }
    }
    const down = (e: PointerEvent) => {
      if (e.pointerType !== 'touch') return
      pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY })
      el.setPointerCapture(e.pointerId)
      if (pointers.current.size !== 2) return
      e.preventDefault()
      const [a, b] = [...pointers.current.values()]
      pinch.current = {
        distance: Math.hypot(a.x - b.x, a.y - b.y) || 1,
        zoom,
        mid: point((a.x + b.x) / 2, (a.y + b.y) / 2),
      }
    }
    const move = (e: PointerEvent) => {
      if (!pointers.current.has(e.pointerId)) return
      pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY })
      const p = pinch.current
      if (!p || pointers.current.size !== 2) return
      e.preventDefault()
      const [a, b] = [...pointers.current.values()]
      const distance = Math.hypot(a.x - b.x, a.y - b.y) || 1
      zoomBy(() => p.zoom * distance / p.distance, p.mid)
    }
    const up = (e: PointerEvent) => {
      pointers.current.delete(e.pointerId)
      if (pointers.current.size < 2) pinch.current = null
    }
    const gesture = (e: Event) => e.preventDefault()
    el.addEventListener('wheel', wheel, { passive: false })
    el.addEventListener('pointerdown', down, { passive: false })
    el.addEventListener('pointermove', move, { passive: false })
    el.addEventListener('pointerup', up)
    el.addEventListener('pointercancel', up)
    el.addEventListener('gesturestart', gesture, { passive: false })
    el.addEventListener('gesturechange', gesture, { passive: false })
    return () => {
      el.removeEventListener('wheel', wheel)
      el.removeEventListener('pointerdown', down)
      el.removeEventListener('pointermove', move)
      el.removeEventListener('pointerup', up)
      el.removeEventListener('pointercancel', up)
      el.removeEventListener('gesturestart', gesture)
      el.removeEventListener('gesturechange', gesture)
    }
  })

  const pageWidth = Math.round(width * zoom)
  return (
    <div className="pdf-viewer">
      <div ref={box} className={'pdf' + (zoom > 1 ? ' pdf--zoomed' : '')}>
        {failed && <p className="viewer__none">{t('viewer.pdfFailed')}</p>}
        {!doc && !failed && <p className="pdf__loading">{t('loading')}</p>}
        {doc &&
          width > 0 &&
          Array.from({ length: doc.numPages }, (_, i) => <PdfPage key={i} doc={doc} n={i + 1} width={pageWidth} />)}
      </div>
      {doc && (
        <div className="pdf__zoom" role="group" aria-label={t('zoom.label')}>
          <button type="button" disabled={zoom <= zoomMin} aria-label={t('zoom.out')} onClick={() => zoomBy((z) => z - 0.25)}>−</button>
          <span>{Math.round(zoom * 100)}%</span>
          <button type="button" disabled={zoom >= zoomMax} aria-label={t('zoom.in')} onClick={() => zoomBy((z) => z + 0.25)}>+</button>
          <button type="button" disabled={zoom === 1} onClick={() => zoomBy(() => 1)}>{t('zoom.fit')}</button>
        </div>
      )}
    </div>
  )
}

function PdfPage({ doc, n, width }: { doc: PDFDocumentProxy; n: number; width: number }) {
  const t = useT()
  const canvas = useRef<HTMLCanvasElement>(null)
  const drawn = useRef(false)
  const [ratio, setRatio] = useState(1.414) // A4 until the page says otherwise
  const [visible, setVisible] = useState(n <= 2)

  useEffect(() => {
    const el = canvas.current
    if (!el || visible) return
    const io = new IntersectionObserver(([e]) => e.isIntersecting && setVisible(true), { rootMargin: '600px 0px' })
    io.observe(el)
    return () => io.disconnect()
  }, [visible])

  useEffect(() => {
    if (!visible) return
    let cancelled = false
    let task: { cancel: () => void } | null = null
    // Wait for a zoom to settle before drawing at the new size; the browser
    // stretches the old drawing meanwhile. The first drawing doesn't wait.
    const timer = setTimeout(() => {
      doc.getPage(n).then((page) => {
        if (cancelled || !canvas.current) return
        const base = page.getViewport({ scale: 1 })
        setRatio(base.height / base.width)
        let scale = (width / base.width) * (window.devicePixelRatio || 1)
        const pixels = base.width * base.height * scale * scale
        if (pixels > maxCanvasPixels) scale *= Math.sqrt(maxCanvasPixels / pixels)
        const viewport = page.getViewport({ scale })
        const el = canvas.current
        el.width = Math.floor(viewport.width)
        el.height = Math.floor(viewport.height)
        const render = page.render({ canvas: el, viewport })
        task = render
        render.promise.catch(() => {})
        drawn.current = true
      })
    }, drawn.current ? 150 : 0)
    return () => {
      cancelled = true
      clearTimeout(timer)
      task?.cancel()
    }
  }, [doc, n, width, visible])

  return (
    <canvas ref={canvas} className="pdf__page" aria-label={t('viewer.pdfPage', { n })} role="img"
      style={{ width: `${width}px`, height: `${Math.round(width * ratio)}px` }} />
  )
}
