import { lazy, Suspense, useEffect, useState } from 'react'
import { api, type EditLock, type Node } from '../api'
import { displayName, formatDateTime, formatDuration, formatSize } from '../format'
import { useLocale, useT, type MessageKey } from '../i18n'
import { useContentUrl, useThumbUrl } from './content'
import { errorText } from './Dialogs'
import { fileTypes, Icon, icons } from './Icon'
import { LinkCard } from './LinkCard'
import { Logo } from './Logo'
import { showToast } from './Toast'

// pdf.js is large; load it the first time a PDF is opened. So is the
// editor, for the first file edited.
const PdfView = lazy(() => import('./PdfView'))
const Editor = lazy(() => import('../editor/Editor'))

const showableImage = /\.(jpe?g|png|gif|webp|avif|bmp)$/i
// Photos browsers can't show open through the server's preview (WebP).
const convertedImage = /\.(heic|heif|tiff?)$/i
const playableVideo = /\.(mp4|m4v|webm|mov)$/i
const maxTextBytes = 2_000_000

export function canPreview(n: Node): boolean {
  return (
    (n.type === 'image' && showableImage.test(n.name)) ||
    (n.type === 'image' && convertedImage.test(n.name) && n.thumb === 'image') ||
    (n.type === 'video' && playableVideo.test(n.name)) ||
    n.type === 'audio' ||
    (n.type === 'pdf' && /\.pdf$/i.test(n.name)) ||
    ((n.type === 'text' || n.type === 'code') && n.size <= maxTextBytes) ||
    (n.type === 'link' && !!n.link)
  )
}

// Documents start at the top of the stage instead of its middle.
const isDoc = (n: Node) => canPreview(n) && (n.type === 'text' || n.type === 'code' || n.type === 'pdf')

