import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { api, ApiError, type Drive, type Node } from '../api'
import { displayName } from '../format'
import { useT, type MessageKey, type Translate } from '../i18n'
import { fileTypes, Icon, icons } from './Icon'
import { driveLabel } from './Items'
import { closeBackLayer, useBackLayer } from '../router'

// errorText turns an API error into a sentence for the user, with the
// error's text details (such as who holds a lock) filled in.
export function errorText(t: Translate, err: unknown): string {
  const code = err instanceof ApiError ? err.code : 'internal'
  const key = `error.${code}` as MessageKey
  const vars: Record<string, string> = {}
  if (err instanceof ApiError) for (const [k, v] of Object.entries(err.body)) if (typeof v === 'string') vars[k] = v
  return t(key) === key ? t('error.internal') : t(key, vars)
}

const focusable = 'button:not(:disabled), input:not(:disabled), [href], [tabindex]:not([tabindex="-1"])'

// Modal is a dialog that traps focus, closes on Esc and gives focus back to
// where it was. On phones it is a bottom sheet. lead goes before the
// title, such as the thumbnail of the item a dialog is about.
export function Modal({
  title,
  subtitle,
  lead,
  onClose,
  children,
}: {
  title: string
  subtitle?: string
  lead?: ReactNode
  onClose: () => void
  children: ReactNode
}) {
  const t = useT()
  const ref = useRef<HTMLDivElement>(null)
  const titleId = useId()
  const close = useRef(onClose)
  close.current = onClose
  useBackLayer(true, onClose)

  useEffect(() => {
    const before = document.activeElement as HTMLElement | null
    const el = ref.current!
    const first = el.querySelector<HTMLElement>('input') ?? el.querySelectorAll<HTMLElement>(focusable)[1]
    first?.focus()
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        // A field that cancels itself with Escape (a new folder's name).
        if ((e.target as HTMLElement | null)?.closest?.('[data-own-escape]')) return
        e.stopPropagation()
        closeBackLayer(close.current)
      } else if (e.key === 'Tab') {
        const all = [...el.querySelectorAll<HTMLElement>(focusable)]
        if (!all.length) return
        const i = all.indexOf(document.activeElement as HTMLElement)
        if (e.shiftKey && i <= 0) {
          all[all.length - 1].focus()
          e.preventDefault()
        } else if (!e.shiftKey && i === all.length - 1) {
          all[0].focus()
          e.preventDefault()
        }
      }
    }
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      before?.focus?.()
    }
  }, [])

  return (
    <div className="modal-wrap">
      <button type="button" aria-label={t('common.close')} className="modal-scrim" tabIndex={-1} onClick={() => closeBackLayer(onClose)} />
      <div ref={ref} role="dialog" aria-modal="true" aria-labelledby={titleId} className="modal">
        <div className="modal__head">
          {lead && <span className="modal__thumb">{lead}</span>}
          <div className="modal__title">
            <h2 id={titleId}>{title}</h2>
            {subtitle && <span>{subtitle}</span>}
          </div>
          <button type="button" aria-label={t('common.close')} className="icon-btn" onClick={() => closeBackLayer(onClose)}>
            <Icon d={icons.close} />
          </button>
        </div>
        {children}
      </div>
    </div>
  )
}

// ConfirmDialog asks before something that can't be undone.
export function ConfirmDialog({
  title,
  body,
  confirm,
  onClose,
  onConfirm,
}: {
  title: string
  body: string
  confirm: string
  onClose: () => void
  onConfirm: () => Promise<void>
}) {
  const t = useT()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function go() {
    setBusy(true)
    setError('')
    try {
      await onConfirm()
    } catch (err) {
      setError(errorText(t, err))
      setBusy(false)
    }
  }
  return (
    <Modal title={title} onClose={onClose}>
      <p className="modal__body">{body}</p>
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="modal__foot">
        <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
        <button type="button" className="btn btn--danger-solid" disabled={busy} onClick={go}>{confirm}</button>
      </div>
    </Modal>
  )
}

