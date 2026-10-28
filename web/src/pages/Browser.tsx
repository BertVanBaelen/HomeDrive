import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, type Details, type Drive, type Node, type SortKey, type TrashedNode, type User } from '../api'
import { AddDialog, errorText, MoveDialog, NameDialog } from '../components/Dialogs'
import { fileTypes, Icon, icons } from '../components/Icon'
import { InfoPanel } from '../components/InfoPanel'
import { Fab, Header, Link, Sidebar, TabBar, type Section } from '../components/Shell'
import { Thumb } from '../components/Thumb'
import { showToast } from '../components/Toast'
import { UploadPanel } from '../components/UploadPanel'
import { Viewer } from '../components/Viewer'
import { notifyChanged, onChanged } from '../events'
import { displayName, formatSize, formatWhen } from '../format'
import { plural, useLocale, useT, type MessageKey, type Translate } from '../i18n'
import { folderPath, navigate, type Route } from '../router'
import { addUploads, onBatchDone } from '../uploads'

type View = 'grid' | 'list'
interface Sort {
  key: SortKey
  desc: boolean
}

// Per-browser conveniences; storage may be unavailable (private mode).
function stored<T>(key: string, fallback: T): T {
  try {
    const v = localStorage.getItem(key)
    return v ? (JSON.parse(v) as T) : fallback
  } catch {
    return fallback
  }
}
function store(key: string, v: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(v))
  } catch {
    // ignore
  }
}

function driveLabel(t: Translate, d: Drive): string {
  return d.kind === 'personal' ? t('nav.myFiles') : t('nav.family')
}

// hrefFor is a folder's URL: drive tops have short ones.
function hrefFor(id: string, drives: Drive[]): string {
  const d = drives.find((x) => x.rootId === id)
  if (d) return d.kind === 'personal' ? '/' : '/family'
  return folderPath(id)
}

export function Browser({ user, route, onSignOut }: { user: User; route: Route; onSignOut: () => void }) {
  const t = useT()
  const [drives, setDrives] = useState<Drive[] | null>(null)
  const [trashCount, setTrashCount] = useState(0)
  const [addOpen, setAddOpen] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    api.drives().then(setDrives, (err) => setError(errorText(t, err)))
  }, [t])

  const refreshTrash = useCallback(() => {
    api.trashList().then((l) => setTrashCount(l.length), () => {})
  }, [])
  useEffect(() => {
    refreshTrash()
    return onChanged((id) => id === 'trash' && refreshTrash())
  }, [refreshTrash])

  useEffect(
    () => onBatchDone((n) => showToast(plural(t, 'toast.uploaded', n))),
    [t],
  )

  let section: Section = null
  let folderId: string | null = null
  if (drives) {
    if (route.name === 'family') {
      section = 'family'
      folderId = drives.find((d) => d.kind === 'shared')?.rootId ?? null
    } else if (route.name === 'trash') {
      section = 'trash'
    } else if (route.name === 'files') {
      folderId = route.folderId ?? drives.find((d) => d.kind === 'personal')?.rootId ?? null
      section = route.folderId ? null : 'files'
    }
  }
  const [folderDrive, setFolderDrive] = useState<Drive | null>(null)
  if (section === null && folderDrive && route.name === 'files') {
    section = folderDrive.kind === 'personal' ? 'files' : 'family'
  }
  const canAdd = !!folderId && (folderDrive?.role ?? 'viewer') !== 'viewer'

  return (
    <div className="app">
      <Header user={user} onSignOut={onSignOut} />
      <div className="app-body">
        <Sidebar section={section} trashCount={trashCount} canAdd={canAdd} onAdd={() => setAddOpen(true)} />
        <main className="main">
          {error && <p className="form-error" role="alert">{error}</p>}
          {drives && folderId && (
            <FolderView
              key={folderId}
              folderId={folderId}
              drives={drives}
              addOpen={addOpen}
              setAddOpen={setAddOpen}
              onDrive={setFolderDrive}
            />
          )}
          {drives && route.name === 'trash' && <TrashView />}
        </main>
      </div>
      <TabBar section={section} />
      {canAdd && <Fab onAdd={() => setAddOpen(true)} />}
    </div>
  )
}

function useViewPrefs() {
  const [view, setViewState] = useState<View>(() => stored('hd.view', 'grid'))
  const [sort, setSortState] = useState<Sort>(() => stored('hd.sort', { key: 'name', desc: false }))
  const setView = (v: View) => {
    setViewState(v)
    store('hd.view', v)
  }
  const setSort = (s: Sort) => {
    setSortState(s)
    store('hd.sort', s)
  }
  return { view, setView, sort, setSort }
}

