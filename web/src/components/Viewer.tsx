import { lazy, Suspense, useEffect, useState } from 'react'
import { contentUrl, type Node } from '../api'
import { displayName, formatDateTime, formatSize } from '../format'
import { useLocale, useT, type MessageKey } from '../i18n'
import { fileTypes, Icon, icons } from './Icon'

// pdf.js is large; load it the first time a PDF is opened.
const PdfView = lazy(() => import('./PdfView'))

const showableImage = /\.(jpe?g|png|gif|webp|avif|bmp)$/i
const playableVideo = /\.(mp4|m4v|webm|mov)$/i
const maxTextBytes = 2_000_000

export function canPreview(n: Node): boolean {
  return (
    (n.type === 'image' && showableImage.test(n.name)) ||
    (n.type === 'video' && playableVideo.test(n.name)) ||
    n.type === 'audio' ||
    (n.type === 'pdf' && /\.pdf$/i.test(n.name)) ||
    ((n.type === 'text' || n.type === 'code') && n.size <= maxTextBytes)
  )
}

// Documents start at the top of the stage instead of its middle.
const isDoc = (n: Node) => canPreview(n) && (n.type === 'text' || n.type === 'code' || n.type === 'pdf')

// Viewer shows one file full screen on dark, with previous/next through the
// folder's files. PDFs get their viewer (pdf.js) in a later step.
export function Viewer({
  files,
  index,
  location,
  onIndex,
  onClose,
}: {
  files: Node[]
  index: number
  location: string
  onIndex: (i: number) => void
  onClose: () => void
}) {
  const t = useT()
  const locale = useLocale()
  const [info, setInfo] = useState(false)
  const n = files[index]
  const type = fileTypes[n.type]

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
      else if (e.key === 'ArrowLeft' && index > 0) onIndex(index - 1)
      else if (e.key === 'ArrowRight' && index < files.length - 1) onIndex(index + 1)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [index, files.length, onClose, onIndex])

  const sizeLabel = formatSize(n.size, locale)
  return (
    <div role="dialog" aria-modal="true" aria-label={displayName(n)} className="viewer">
      <div className="viewer__bar">
        <button type="button" aria-label={t('viewer.close')} className="dark-btn dark-btn--plain" onClick={onClose} autoFocus>
          <Icon d={icons.back} />
        </button>
        <Icon d={type.d} stroke={type.lc} />
        <div className="viewer__title">
          <strong>{displayName(n)}</strong>
          <span>{t(`short.${n.type}` as MessageKey)} · {sizeLabel}</span>
        </div>
        <a className="dark-btn" aria-label={t('viewer.download')} href={contentUrl(n.id, true)} download>
          <Icon d={icons.download} size={18} />
        </a>
        <button type="button" aria-label={t('viewer.info')} aria-pressed={info} className="dark-btn" onClick={() => setInfo(!info)}>
          <Icon d={icons.info} />
        </button>
      </div>
      <div className="viewer__main">
        <div className="viewer__stage">
          <div key={n.id}
            className={'viewer__content' + (isDoc(n) ? ' viewer__content--doc' : '')}>
            <Preview node={n} />
          </div>
          {index > 0 && (
            <button type="button" aria-label={t('viewer.prev')} className="dark-btn viewer__nav viewer__nav--prev" onClick={() => onIndex(index - 1)}>
              <Icon d={icons.chevronLeft} />
            </button>
          )}
          {index < files.length - 1 && (
            <button type="button" aria-label={t('viewer.next')} className="dark-btn viewer__nav viewer__nav--next" onClick={() => onIndex(index + 1)}>
              <Icon d={icons.chevronRight} />
            </button>
          )}
        </div>
        {info && (
          <aside aria-label={t('info.title')} className="viewer__info">
            <strong>{t('info.title')}</strong>
            <dl>
              <dt>{t('info.fileName')}</dt>
              <dd>{n.name}</dd>
              <dt>{t('col.size')}</dt>
              <dd>{sizeLabel}</dd>
              <dt>{t('info.modified')}</dt>
              <dd>{formatDateTime(n.modifiedAt, locale)}</dd>
              <dt>{t('info.location')}</dt>
              <dd>{location}</dd>
            </dl>
          </aside>
        )}
      </div>
    </div>
  )
}

function Preview({ node }: { node: Node }) {
  const t = useT()
  if (!canPreview(node)) {
    const type = fileTypes[node.type]
    return (
      <div className="viewer__none">
        <Icon d={type.d} size={48} stroke={type.lc} width={1.5} />
        <strong>{t('viewer.noPreview')}</strong>
        <span>{t('viewer.noPreviewBody')}</span>
        <a className="btn btn--primary" href={contentUrl(node.id, true)} download>
          <Icon d={icons.download} size={18} />
          {t('viewer.download')}
        </a>
      </div>
    )
  }
  switch (node.type) {
    case 'image':
      return <img src={contentUrl(node.id)} alt={displayName(node)} />
    case 'video':
      return <video src={contentUrl(node.id)} controls autoPlay playsInline />
    case 'audio':
      return <audio src={contentUrl(node.id)} controls autoPlay />
    case 'pdf':
      return (
        <Suspense fallback={<p className="pdf__loading">{t('loading')}</p>}>
          <PdfView url={contentUrl(node.id)} />
        </Suspense>
      )
    default:
      return <TextPreview node={node} />
  }
}

function TextPreview({ node }: { node: Node }) {
  const t = useT()
  const [text, setText] = useState<string | null>(null)
  useEffect(() => {
    let live = true
    fetch(contentUrl(node.id), { credentials: 'same-origin' })
      .then((r) => (r.ok ? r.text() : Promise.reject()))
      .then((s) => live && setText(s))
      .catch(() => live && setText(t('error.internal')))
    return () => {
      live = false
    }
  }, [node.id, t])
  return <pre className="viewer__text">{text ?? t('loading')}</pre>
}
