import { useEffect, useRef, useState, type ComponentType, type ReactNode } from 'react'
import { api, ApiError, type Node } from '../api'
import { errorText, Modal } from '../components/Dialogs'
import { fileTypes, Icon, icons } from '../components/Icon'
import { showToast } from '../components/Toast'
import { displayName, formatDateTime } from '../format'
import { useLocale, useT } from '../i18n'
import { editOf, toCsv, type Snapshot } from '../sheet/grid'
import MarkdownEditor from './MarkdownEditor'
import { saveCopy } from './saveCopy'
import SheetEditor from './SheetEditor'
import TextEditor from './TextEditor'
import { useEditSession, type EditorBodyProps, type Saver, type SaveStatus } from './useEditSession'

const maxText = 1 << 20

// Editor edits a file full screen (node.editor says how). lockId is the
// file's edit lock when it is taken already. onClose gets the file as saved
// last, once everything is saved or the user gave up on it.
export default function Editor(props: { node: Node; lockId?: string; onClose: (node: Node) => void }) {
  const { node } = props
  if (node.editor === 'sheet') {
    // A browser can't write .xlsx: the copy is a CSV of the values shown.
    return (
      <Frame {...props} Body={SheetEditor} save={(l, v, p: Snapshot) => api.saveSheet(node.id, l, v, editOf(p))}
        blob={(p) => new Blob([toCsv(p)], { type: 'text/csv' })} copyExt=".csv" />
    )
  }
  const save: Saver<string> = (lockId, version, text) =>
    new TextEncoder().encode(text).length > maxText
      ? Promise.reject(new ApiError(413, 'too_large'))
      : api.saveText(node.id, lockId, version, text)
  return (
    <Frame {...props} Body={node.editor === 'markdown' ? MarkdownEditor : TextEditor} save={save}
      blob={(s: string) => new Blob([s], { type: 'text/plain' })} />
  )
}

const tabbable = 'button:not(:disabled), input, textarea, [contenteditable="true"], [tabindex="0"]'