// AddDialog offers what can be added to a folder: a folder, files, a folder
// of files and, with onNewFile, a new document or spreadsheet to edit.
export function AddDialog({
  folderName,
  onClose,
  onCreateFolder,
  onUpload,
  onUploadFolder,
  onNewFile,
}: {
  folderName: string
  onClose: () => void
  onCreateFolder: (name: string) => Promise<void>
  onUpload: () => void
  onUploadFolder: () => void
  onNewFile?: (kind: 'markdown' | 'sheet') => void
}) {
  const t = useT()
  const [step, setStep] = useState<'pick' | 'folder'>('pick')
  const tiles = [
    { label: t('add.folder'), desc: t('add.folderDesc'), icon: icons.folderAdd, color: '#a16207', tint: '#fef9c3', go: () => setStep('folder') },
    { label: t('add.upload'), desc: t('add.uploadDesc'), icon: icons.upload, color: '#374151', tint: '#f3f4f6', go: onUpload },
    { label: t('add.uploadFolder'), desc: t('add.uploadFolderDesc'), icon: icons.cloudUpload, color: '#6d28d9', tint: '#ede9fe', go: onUploadFolder },
    ...(onNewFile
      ? [
          { label: t('editor.addDoc'), desc: t('editor.addDocDesc'), icon: fileTypes.text.d, color: '#0369a1', tint: '#e0f2fe', go: () => onNewFile('markdown') },
          { label: t('editor.addSheet'), desc: t('editor.addSheetDesc'), icon: fileTypes.sheet.d, color: '#15803d', tint: '#dcfce7', go: () => onNewFile('sheet') },
        ]
      : []),
  ]
  // Both steps stay in one Modal. Replacing it with another Modal would make
  // the first one's history entry go away (Back) after the second has made
  // its own, and that Back would close the second.
  return (
    <Modal title={step === 'folder' ? t('folder.newTitle') : t('add.title')} subtitle={t('add.in', { folder: folderName })} onClose={onClose}>
      {step === 'folder' ? (
        <NameForm
          label={t('folder.name')}
          initial=""
          placeholder={t('folder.placeholder')}
          submit={t('folder.create')}
          onBack={() => setStep('pick')}
          onClose={onClose}
          onSubmit={(name) => onCreateFolder(name || t('folder.placeholder'))}
        />
      ) : (
        <div className="tiles">
          {tiles.map((a) => (
            <button key={a.label} type="button" className="tile" onClick={a.go}>
              <span className="tile__icon" style={{ background: a.tint, color: a.color }}>
                <Icon d={a.icon} size={22} />
              </span>
              <strong>{a.label}</strong>
              <span>{a.desc}</span>
            </button>
          ))}
        </div>
      )}
    </Modal>
  )
}

// NameDialog asks for one name: new folder, rename, new shared drive.
export function NameDialog({
  title,
  subtitle,
  onClose,
  ...form
}: {
  title: string
  subtitle?: string
  label: string
  initial: string
  placeholder?: string
  submit: string
  selectStem?: boolean
  maxLength?: number
  onBack?: () => void
  onClose: () => void
  onSubmit: (name: string) => Promise<void>
}) {
  return (
    <Modal title={title} subtitle={subtitle} onClose={onClose}>
      <NameForm onClose={onClose} {...form} />
    </Modal>
  )
}

// NameForm is the form of a NameDialog, for a Modal that has other steps.
function NameForm({
  label,
  initial,
  placeholder,
  submit,
  selectStem,
  maxLength = 255,
  onBack,
  onClose,
  onSubmit,
}: {
  label: string
  initial: string
  placeholder?: string
  submit: string
  selectStem?: boolean
  maxLength?: number
  onBack?: () => void
  onClose: () => void
  onSubmit: (name: string) => Promise<void>
}) {
  const t = useT()
  const [name, setName] = useState(initial)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const inputId = useId()
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    // Select the name without its extension, like file managers do. The
    // Modal only focuses its first field when it opens, so a form shown in
    // a later step has to focus itself.
    const el = input.current
    el?.focus()
    if (el && selectStem) {
      const dot = initial.lastIndexOf('.')
      el.setSelectionRange(0, dot > 0 ? dot : initial.length)
    } else el?.select()
  }, [initial, selectStem])

  async function save(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await onSubmit(name.trim())
    } catch (err) {
      setError(errorText(t, err))
      setBusy(false)
    }
  }

  return (
    <form className="field" onSubmit={save}>
      <label htmlFor={inputId}>{label}</label>
      <input id={inputId} ref={input} value={name} placeholder={placeholder} maxLength={maxLength}
        onChange={(e) => setName(e.target.value)} autoComplete="off" />
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="modal__foot">
        <button type="button" className="btn" onClick={onBack ?? onClose}>
          {onBack ? t('common.back') : t('common.cancel')}
        </button>
        <button type="submit" className="btn btn--primary" disabled={busy || (!name.trim() && !placeholder)}>
          {submit}
        </button>
      </div>
    </form>
  )
}

// MoveDialog lets the user browse folders on their drives and pick a target.
export function MoveDialog({
  node,
  drives,
  startFolder,
  onClose,
  onMove,
}: {
  node: Node
  drives: Drive[]
  startFolder: string
  onClose: () => void
  onMove: (targetId: string, targetName: string) => Promise<void>
}) {
  const t = useT()
  return (
    <FolderPicker
      title={t('move.title', { name: displayName(node) })}
      submit={t('move.here')}
      drives={drives}
      startFolder={startFolder}
      excludeId={node.id}
      currentId={node.parentId}
      onClose={onClose}
      onPick={(id, name) => onMove(id, name)}
    />
  )
}

