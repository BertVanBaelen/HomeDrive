import { useEffect, useRef, useState } from 'react'
import { getDocument, GlobalWorkerOptions, type PDFDocumentProxy } from 'pdfjs-dist'
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import { useT } from '../i18n'

// pdf.js parses the file in a worker served from our own origin, so the
// Content-Security-Policy stays as strict as for the rest of the app. This
// module is loaded only when a PDF is opened.
GlobalWorkerOptions.workerSrc = workerUrl

const maxWidth = 900

// PdfView shows every page of a PDF, one below the other. Pages are drawn
// when they come near the screen.
export default function PdfView({ url }: { url: string }) {
  const t = useT()
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null)
  const [failed, setFailed] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)

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
    const ro = new ResizeObserver(() => setWidth(Math.min(maxWidth, el.clientWidth)))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  return (
    <div ref={box} className="pdf">
      {failed && <p className="viewer__none">{t('viewer.pdfFailed')}</p>}
      {!doc && !failed && <p className="pdf__loading">{t('loading')}</p>}
      {doc &&
        width > 0 &&
        Array.from({ length: doc.numPages }, (_, i) => <PdfPage key={i} doc={doc} n={i + 1} width={width} />)}
    </div>
  )
}

function PdfPage({ doc, n, width }: { doc: PDFDocumentProxy; n: number; width: number }) {
  const t = useT()
  const canvas = useRef<HTMLCanvasElement>(null)
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
    doc.getPage(n).then((page) => {
      if (cancelled || !canvas.current) return
      const base = page.getViewport({ scale: 1 })
      setRatio(base.height / base.width)
      const dpr = window.devicePixelRatio || 1
      const viewport = page.getViewport({ scale: (width / base.width) * dpr })
      const el = canvas.current
      el.width = Math.floor(viewport.width)
      el.height = Math.floor(viewport.height)
      const render = page.render({ canvas: el, viewport })
      task = render
      render.promise.catch(() => {})
    })
    return () => {
      cancelled = true
      task?.cancel()
    }
  }, [doc, n, width, visible])

  return (
    <canvas ref={canvas} className="pdf__page" aria-label={t('viewer.pdfPage', { n })} role="img"
      style={{ width: `${width}px`, height: `${Math.round(width * ratio)}px` }} />
  )
}