function Frame<P>({ node, lockId, onClose, Body, save, blob, copyExt }: {
  node: Node
  lockId?: string
  onClose: (node: Node) => void
  Body: ComponentType<EditorBodyProps<P>>
  save: Saver<P>
  // blob is the content to save as a copy.
  blob: (payload: P) => Blob
  copyExt?: string // the copy's extension, when not the file's
}) {
  const t = useT()
  const locale = useLocale()
  const session = useEditSession(node, lockId, save)
  const [header, setHeader] = useState<HTMLDivElement | null>(null)
  const [stem, setStem] = useState(displayName(node))
  const [asking, setAsking] = useState(false)
  const [dismissed, setDismissed] = useState<SaveStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const nameInput = useRef<HTMLInputElement>(null)
  const renaming = useRef<Promise<unknown>>(Promise.resolve())
  const ext = node.name.slice(displayName(node).length)
  const current = session?.node ?? node
  const s: SaveStatus = session?.status ?? { kind: 'loading' }
  const stuck = s.kind === 'readonly' || s.kind === 'conflict'
  const unsaved = stuck ? session?.unsaved() : undefined

  useEffect(() => {
    const before = document.activeElement as HTMLElement | null
    root.current?.focus()
    return () => before?.focus?.()
  }, [])

  // Turning read-only drops the focus from the page; keep it in here.
  useEffect(() => {
    if (document.activeElement === document.body) root.current?.focus()
  }, [s.kind])

  // The name follows renames made elsewhere, unless it is being edited.
  useEffect(() => {
    if (document.activeElement !== nameInput.current) setStem(displayName(current))
  }, [current])

  function rename() {
    const name = stem.trim()
    if (!session || !name || name + ext === current.name) return setStem(displayName(current))
    renaming.current = api.rename(current.id, name + ext).then(
      (n) => {
        session.node = { ...session.node, name: n.name }
        setStem(displayName(n))
      },
      (err) => {
        setStem(displayName(session.node))
        showToast(errorText(t, err), { error: true })
      },
    )
  }

  // close saves first; when that fails it asks before dropping the changes.
  async function close() {
    if (busy) return
    if (!session) return onClose(node)
    setBusy(true)
    await renaming.current
    const timeout = new Promise<boolean>((r) => setTimeout(() => r(false), 15_000))
    if (await Promise.race([session.flush(), timeout])) return onClose(session.node)
    setBusy(false)
    setAsking(true)
  }

  async function copy() {
    if (!session || unsaved === undefined) return
    setBusy(true)
    try {
      const c = await saveCopy(current, t('editor.copyName', { name: displayName(current) }) + (copyExt ?? ext), blob(unsaved))
      showToast(t('editor.copySaved', { name: c.name }))
      onClose(session.node)
    } catch (err) {
      showToast(errorText(t, err), { error: true })
      setBusy(false)
    }
  }

  function onKey(e: React.KeyboardEvent) {
    if ((e.target as HTMLElement).closest('.modal')) return
    if (e.key === 'Escape') {
      // Not on to the page below, which would close as well.
      e.preventDefault()
      e.stopPropagation()
      void close()
    } else if (e.key === 'Tab') {
      const all = [...root.current!.querySelectorAll<HTMLElement>(tabbable)]
      const i = all.indexOf(document.activeElement as HTMLElement)
      const to = e.shiftKey ? (i <= 0 ? all.at(-1) : null) : i === all.length - 1 ? all[0] : null
      if (to) {
        e.preventDefault()
        to.focus()
      }
    }
  }

  const conflict = s.kind === 'conflict' && s.at
    ? t('editor.conflict', { name: s.by ?? t('editor.someone'), date: formatDateTime(s.at, locale) })
    : t('error.edit_conflict')
  const copyButton = unsaved !== undefined && (
    <button type="button" className="btn btn--small" disabled={busy} onClick={copy}>
      <Icon d={icons.copy} size={16} />
      {t('editor.saveCopy')}
    </button>
  )
  let note: ReactNode = null
  if (s.kind === 'readonly') {
    note = (
      <>
        <p>
          {errorText(t, new ApiError(0, s.code, { holder: s.holder ?? t('editor.someone') }))}{' '}
          {unsaved !== undefined && t('editor.unsavedNote')}
        </p>
        {copyButton}
      </>
    )
  } else if (s.kind === 'conflict') {
    note = (
      <>
        <p>{conflict}</p>
        <button type="button" className="btn btn--small" disabled={busy} onClick={() => session?.reload()}>
          {t('editor.reload')}
        </button>
        {copyButton}
      </>
    )
  } else if (s.kind === 'error') {
    note = (
      <>
        <p>{s.code === 'too_large' && node.editor !== 'sheet' ? t('editor.tooLarge') : errorText(t, new ApiError(0, s.code))}</p>
        <button type="button" className="btn btn--small" onClick={() => session?.retry()}>{t('editor.retry')}</button>
      </>
    )
  }
  const status = {
    loading: t('editor.opening'),
    saving: t('editor.saving'),
    saved: t('editor.saved'),
    error: s.kind === 'error' && s.retrying ? t('editor.retrying') : t('editor.notSaved'),
    readonly: t('editor.readOnly'),
    conflict: t('editor.notSaved'),
  }[s.kind]
  const type = fileTypes[node.type]

  return (
    <div ref={root} role="dialog" aria-modal="true" aria-label={displayName(current)} tabIndex={-1} className="editor"
      onKeyDown={onKey}>
      <div className="editor__head">
        <button type="button" className="editor__close" aria-label={t('editor.close')} title={t('editor.close')}
          disabled={busy} onClick={close}>
          <Icon d={icons.back} />
        </button>
        <Icon d={type.d} stroke={type.c} />
        <div className="editor__titles">
          <input ref={nameInput} className="editor__name" aria-label={t('editor.fileName')} value={stem} maxLength={255}
            readOnly={!session || session.readOnly} autoComplete="off" spellCheck={false}
            onChange={(e) => setStem(e.target.value)} onBlur={rename}
            onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()} />
          <span role="status" className={'editor__status editor__status--' + s.kind}>{status}</span>
        </div>
        <div ref={setHeader} className="editor__extra" />
      </div>
      {note && <div className="editor__bar" role="alert">{note}</div>}
      <div className="editor__main">{session && <Body session={session} header={header} />}</div>
      {s.kind === 'conflict' && dismissed !== s && (
        <Modal title={t('editor.conflictTitle')} onClose={() => setDismissed(s)}>
          <p className="modal__body">{conflict} {t('editor.conflictBody')}</p>
          <div className="modal__foot">
            {unsaved !== undefined && (
              <button type="button" className="btn" disabled={busy} onClick={copy}>{t('editor.saveCopy')}</button>
            )}
            <button type="button" className="btn btn--primary" onClick={() => session?.reload()}>{t('editor.reload')}</button>
          </div>
        </Modal>
      )}
      {asking && (
        <Modal title={t('editor.closeTitle')} onClose={() => setAsking(false)}>
          <p className="modal__body">{t('editor.closeBody')}</p>
          <div className="modal__foot">
            <button type="button" className="btn" onClick={() => setAsking(false)}>{t('editor.keepEditing')}</button>
            <button type="button" className="btn btn--danger-solid" onClick={() => onClose(session?.node ?? node)}>
              {t('editor.closeAnyway')}
            </button>
          </div>
        </Modal>
      )}
    </div>
  )
}
