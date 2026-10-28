import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { api, ApiError, type Drive, type Node } from '../api'
import { displayName } from '../format'
import { useT, type MessageKey, type Translate } from '../i18n'
import { fileTypes, Icon, icons } from './Icon'

// errorText turns an API error into a sentence for the user.
export function errorText(t: Translate, err: unknown): string {
  const code = err instanceof ApiError ? err.code : 'internal'
  const key = `error.${code}` as MessageKey
  return t(key) === key ? t('error.internal') : t(key)
}

const focusable = 'button:not(:disabled), input:not(:disabled), [href], [tabindex]:not([tabindex="-1"])'

// Modal is a dialog that traps focus, closes on Esc and gives focus back to
// where it was. On phones it is a bottom sheet.
export function Modal({
  title,
  subtitle,
  onClose,
  children,
}: {
  title: string
  subtitle?: string
  onClose: () => void
  children: ReactNode
}) {
  const t = useT()
  const ref = useRef<HTMLDivElement>(null)
  const titleId = useId()
  const close = useRef(onClose)
  close.current = onClose

  useEffect(() => {
    const before = document.activeElement as HTMLElement | null
    const el = ref.current!
    const first = el.querySelector<HTMLElement>('input') ?? el.querySelectorAll<HTMLElement>(focusable)[1]
    first?.focus()
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.stopPropagation()
        close.current()
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
      <button type="button" aria-label={t('common.close')} className="modal-scrim" tabIndex={-1} onClick={onClose} />
      <div ref={ref} role="dialog" aria-modal="true" aria-labelledby={titleId} className="modal">
        <div className="modal__head">
          <div>
            <h2 id={titleId}>{title}</h2>
            {subtitle && <span>{subtitle}</span>}
          </div>
          <button type="button" aria-label={t('common.close')} className="icon-btn" onClick={onClose}>
            <Icon d={icons.close} />
          </button>
        </div>
        {children}
      </div>
    </div>
  )
}

// AddDialog offers what can be added to a folder: in v1 a folder or files.
export function AddDialog({
  folderName,
  onClose,
  onCreateFolder,
  onUpload,
}: {
  folderName: string
  onClose: () => void
  onCreateFolder: (name: string) => Promise<void>
  onUpload: () => void
}) {
  const t = useT()
  const [step, setStep] = useState<'pick' | 'folder'>('pick')
  if (step === 'folder') {
    return (
      <NameDialog
        title={t('folder.newTitle')}
        subtitle={t('add.in', { folder: folderName })}
        label={t('folder.name')}
        initial=""
        placeholder={t('folder.placeholder')}
        submit={t('folder.create')}
        onBack={() => setStep('pick')}
        onClose={onClose}
        onSubmit={(name) => onCreateFolder(name || t('folder.placeholder'))}
      />
    )
  }
  const tiles = [
    { label: t('add.folder'), desc: t('add.folderDesc'), icon: icons.folderAdd, color: '#a16207', tint: '#fef9c3', go: () => setStep('folder') },
    { label: t('add.upload'), desc: t('add.uploadDesc'), icon: icons.upload, color: '#374151', tint: '#f3f4f6', go: onUpload },
  ]
  return (
    <Modal title={t('add.title')} subtitle={t('add.in', { folder: folderName })} onClose={onClose}>
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
    </Modal>
  )
}

// NameDialog asks for one name: new folder or rename.
export function NameDialog({
  title,
  subtitle,
  label,
  initial,
  placeholder,
  submit,
  selectStem,
  onBack,
  onClose,
  onSubmit,
}: {
  title: string
  subtitle?: string
  label: string
  initial: string
  placeholder?: string
  submit: string
  selectStem?: boolean
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
    // Select the name without its extension, like file managers do.
    const el = input.current
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
    <Modal title={title} subtitle={subtitle} onClose={onClose}>
      <form className="field" onSubmit={save}>
        <label htmlFor={inputId}>{label}</label>
        <input id={inputId} ref={input} value={name} placeholder={placeholder} maxLength={255}
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
    </Modal>
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

// FolderPicker browses the folders on the user's drives. excludeId (the
// folder being moved) can't be entered; currentId (where it already is)
// can't be picked.
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

  const driveLabel = (d: Drive) => (d.kind === 'personal' ? t('nav.myFiles') : t('nav.family'))
  const here = trail.length ? trail[trail.length - 1].name : drive ? driveLabel(drive) : ''
  const parentOfHere = trail.length > 1 ? trail[trail.length - 2].id : drive?.rootId
  const path = drive ? [driveLabel(drive), ...trail.map((c) => c.name)].join(' / ') : ''

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

  return (
    <Modal title={title} onClose={onClose}>
      <div className="picker">
        <div className="picker__drives" role="group">
          {drives.map((d) => (
            <button key={d.id} type="button" aria-pressed={drive?.id === d.id} onClick={() => setFolder(d.rootId)}>
              {driveLabel(d)}
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
          <button type="button" className="btn btn--primary" disabled={busy || !drive || folder === currentId || folder === excludeId}
            onClick={pick}>
            {submit}
          </button>
        </div>
      </div>
    </Modal>
  )
}