// Viewer shows one file full screen on dark, with previous/next through the
// folder's files. Without onClose (a link to one file) the logo takes the
// place of the close button; showInfo hides the info drawer for visitors.
// With onSaved, files the user may edit in the app get an Edit button;
// onSaved gets such a file as saved when its editor closes.
export function Viewer({
  files,
  index,
  location,
  onIndex,
  onClose,
  onSaved,
  showInfo = true,
}: {
  files: Node[]
  index: number
  location: string
  onIndex: (i: number) => void
  onClose?: () => void
  onSaved?: (n: Node) => void
  showInfo?: boolean
}) {
  const t = useT()
  const locale = useLocale()
  const contentUrl = useContentUrl()
  const [info, setInfo] = useState(false)
  const [editing, setEditing] = useState<{ node: Node; lockId: string } | null>(null)
  const n = files[index]
  const type = fileTypes[n.type]

  useEffect(() => {
    if (editing) return
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose?.()
      else if (e.key === 'ArrowLeft' && index > 0) onIndex(index - 1)
      else if (e.key === 'ArrowRight' && index < files.length - 1) onIndex(index + 1)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [index, files.length, onClose, onIndex, editing])

  const sizeLabel = formatSize(n.size, locale)
  return (
    <div role="dialog" aria-modal="true" aria-label={displayName(n)} className="viewer">
      <div className="viewer__bar">
        {onClose ? (
          <button type="button" aria-label={t('viewer.close')} className="dark-btn dark-btn--plain" onClick={onClose} autoFocus>
            <Icon d={icons.back} />
          </button>
        ) : (
          <span className="viewer__brand">
            <Logo size={28} label={t('app.name')} />
          </span>
        )}
        <Icon d={type.d} stroke={type.lc} />
        <div className="viewer__title">
          <strong>{displayName(n)}</strong>
          <span>{t(`short.${n.type}` as MessageKey)} · {sizeLabel}</span>
        </div>
        {onSaved && n.editor && (
          <EditButton key={n.id} node={n} editing={!!editing} onEdit={(lockId) => setEditing({ node: n, lockId })} />
        )}
        <a className="dark-btn" aria-label={t('viewer.download')} href={contentUrl(n.id, true)} download
          autoFocus={!onClose}>
          <Icon d={icons.download} size={18} />
        </a>
        {showInfo && (
          <button type="button" aria-label={t('viewer.info')} aria-pressed={info} className="dark-btn" onClick={() => setInfo(!info)}>
            <Icon d={icons.info} />
          </button>
        )}
      </div>
      <div className="viewer__main">
        <div className="viewer__stage">
          <div key={n.id + n.version}
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
        {showInfo && info && (
          <aside aria-label={t('info.title')} className="viewer__info">
            <strong>{t('info.title')}</strong>
            <dl>
              <dt>{t('info.fileName')}</dt>
              <dd>{n.name}</dd>
              <dt>{t('col.size')}</dt>
              <dd>{sizeLabel}</dd>
              <dt>{t('info.modified')}</dt>
              <dd>{formatDateTime(n.modifiedAt, locale)}</dd>
              {n.durationMs ? (
                <>
                  <dt>{t('info.length')}</dt>
                  <dd>{formatDuration(n.durationMs)}</dd>
                </>
              ) : null}
              <dt>{t('info.location')}</dt>
              <dd>{location}</dd>
              {n.description && (
                <>
                  <dt>{t('info.description')}</dt>
                  <dd className="description">{n.description}</dd>
                </>
              )}
            </dl>
          </aside>
        )}
      </div>
      {editing && onSaved && (
        <Suspense fallback={null}>
          <Editor node={editing.node} lockId={editing.lockId}
            onClose={(node) => {
              setEditing(null)
              onSaved(node)
            }} />
        </Suspense>
      )}
    </div>
  )
}

// EditButton takes the file's edit lock and opens the editor. Until then it
// shows who else is editing the file, checked every 30 s while the page
// shows; looking at a file never locks it.
function EditButton({ node, editing, onEdit }: { node: Node; editing: boolean; onEdit: (lockId: string) => void }) {
  const t = useT()
  const [lock, setLock] = useState<EditLock | null>(null)
  const [busy, setBusy] = useState(false)
  const [tick, setTick] = useState(0)
  // After editing, give the editor's lock a moment to be let go.
  const [delay, setDelay] = useState(0)

  useEffect(() => {
    if (editing) return
    let live = true
    const check = () => {
      if (document.visibilityState === 'visible') api.lockStatus(node.id).then((l) => live && setLock(l), () => {})
    }
    const first = setTimeout(check, delay)
    const timer = setInterval(check, 30_000)
    document.addEventListener('visibilitychange', check)
    return () => {
      live = false
      clearTimeout(first)
      clearInterval(timer)
      document.removeEventListener('visibilitychange', check)
    }
  }, [node.id, editing, tick, delay])

  async function edit() {
    setBusy(true)
    try {
      const { lockId } = await api.takeLock(node.id, !!lock?.mine)
      setLock(null)
      setDelay(1500)
      onEdit(lockId)
    } catch (err) {
      // Someone took it meanwhile: say so, and show who.
      showToast(errorText(t, err), { error: true })
      setTick((n) => n + 1)
    }
    setBusy(false)
  }

  const other = lock && !lock.mine ? t('editor.editingBy', { name: lock.holder.name }) : ''
  return (
    <button type="button" className="dark-btn dark-btn--label" disabled={busy || !!other}
      title={other || (lock ? t('editor.editHereHint') : undefined)} onClick={edit}>
      <Icon d={icons.pencil} size={18} />
      <span>{other || t(lock ? 'editor.editHere' : 'editor.edit')}</span>
    </button>
  )
}

function Preview({ node }: { node: Node }) {
  const t = useT()
  const contentUrl = useContentUrl()
  const thumbUrl = useThumbUrl()
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
      if (convertedImage.test(node.name)) return <img src={thumbUrl(node.id, node.version, 'preview')} alt={displayName(node)} />
      return <img src={contentUrl(node.id)} alt={displayName(node)} />
    case 'video':
      return <video src={contentUrl(node.id)} controls autoPlay playsInline />
    case 'audio':
      return <audio src={contentUrl(node.id)} controls autoPlay />
    case 'link':
      return <LinkCard node={node} />
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
  const contentUrl = useContentUrl()
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
  }, [node.id, t, contentUrl])
  return <pre className="viewer__text">{text ?? t('loading')}</pre>
}