type Dialog = { kind: 'rename'; node: Node } | { kind: 'move'; node: Node } | null

function FolderView({
  folderId,
  drives,
  addOpen,
  setAddOpen,
  onDrive,
}: {
  folderId: string
  drives: Drive[]
  addOpen: boolean
  setAddOpen: (b: boolean) => void
  onDrive: (d: Drive) => void
}) {
  const t = useT()
  const { view, setView, sort, setSort } = useViewPrefs()
  const [details, setDetails] = useState<Details | null>(null)
  const [items, setItems] = useState<Node[] | null>(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const [dialog, setDialog] = useState<Dialog>(null)
  const [viewing, setViewing] = useState<number | null>(null)
  const [dragging, setDragging] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)

  const load = useCallback(() => {
    Promise.all([api.node(folderId), api.children(folderId, sort.key, sort.desc)]).then(
      ([d, kids]) => {
        setDetails(d)
        setItems(kids)
        onDrive(d.drive)
        setError('')
      },
      (err) => setError(errorText(t, err)),
    )
  }, [folderId, sort.key, sort.desc, t, onDrive])

  useEffect(load, [load])
  useEffect(() => onChanged((id) => id === folderId && load()), [folderId, load])

  const drive = details?.drive
  const readOnly = !drive || drive.role === 'viewer'
  const isRoot = !!details && details.node.id === details.drive.rootId
  const title = !details ? '' : isRoot ? driveLabel(t, details.drive) : details.node.name
  const files = useMemo(() => (items ?? []).filter((n) => n.kind === 'file'), [items])
  const sel = items?.find((n) => n.id === selected) ?? null

  // Breadcrumb: drive top, the folders above, this folder.
  const crumbs = details
    ? [
        { id: details.drive.rootId, name: driveLabel(t, details.drive), home: true },
        ...details.path.slice(1).map((c) => ({ ...c, home: false })),
        ...(isRoot ? [] : [{ id: details.node.id, name: details.node.name, home: false }]),
      ]
    : []
  const parent = crumbs.length > 1 ? crumbs[crumbs.length - 2] : null
  const location = crumbs.map((c) => c.name).join(' / ')

  // Drop files anywhere to upload into this folder.
  useEffect(() => {
    if (readOnly) return
    let depth = 0
    const hasFiles = (e: DragEvent) => e.dataTransfer?.types.includes('Files')
    const enter = (e: DragEvent) => {
      if (!hasFiles(e)) return
      depth++
      setDragging(true)
    }
    const leave = (e: DragEvent) => {
      if (!hasFiles(e)) return
      if (--depth <= 0) setDragging(false)
    }
    const over = (e: DragEvent) => {
      if (hasFiles(e)) e.preventDefault()
    }
    const drop = (e: DragEvent) => {
      if (!hasFiles(e)) return
      e.preventDefault()
      depth = 0
      setDragging(false)
      // Folders can't be uploaded yet; keep plain files only.
      const dropped = [...(e.dataTransfer?.items ?? [])]
        .filter((it) => it.kind === 'file' && !it.webkitGetAsEntry?.()?.isDirectory)
        .map((it) => it.getAsFile())
        .filter((f): f is File => !!f)
      if (dropped.length) addUploads(dropped, folderId, title)
    }
    window.addEventListener('dragenter', enter)
    window.addEventListener('dragleave', leave)
    window.addEventListener('dragover', over)
    window.addEventListener('drop', drop)
    return () => {
      window.removeEventListener('dragenter', enter)
      window.removeEventListener('dragleave', leave)
      window.removeEventListener('dragover', over)
      window.removeEventListener('drop', drop)
    }
  }, [readOnly, folderId, title])

  function open(n: Node) {
    if (n.kind === 'folder') navigate(hrefFor(n.id, drives))
    else setViewing(files.findIndex((f) => f.id === n.id))
  }

  async function trash(n: Node) {
    try {
      await api.trash(n.id)
      setSelected(null)
      load()
      notifyChanged('trash')
      showToast(t('toast.trashed'), {
        undo: () =>
          api.restore(n.id).then(
            () => {
              load()
              notifyChanged('trash')
            },
            (err) => showToast(errorText(t, err), { error: true }),
          ),
      })
    } catch (err) {
      showToast(errorText(t, err), { error: true })
    }
  }

  if (error && !details) return <p className="form-error" role="alert">{error}</p>
  if (!details || !items) return null

  return (
    <>
      <div className="page-head">
        <div className="page-head__text">
          <nav aria-label={t('crumbs.label')} className="crumbs">
            <button type="button" className="back-btn" disabled={!parent}
              aria-label={parent ? t('crumbs.backTo', { name: parent.name }) : t('crumbs.back')}
              onClick={() => parent && navigate(hrefFor(parent.id, drives))}>
              <Icon d={icons.back} size={18} />
              <span className="back-btn__label">{parent ? t('crumbs.backTo', { name: parent.name }) : t('crumbs.back')}</span>
            </button>
            <ol>
              {crumbs.map((c, i) => {
                const here = i === crumbs.length - 1
                return (
                  <li key={c.id}>
                    {i > 0 && <Icon d={icons.chevronRight} size={14} stroke="#9ca3af" />}
                    {here ? (
                      <span className="crumb" aria-current="page">
                        {c.home && <Icon d={icons.home} size={16} />}
                        {c.name}
                      </span>
                    ) : (
                      <Link className="crumb" href={hrefFor(c.id, drives)}>
                        {c.home && <Icon d={icons.home} size={16} />}
                        {c.name}
                      </Link>
                    )}
                  </li>
                )
              })}
            </ol>
          </nav>
          <div className="page-title">
            <h1 key={folderId}>{title}</h1>
            <span>{plural(t, 'count.items', items.length)}</span>
          </div>
        </div>
        <ViewToggle view={view} setView={setView} />
      </div>

      <UploadPanel />

      <div className="files-row">
        <section aria-label={title} className="files">
          {items.length === 0 ? (
            <div className="empty">
              <Icon d={icons.folder} size={40} stroke="#6b7280" />
              <strong>{t('empty.folder.title')}</strong>
              <span>{t('empty.folder.body')}</span>
              {!readOnly && (
                <button type="button" className="btn btn--primary" onClick={() => setAddOpen(true)}>
                  <Icon d={icons.plus} size={18} width={2.25} />
                  {t('empty.add')}
                </button>
              )}
            </div>
          ) : view === 'grid' ? (
            <Grid items={items} selected={selected} onOpen={open} onInfo={(n) => setSelected(selected === n.id ? null : n.id)} />
          ) : (
            <div className="list-wrap">
              {!isRoot && <Rail crumbs={crumbs} drives={drives} />}
              <List items={items} selected={selected} sort={sort} setSort={setSort} onOpen={open}
                onInfo={(n) => setSelected(selected === n.id ? null : n.id)} />
            </div>
          )}
        </section>
        {sel && (
          <InfoPanel
            node={sel}
            location={location}
            readOnly={readOnly}
            onClose={() => setSelected(null)}
            onOpen={() => open(sel)}
            onRename={() => setDialog({ kind: 'rename', node: sel })}
            onMove={() => setDialog({ kind: 'move', node: sel })}
            onTrash={() => trash(sel)}
            onRestore={() => {}}
          />
        )}
      </div>

      {!readOnly && (
        <p className="drop-hint">
          <Icon d={icons.cloudUpload} size={16} />
          {t('drop.hint')}
        </p>
      )}
      {dragging && (
        <div className="drop-overlay" aria-hidden="true">
          <div>
            <Icon d={icons.cloudUpload} size={28} />
            {t('drop.overlay', { folder: title })}
          </div>
        </div>
      )}

      <input ref={fileInput} type="file" multiple hidden
        onChange={(e) => {
          if (e.target.files?.length) addUploads([...e.target.files], folderId, title)
          e.target.value = ''
        }} />

      {addOpen && (
        <AddDialog
          folderName={title}
          onClose={() => setAddOpen(false)}
          onUpload={() => {
            setAddOpen(false)
            fileInput.current?.click()
          }}
          onCreateFolder={async (name) => {
            await api.createFolder(folderId, name)
            setAddOpen(false)
            load()
            showToast(t('toast.folderCreated'))
          }}
        />
      )}
      {dialog?.kind === 'rename' && (
        <NameDialog
          title={t('rename.title')}
          label={t('rename.label')}
          initial={dialog.node.name}
          submit={t('rename.submit')}
          selectStem={dialog.node.kind === 'file'}
          onClose={() => setDialog(null)}
          onSubmit={async (name) => {
            await api.rename(dialog.node.id, name)
            setDialog(null)
            load()
            showToast(t('toast.renamed'))
          }}
        />
      )}
      {dialog?.kind === 'move' && (
        <MoveDialog
          node={dialog.node}
          drives={drives}
          startFolder={folderId}
          onClose={() => setDialog(null)}
          onMove={async (target, targetName) => {
            await api.move(dialog.node.id, target)
            setDialog(null)
            setSelected(null)
            load()
            notifyChanged(target)
            showToast(t('toast.moved', { folder: targetName }))
          }}
        />
      )}
      {viewing !== null && files[viewing] && (
        <Viewer files={files} index={viewing} location={location} onIndex={setViewing} onClose={() => setViewing(null)} />
      )}
    </>
  )
}