// FolderPicker browses the folders on the drives the user can add to.
// excludeId (the folder being moved) can't be entered; currentId (where it
// already is) can't be picked.
export function FolderPicker({
  title,
  submit,
  drives,
  startFolder,
  excludeId,
  currentId,
  onClose,
  onPick,
}: {
  title: string
  submit: string
  drives: Drive[]
  startFolder: string
  excludeId?: string
  currentId?: string
  onClose: () => void
  onPick: (folderId: string, name: string, path: string) => Promise<void>
}) {
  const t = useT()
  const [folder, setFolder] = useState(startFolder)
  const [trail, setTrail] = useState<{ id: string; name: string }[]>([])
  const [drive, setDrive] = useState<Drive | null>(null)
  const [folders, setFolders] = useState<Node[] | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [newFolder, setNewFolder] = useState(false)
  const [newName, setNewName] = useState('')
  const [newBusy, setNewBusy] = useState(false)

  useEffect(() => {
    let live = true
    setFolders(null)
    Promise.all([api.node(folder), api.children(folder, 'name', false)])
      .then(([d, kids]) => {
        if (!live) return
        setDrive(d.drive)
        setTrail(d.node.id === d.drive.rootId ? [] : [...d.path.slice(1), { id: d.node.id, name: d.node.name }])
        setFolders(kids.filter((k) => k.kind === 'folder'))
      })
      .catch((err) => live && setError(errorText(t, err)))
    return () => {
      live = false
    }
  }, [folder, t])

  const label = (d: Drive) => driveLabel(t, d)
  const here = trail.length ? trail[trail.length - 1].name : drive ? label(drive) : ''
  const parentOfHere = trail.length > 1 ? trail[trail.length - 2].id : drive?.rootId
  const path = drive ? [label(drive), ...trail.map((c) => c.name)].join(' / ') : ''
  const writable = drives.filter((d) => d.role !== 'viewer')
  const canPick = !!drive && drive.role !== 'viewer' && folder !== currentId && folder !== excludeId

  async function pick() {
    setBusy(true)
    setError('')
    try {
      await onPick(folder, here, path)
    } catch (err) {
      setError(errorText(t, err))
      setBusy(false)
    }
  }

  async function createHere() {
    const name = newName.trim()
    if (!name || !drive) return
    setNewBusy(true)
    setError('')
    try {
      const created = await api.createFolder(folder, name)
      setNewFolder(false)
      setNewName('')
      setFolder(created.id)
    } catch (err) {
      setError(errorText(t, err))
    } finally {
      setNewBusy(false)
    }
  }

  return (
    <Modal title={title} onClose={onClose}>
      <div className="picker">
        <div className="picker__drives" role="group">
          {writable.map((d) => (
            <button key={d.id} type="button" aria-pressed={drive?.id === d.id} onClick={() => setFolder(d.rootId)}>
              {label(d)}
            </button>
          ))}
        </div>
        <nav aria-label={t('crumbs.label')} className="crumbs">
          <button type="button" className="back-btn" disabled={!trail.length}
            onClick={() => parentOfHere && setFolder(parentOfHere)}>
            <Icon d={icons.back} size={18} />
          </button>
          <strong>{here}</strong>
        </nav>
        <div className="picker__list">
          {folders === null && !error && <div className="picker__empty">{t('loading')}</div>}
          {folders?.length === 0 && <div className="picker__empty">{t('move.noFolders')}</div>}
          {drive && drive.role !== 'viewer' && !newFolder && (
            <button type="button" className="picker__row picker__row--new"
              onClick={() => { setNewFolder(true); setNewName('') }}>
              <Icon d={icons.folderAdd} stroke="#a16207" />
              <span>{t('folder.newTitle')}</span>
            </button>
          )}
          {newFolder && (
            <div className="picker__new" data-own-escape="">
              <input
                autoFocus
                value={newName}
                maxLength={255}
                placeholder={t('folder.placeholder')}
                aria-label={t('folder.name')}
                onChange={(e) => setNewName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void createHere()
                  if (e.key === 'Escape') {
                    e.stopPropagation()
                    setNewFolder(false)
                  }
                }}
              />
              <button type="button" className="icon-btn" aria-label={t('common.cancel')} onClick={() => setNewFolder(false)}>
                <Icon d={icons.close} size={16} />
              </button>
              <button type="button" className="btn btn--primary btn--small" disabled={newBusy || !newName.trim()} onClick={() => void createHere()}>
                {t('folder.create')}
              </button>
            </div>
          )}
          {folders?.map((f) => (
            <button key={f.id} type="button" className="picker__row" disabled={f.id === excludeId}
              aria-label={t('move.open', { name: f.name })} onClick={() => setFolder(f.id)}>
              <Icon d={fileTypes.folder.d} stroke={fileTypes.folder.c} fill="#fef9c3" />
              <span>{f.name}</span>
              <Icon d={icons.chevronRight} size={16} stroke="#9ca3af" />
            </button>
          ))}
        </div>
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="modal__foot">
          <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button type="button" className="btn btn--primary" disabled={busy || !canPick}
            onClick={pick}>
            {submit}
          </button>
        </div>
      </div>
    </Modal>
  )
}
