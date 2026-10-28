import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, type Details, type Drive, type Located, type Node, type User } from '../api'
import { AddDialog, errorText } from '../components/Dialogs'
import { Icon, icons } from '../components/Icon'
import { InfoPanel } from '../components/InfoPanel'
import { useItemActions } from '../components/ItemActions'
import {
  driveLabel,
  Empty,
  Grid,
  hrefFor,
  List,
  PageHead,
  QuickAccess,
  Rail,
  useViewPrefs,
  whereIs,
  type Crumb,
} from '../components/Items'
import { Fab, Header, Sidebar, TabBar, type Section } from '../components/Shell'
import { showToast } from '../components/Toast'
import { UploadPanel } from '../components/UploadPanel'
import { notifyChanged, onChanged } from '../events'
import { plural, useT } from '../i18n'
import type { ListName, Route } from '../router'
import { addUploads, onBatchDone } from '../uploads'

export function Browser({ user, route, onSignOut }: { user: User; route: Route; onSignOut: () => void }) {
  const t = useT()
  const [drives, setDrives] = useState<Drive[] | null>(null)
  const [counts, setCounts] = useState({ trash: 0, starred: 0 })
  const [addOpen, setAddOpen] = useState(false)
  const [folderDrive, setFolderDrive] = useState<Drive | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api.drives().then(setDrives, (err) => setError(errorText(t, err)))
  }, [t])

  // Sidebar counts follow changes made anywhere in the app.
  useEffect(() => {
    const trash = () => api.trashList().then((l) => setCounts((c) => ({ ...c, trash: l.length })), () => {})
    const starred = () => api.starred().then((l) => setCounts((c) => ({ ...c, starred: l.length })), () => {})
    trash()
    starred()
    return onChanged((id) => {
      if (id === 'trash') trash()
      if (id === 'starred') starred()
    })
  }, [])

  useEffect(() => onBatchDone((n) => showToast(plural(t, 'toast.uploaded', n))), [t])

  let section: Section = null
  let folderId: string | null = null
  if (route.name === 'trash') section = 'trash'
  else if (route.name === 'list' && route.list !== 'search') section = route.list
  else if (drives && route.name === 'family') {
    section = 'family'
    folderId = drives.find((d) => d.kind === 'shared')?.rootId ?? null
  } else if (drives && route.name === 'files') {
    folderId = route.folderId ?? drives.find((d) => d.kind === 'personal')?.rootId ?? null
    section = !route.folderId || folderDrive?.kind === 'personal' ? 'files' : folderDrive ? 'family' : null
  }
  const canAdd = !!folderId && !!folderDrive && folderDrive.role !== 'viewer'

  return (
    <div className="app">
      <Header user={user} onSignOut={onSignOut} query={route.name === 'list' && route.list === 'search' ? route.q : ''} />
      <div className="app-body">
        <Sidebar section={section} counts={counts} canAdd={canAdd} onAdd={() => setAddOpen(true)} />
        <main className="main">
          {error && <p className="form-error" role="alert">{error}</p>}
          {drives && folderId && (
            <FolderView key={folderId} folderId={folderId} drives={drives} addOpen={addOpen}
              setAddOpen={setAddOpen} onDrive={setFolderDrive} />
          )}
          {drives && route.name === 'list' && (
            <LocatedView key={route.list + route.q} list={route.list} q={route.q} drives={drives} />
          )}
          {drives && route.name === 'trash' && <TrashView />}
        </main>
      </div>
      <TabBar section={section} />
      {canAdd && <Fab onAdd={() => setAddOpen(true)} />}
    </div>
  )
}

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
  const [quick, setQuick] = useState<Located[]>([])
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
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
  const isMyFiles = isRoot && drive?.kind === 'personal'
  const title = !details ? '' : isRoot ? driveLabel(t, details.drive) : details.node.name

  // Starred folders as quick access at the top of My Files.
  useEffect(() => {
    if (!isMyFiles) return
    const loadQuick = () => api.starred().then((l) => setQuick(l.filter((n) => n.kind === 'folder')), () => {})
    loadQuick()
    return onChanged((id) => id === 'starred' && loadQuick())
  }, [isMyFiles])

  const actions = useItemActions({ drives, reload: load })
  const sel = items?.find((n) => n.id === selected) ?? null

  const crumbs: Crumb[] = details
    ? [
        { id: details.drive.rootId, name: driveLabel(t, details.drive), home: true },
        ...details.path.slice(1),
        ...(isRoot ? [] : [{ id: details.node.id, name: details.node.name }]),
      ].map((c) => ({ ...c, href: hrefFor(c.id, drives) }))
    : []
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
      if (hasFiles(e) && --depth <= 0) setDragging(false)
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

  if (error && !details) return <p className="form-error" role="alert">{error}</p>
  if (!details || !items) return null

  const open = (n: Node) => actions.open(n, items, () => location)
  const info = (n: Node) => setSelected(selected === n.id ? null : n.id)

  return (
    <>
      <PageHead crumbs={crumbs} title={title} count={items.length} view={view} setView={setView} />
      <UploadPanel />
      {isMyFiles && <QuickAccess folders={quick} drives={drives} />}

      <div className="files-row">
        <section aria-label={title} className="files">
          {items.length === 0 ? (
            <Empty icon={icons.folder} title={t('empty.folder.title')} body={t('empty.folder.body')}>
              {!readOnly && (
                <button type="button" className="btn btn--primary" onClick={() => setAddOpen(true)}>
                  <Icon d={icons.plus} size={18} width={2.25} />
                  {t('empty.add')}
                </button>
              )}
            </Empty>
          ) : view === 'grid' ? (
            <Grid items={items} selected={selected} onOpen={open} onInfo={info} />
          ) : (
            <div className="list-wrap">
              {!isRoot && <Rail crumbs={crumbs} />}
              <List items={items} selected={selected} sort={sort} setSort={setSort} onOpen={open} onInfo={info} />
            </div>
          )}
        </section>
        {sel && (
          <InfoPanel
            node={sel}
            location={location}
            readOnly={readOnly}
            onClose={() => setSelected(null)}
            actions={{
              onOpen: () => open(sel),
              onRename: () => actions.rename(sel),
              onMove: () => actions.move(sel),
              onTrash: () => {
                setSelected(null)
                actions.trash(sel)
              },
              onStar: () => actions.star(sel),
            }}
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
      {actions.elements}
    </>
  )
}

// LocatedView lists items from across the drives: Recent, Starred or
// search results.
function LocatedView({ list, q, drives }: { list: ListName; q: string; drives: Drive[] }) {
  const t = useT()
  const { view, setView } = useViewPrefs()
  const [items, setItems] = useState<Located[] | null>(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<string | null>(null)

  const load = useCallback(() => {
    const fetch = list === 'recent' ? api.recent() : list === 'starred' ? api.starred() : api.search(q)
    fetch.then(setItems, (err) => setError(errorText(t, err)))
  }, [list, q, t])
  useEffect(load, [load])
  useEffect(() => onChanged((id) => (id === 'starred' || id === 'trash') && load()), [load])

  const actions = useItemActions({ drives, reload: load })
  const where = useMemo(() => (n: Node) => whereIs(t, n as Located), [t])

  if (error) return <p className="form-error" role="alert">{error}</p>
  if (!items) return null

  const title = list === 'search' ? t('search.title', { q }) : t(list === 'recent' ? 'nav.recent' : 'nav.starred')
  const crumbs: Crumb[] = [
    { name: t('nav.myFiles'), home: true, href: '/' },
    { name: list === 'search' ? t('search.crumb') : title },
  ]
  const empty = {
    recent: [icons.recent, t('empty.recent.title'), t('empty.recent.body')],
    starred: [icons.star, t('empty.starred.title'), t('empty.starred.body')],
    search: [icons.search, t('empty.search.title'), t('empty.search.body')],
  }[list]
  const sel = items.find((n) => n.id === selected) ?? null
  const open = (n: Node) => actions.open(n, items, where)
  const info = (n: Node) => setSelected(selected === n.id ? null : n.id)

  return (
    <>
      <PageHead crumbs={crumbs} title={title} count={items.length} view={view} setView={setView} />
      <div className="files-row">
        <section aria-label={title} className="files">
          {items.length === 0 ? (
            <Empty icon={empty[0]} title={empty[1]} body={empty[2]} />
          ) : view === 'grid' ? (
            <Grid items={items} selected={selected} onOpen={open} onInfo={info} />
          ) : (
            <List items={items} selected={selected} onOpen={open} onInfo={info} location={where} />
          )}
        </section>
        {sel && (
          <InfoPanel
            node={sel}
            location={where(sel)}
            readOnly={sel.drive.role === 'viewer'}
            onClose={() => setSelected(null)}
            actions={{
              onOpen: () => open(sel),
              onRename: () => actions.rename(sel),
              onMove: () => actions.move(sel),
              onTrash: () => {
                setSelected(null)
                actions.trash(sel)
              },
              onStar: () => actions.star(sel),
            }}
          />
        )}
      </div>
      {actions.elements}
    </>
  )
}

function TrashView() {
  const t = useT()
  const { view, setView } = useViewPrefs()
  const [items, setItems] = useState<Located[] | null>(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<string | null>(null)

  const load = useCallback(() => {
    api.trashList().then(setItems, (err) => setError(errorText(t, err)))
  }, [t])
  useEffect(load, [load])
  useEffect(() => onChanged((id) => id === 'trash' && load()), [load])

  const where = (n: Node) => whereIs(t, n as Located)
  const sel = items?.find((n) => n.id === selected) ?? null

  async function restore(n: Located) {
    try {
      await api.restore(n.id)
      setSelected(null)
      notifyChanged('trash')
      notifyChanged('starred')
      showToast(t('toast.restored', { folder: where(n) }))
    } catch (err) {
      showToast(errorText(t, err), { error: true })
    }
  }

  if (error) return <p className="form-error" role="alert">{error}</p>
  if (!items) return null
  const info = (n: Node) => setSelected(selected === n.id ? null : n.id)
  return (
    <>
      <PageHead crumbs={[]} title={t('nav.trash')} count={items.length} view={view} setView={setView} />
      <div className="files-row">
        <section aria-label={t('nav.trash')} className="files">
          {items.length === 0 ? (
            <Empty icon={icons.trash} title={t('empty.trash.title')} body={t('empty.trash.body')} />
          ) : view === 'grid' ? (
            <Grid items={items} selected={selected} onOpen={(n) => setSelected(n.id)} onInfo={info} />
          ) : (
            <List items={items} selected={selected} onOpen={(n) => setSelected(n.id)} onInfo={info} trashed location={where} />
          )}
        </section>
        {sel && (
          <InfoPanel node={sel} location={where(sel)} readOnly={sel.drive.role === 'viewer'}
            onClose={() => setSelected(null)} onRestore={() => restore(sel)} />
        )}
      </div>
    </>
  )
}