function ViewToggle({ view, setView }: { view: View; setView: (v: View) => void }) {
  const t = useT()
  return (
    <div role="group" aria-label={t('view.label')} className="toggle">
      <button type="button" aria-label={t('view.gridAria')} aria-pressed={view === 'grid'} onClick={() => setView('grid')}>
        <Icon d={icons.grid} size={16} />
        <span>{t('view.grid')}</span>
      </button>
      <button type="button" aria-label={t('view.listAria')} aria-pressed={view === 'list'} onClick={() => setView('list')}>
        <Icon d={icons.list} size={16} />
        <span>{t('view.list')}</span>
      </button>
    </div>
  )
}

// Staggered entrance, capped at 12 items like the design.
const delay = (i: number) => ({ animationDelay: `${Math.min(i, 12) * 30}ms` })

function sizeLabel(n: Node, t: Translate, locale: ReturnType<typeof useLocale>) {
  return n.kind === 'folder' ? plural(t, 'count.items', n.childCount ?? 0) : formatSize(n.size, locale)
}

function Grid({
  items,
  selected,
  onOpen,
  onInfo,
}: {
  items: Node[]
  selected: string | null
  onOpen: (n: Node) => void
  onInfo: (n: Node) => void
}) {
  const t = useT()
  const locale = useLocale()
  return (
    <div className="grid">
      {items.map((n, i) => {
        const type = fileTypes[n.type]
        const name = displayName(n)
        const size = sizeLabel(n, t, locale)
        const when = formatWhen(n.modifiedAt, t, locale)
        const meta = n.kind === 'folder' ? `${size} · ${when}` : `${t(`short.${n.type}` as MessageKey)} · ${size} · ${when}`
        return (
          <div key={n.id} className={'card' + (selected === n.id ? ' is-selected' : '')} style={delay(i)}>
            <button type="button" className="card__open" onClick={() => onOpen(n)}
              aria-label={n.kind === 'folder' ? t('item.openFolder', { name }) : t('item.open', { name })}>
              <Thumb node={n} />
              <span className="card__title">
                <Icon d={type.d} size={18} stroke={type.c} fill={n.kind === 'folder' ? '#fef9c3' : 'none'} />
                <span className="card__name">{name}</span>
              </span>
              <span className="card__meta">{meta}</span>
            </button>
            <button type="button" className="round-btn" aria-label={t('item.info', { name })} aria-pressed={selected === n.id}
              onClick={() => onInfo(n)}>
              <Icon d={icons.info} />
            </button>
          </div>
        )
      })}
    </div>
  )
}

function List({
  items,
  selected,
  sort,
  setSort,
  onOpen,
  onInfo,
  trash,
}: {
  items: Node[]
  selected: string | null
  sort?: Sort
  setSort?: (s: Sort) => void
  onOpen: (n: Node) => void
  onInfo: (n: Node) => void
  trash?: (n: Node) => string
}) {
  const t = useT()
  const locale = useLocale()
  const col = (key: SortKey, label: string) => {
    if (!sort || !setSort) return <span>{label}</span>
    const active = sort.key === key
    return (
      <button type="button" aria-sort={active ? (sort.desc ? 'descending' : 'ascending') : undefined}
        onClick={() => setSort({ key, desc: active ? !sort.desc : key !== 'name' })}>
        {label}
        {active ? (sort.desc ? ' ↓' : ' ↑') : ''}
      </button>
    )
  }
  return (
    <div className={'table' + (trash ? ' table--trash' : '')}>
      <div className="table__inner">
        <div className="table__head">
          <div className="table__cols">
            {col('name', t('col.name'))}
            {trash ? <span>{t('info.deleted')}</span> : col('modified', t('col.modified'))}
            {trash ? <span>{t('col.location')}</span> : col('size', t('col.size'))}
          </div>
          <span />
        </div>
        {items.map((n, i) => {
          const type = fileTypes[n.type]
          const name = displayName(n)
          return (
            <div key={n.id} className={'row' + (selected === n.id ? ' is-selected' : '')} style={delay(i)}>
              <button type="button" className="row__open" onClick={() => onOpen(n)}
                aria-label={n.kind === 'folder' ? t('item.openFolder', { name }) : t('item.open', { name })}>
                <span className="row__name">
                  <Icon d={type.d} stroke={type.c} fill={n.kind === 'folder' ? '#fef9c3' : 'none'} />
                  <span>{name}</span>
                </span>
                <span className="row__cell">{formatWhen(trash ? n.trashedAt! : n.modifiedAt, t, locale)}</span>
                <span className="row__cell">{trash ? trash(n) : sizeLabel(n, t, locale)}</span>
              </button>
              <div className="row__actions">
                <button type="button" className="round-btn" aria-label={t('item.info', { name })}
                  aria-pressed={selected === n.id} onClick={() => onInfo(n)}>
                  <Icon d={icons.info} size={18} />
                </button>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

// Rail is the vertical "You are here" breadcrumb next to the list.
function Rail({ crumbs, drives }: { crumbs: { id: string; name: string; home: boolean }[]; drives: Drive[] }) {
  const t = useT()
  return (
    <nav aria-label={t('rail.label')} className="rail">
      <span className="rail__title">{t('rail.here')}</span>
      {crumbs.map((c, d) => {
        const last = d === crumbs.length - 1
        return (
          <Link key={c.id} href={hrefFor(c.id, drives)} className="rail__item" aria-current={last ? 'page' : undefined}
            style={{ paddingLeft: `${10 + d * 16}px` }}>
            {d > 0 && <span aria-hidden="true" className="rail__line" style={{ left: `${d * 16 - 2}px` }} />}
            {c.home ? (
              <Icon d={icons.home} size={18} />
            ) : (
              <Icon d={icons.folder} size={18} stroke="#a16207" fill="#fef9c3" />
            )}
            <span>{c.name}</span>
          </Link>
        )
      })}
    </nav>
  )
}

function TrashView() {
  const t = useT()
  const { view, setView } = useViewPrefs()
  const [items, setItems] = useState<TrashedNode[] | null>(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<string | null>(null)

  const load = useCallback(() => {
    api.trashList().then(setItems, (err) => setError(errorText(t, err)))
  }, [t])
  useEffect(load, [load])

  const where = (n: TrashedNode) => [driveLabel(t, n.drive), ...n.path.slice(1).map((c) => c.name)].join(' / ')
  const sel = items?.find((n) => n.id === selected) ?? null

  async function restore(n: TrashedNode) {
    try {
      await api.restore(n.id)
      setSelected(null)
      load()
      notifyChanged('trash')
      showToast(t('toast.restored', { folder: where(n) }))
    } catch (err) {
      showToast(errorText(t, err), { error: true })
    }
  }

  if (error) return <p className="form-error" role="alert">{error}</p>
  if (!items) return null
  return (
    <>
      <div className="page-head">
        <div className="page-head__text">
          <div className="page-title">
            <h1>{t('nav.trash')}</h1>
            <span>{plural(t, 'count.items', items.length)}</span>
          </div>
        </div>
        <ViewToggle view={view} setView={setView} />
      </div>
      <div className="files-row">
        <section aria-label={t('nav.trash')} className="files">
          {items.length === 0 ? (
            <div className="empty">
              <Icon d={icons.trash} size={40} stroke="#6b7280" />
              <strong>{t('empty.trash.title')}</strong>
              <span>{t('empty.trash.body')}</span>
            </div>
          ) : view === 'grid' ? (
            <Grid items={items} selected={selected} onOpen={(n) => setSelected(n.id)}
              onInfo={(n) => setSelected(selected === n.id ? null : n.id)} />
          ) : (
            <List items={items} selected={selected} onOpen={(n) => setSelected(n.id)}
              onInfo={(n) => setSelected(selected === n.id ? null : n.id)} trash={(n) => where(n as TrashedNode)} />
          )}
        </section>
        {sel && (
          <InfoPanel
            node={sel}
            location={where(sel)}
            readOnly={sel.drive.role === 'viewer'}
            onClose={() => setSelected(null)}
            onOpen={() => {}}
            onRename={() => {}}
            onMove={() => {}}
            onTrash={() => {}}
            onRestore={() => restore(sel)}
          />
        )}
      </div>
    </>
  )
}
