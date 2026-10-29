import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from 'react'
import { api, type Details, type Drive, type Located, type Node, type SharedItem, type User } from '../api'
import { AddDialog, ConfirmDialog, errorText } from '../components/Dialogs'
import { DriveActions, NewDriveDialog, roleLabel } from '../components/Drives'
import { Icon, icons } from '../components/Icon'
import { InfoPanel, type InfoActions } from '../components/InfoPanel'
import { useItemActions } from '../components/ItemActions'
import {
  driveIcon,
  driveLabel,
  Empty,
  Grid,
  hrefFor,
  List,
  PageHead,
  QuickAccess,
  Rail,
  useViewPrefs,
  ViewToggle,
  whereIs,
  type Crumb,
} from '../components/Items'
import { Fab, Header, Link, Sidebar, TabBar, type Section } from '../components/Shell'
import { showToast } from '../components/Toast'
import { UploadPanel } from '../components/UploadPanel'
import { notifyChanged, onChanged } from '../events'
import { displayName, formatWhen } from '../format'
import { plural, useLocale, useT } from '../i18n'
import { drivePath, folderPath, navigate, type ListName, type Route } from '../router'
import { leaveFolder, playFolderMotion, takePrefetched } from '../motion'
import { addUploads, onBatchDone } from '../uploads'
import { StorageView } from './Storage'

export function Browser({ user, route, onSignOut }: { user: User; route: Route; onSignOut: () => void }) {
  const t = useT()
  const [drives, setDrives] = useState<Drive[] | null>(null)
  const [counts, setCounts] = useState({ trash: 0, starred: 0 })
  const [addOpen, setAddOpen] = useState(false)
  const [newDrive, setNewDrive] = useState(false)
  const [folderDrive, setFolderDrive] = useState<Drive | null>(null)
  const [error, setError] = useState('')

  const reloadDrives = useCallback(
    () => api.drives().then(setDrives, (err) => setError(errorText(t, err))),
    [t],
  )
  useEffect(() => {
    reloadDrives()
    return onChanged((id) => id === 'drives' && reloadDrives())
  }, [reloadDrives])

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

  // v1's /family address opens the Family drive, or the first shared one.
  useEffect(() => {
    if (route.name !== 'family' || !drives) return
    const shared = drives.filter((d) => d.kind === 'shared')
    const family = shared.find((d) => /^(family|familie)$/i.test(d.name)) ?? shared[0]
    navigate(family ? drivePath(family.id) : '/shared', true)
  }, [route.name, drives])

  // v1's Backup page is part of Storage.
  useEffect(() => {
    if (route.name === 'backup') navigate('/storage', true)
  }, [route.name])

  let section: Section = null
  let folderId: string | null = null
  let driveId: string | null = null
  let missing = false
  if (route.name === 'trash') section = 'trash'
  else if (route.name === 'storage') section = 'storage'
  else if (route.name === 'list' && route.list !== 'search') section = route.list
  else if (route.name === 'shared') section = 'sharedWithMe'
  else if (drives && route.name === 'drive') {
    section = 'shared'
    driveId = route.driveId
    const d = drives.find((x) => x.id === route.driveId && x.kind === 'shared')
    if (d) folderId = d.rootId
    else missing = true
  } else if (drives && route.name === 'files') {
    folderId = route.folderId ?? drives.find((d) => d.kind === 'personal')?.rootId ?? null
    if (!route.folderId || folderDrive?.kind === 'personal') section = 'files'
    else if (folderDrive?.kind === 'share') section = 'sharedWithMe'
    else if (folderDrive) {
      section = 'shared'
      driveId = folderDrive.id
    }
  }
  const canAdd = !!folderId && !!folderDrive && folderDrive.role !== 'viewer'

  return (
    <div className="app">
      <Header user={user} onSignOut={onSignOut} query={route.name === 'list' && route.list === 'search' ? route.q : ''} />
      <div className="app-body">
        <Sidebar section={section} drives={drives ?? []} driveId={driveId} counts={counts} canAdd={canAdd}
          onAdd={() => setAddOpen(true)} onNewDrive={() => setNewDrive(true)} />
        <main className="main">
          {error && <p className="form-error" role="alert">{error}</p>}
          {drives && folderId && (
            <FolderView key={folderId} folderId={folderId} drives={drives} user={user} addOpen={addOpen}
              setAddOpen={setAddOpen} onDrive={setFolderDrive} />
          )}
          {missing && (
            <Empty icon={icons.family} title={t('drive.missingTitle')} body={t('drive.missingBody')}>
              <Link href="/shared" className="btn btn--primary">{t('drive.toShared')}</Link>
            </Empty>
          )}
          {drives && route.name === 'shared' && (
            <SharedView drives={drives} user={user} onNewDrive={() => setNewDrive(true)} />
          )}
          {drives && route.name === 'list' && (
            <LocatedView key={route.list} list={route.list} q={route.q} drives={drives} user={user} />
          )}
          {drives && route.name === 'trash' && <TrashView />}
          {drives && route.name === 'storage' && <StorageView drives={drives} user={user} />}
        </main>
      </div>
      <TabBar section={section} />
      {canAdd && <Fab onAdd={() => setAddOpen(true)} />}
      {newDrive && (
        <NewDriveDialog onClose={() => setNewDrive(false)}
          onCreated={async (d) => {
            await reloadDrives()
            setNewDrive(false)
            navigate(drivePath(d.id))
          }} />
      )}
    </div>
  )
}

const phoneQuery = '(max-width: 699px)'
function onPhoneChange(fn: () => void) {
  const m = matchMedia(phoneQuery)
  m.addEventListener('change', fn)
  return () => m.removeEventListener('change', fn)
}

// usePhone follows the phone layout's breakpoint (styles/app.css).
function usePhone(): boolean {
  return useSyncExternalStore(onPhoneChange, () => matchMedia(phoneQuery).matches)
}

type ItemActions = ReturnType<typeof useItemActions>

// infoActions wires the info panel's buttons for an item.
function infoActions(actions: ItemActions, n: Node, onOpen: () => void, unselect: () => void): InfoActions {
  return {
    onOpen,
    onRename: () => actions.rename(n),
    onMove: () => actions.move(n),
    onTrash: () => {
      unselect()
      actions.trash(n)
    },
    onStar: () => actions.star(n),
    onShare: () => actions.share(n),
  }
}

// SharedView is Shared with me: what people shared with the user, with who
// shared it and when. On a phone it is the Shared tab, which first lists
// the shared drives.
function SharedView({ drives, user, onNewDrive }: { drives: Drive[]; user: User; onNewDrive: () => void }) {
  const t = useT()
  const locale = useLocale()
  const phone = usePhone()
  const headingId = useId()
  const { view, setView } = useViewPrefs()
  const [items, setItems] = useState<SharedItem[] | null>(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<string | null>(null)

  const load = useCallback(() => {
    api.sharedWithMe().then(
      (l) => {
        setItems(l)
        setError('')
      },
      (err) => setError(errorText(t, err)),
    )
  }, [t])
  useEffect(load, [load])
  useEffect(() => onChanged((id) => id === 'starred' && load()), [load])

  const actions = useItemActions({ drives, me: user, reload: load })
  const where = useMemo(() => (n: Node) => whereIs(t, n as Located), [t])

  const title = t('nav.sharedWithMe')
  const sel = items?.find((n) => n.id === selected) ?? null
  const open = (n: Node) => actions.open(n, items ?? [], where)
  const info = (n: Node) => setSelected(selected === n.id ? null : n.id)

  let content: ReactNode = null
  if (error) content = <p className="form-error" role="alert">{error}</p>
  else if (items?.length === 0) {
    content = phone ? (
      <p className="shared-none">{t('empty.sharedWithMe.body')}</p>
    ) : (
      <Empty icon={icons.shared} title={t('empty.sharedWithMe.title')} body={t('empty.sharedWithMe.body')} />
    )
  } else if (items) {
    content = view === 'grid' ? (
      <Grid items={items} selected={selected} onOpen={open} onInfo={info}
        meta={(n) => `${t('shared.by', { name: n.sharedBy })} · ${formatWhen(n.sharedAt, t, locale)}`} />
    ) : (
      <List items={items} selected={selected} onOpen={open} onInfo={info} location={(n) => n.sharedBy}
        locationLabel={t('col.sharedBy')} date={{ label: t('col.sharedAt'), at: (n) => n.sharedAt }} />
    )
  }
  const files = (
    <div className="files-row">
      <section aria-label={phone ? undefined : title} className="files">{content}</section>
      {sel && (
        <InfoPanel node={sel} drive={sel.drive} location={where(sel)} readOnly={sel.drive.role === 'viewer'}
          onClose={() => setSelected(null)} actions={infoActions(actions, sel, () => open(sel), () => setSelected(null))} />
      )}
    </div>
  )

  if (!phone) {
    if (!items && !error) return null
    return (
      <>
        <PageHead crumbs={[]} title={title} count={items?.length ?? 0} view={view} setView={setView} />
        {files}
        {actions.elements}
      </>
    )
  }

  const shared = drives.filter((d) => d.kind === 'shared')
  const newButton = (cls: string) => (
    <button type="button" className={cls} onClick={onNewDrive}>
      <Icon d={icons.plus} size={18} width={2.25} />
      {t('drive.new')}
    </button>
  )
  return (
    <>
      <PageHead crumbs={[]} title={t('nav.sharedDrives')} note={plural(t, 'count.drives', shared.length)}
        actions={shared.length > 0 && newButton('btn btn--small btn--primary')} />
      {shared.length === 0 ? (
        <Empty icon={icons.family} title={t('empty.shared.title')} body={t('empty.shared.body')}>
          {newButton('btn btn--primary')}
        </Empty>
      ) : (
        <ul className="drive-list">
          {shared.map((d) => (
            <li key={d.id}>
              <Link href={drivePath(d.id)} className="drive-row">
                <span className="drive-row__icon" aria-hidden="true">
                  <Icon d={icons.family} size={22} />
                </span>
                <span className="drive-row__text">
                  <strong>{d.name}</strong>
                  <span>{roleLabel(t, d.role)}</span>
                </span>
                <Icon d={icons.chevronRight} size={18} stroke="#9ca3af" />
              </Link>
            </li>
          ))}
        </ul>
      )}
      <section className="shared-section" aria-labelledby={headingId}>
        <div className="shared-section__head">
          <h2 id={headingId}>{title}</h2>
          {!!items?.length && <ViewToggle view={view} setView={setView} />}
        </div>
        {(items || error) && files}
      </section>
      {actions.elements}
    </>
  )
}

function FolderView({
  folderId,
  drives,
  user,
  addOpen,
  setAddOpen,
  onDrive,
}: {
  folderId: string
  drives: Drive[]
  user: User
  addOpen: boolean
  setAddOpen: (b: boolean) => void
  onDrive: (d: Drive) => void
}) {
  const t = useT()
  const { view, setView, sort, setSort } = useViewPrefs()
  // When the folder was opened with a zoom, its contents are already here.
  const [ahead] = useState(() => takePrefetched(folderId, sort.key, sort.desc))
  const [details, setDetails] = useState<Details | null>(ahead?.details ?? null)
  const [items, setItems] = useState<Node[] | null>(ahead?.items ?? null)
  const [quick, setQuick] = useState<Located[]>([])
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const [dragging, setDragging] = useState(false)
  const [zoomed, setZoomed] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const filesRef = useRef<HTMLElement>(null)
  const motionPlayed = useRef(false)

  // Zoom in (opened from its card) or out (came up from a subfolder) once
  // the cards are there, before the browser paints them.
  useLayoutEffect(() => {
    if (motionPlayed.current || !items || !filesRef.current) return
    motionPlayed.current = true
    if (view === 'grid' && playFolderMotion(folderId, filesRef.current)) setZoomed(true)
  }, [items, folderId, view])
  useLayoutEffect(() => () => leaveFolder(folderId), [folderId])

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

  const actions = useItemActions({ drives, me: user, reload: load })
  const sel = items?.find((n) => n.id === selected) ?? null

  // Inside something shared with the user, the path starts at the shared
  // item: the folders above it aren't theirs to see.
  const inShare = details?.drive.kind === 'share'
  const crumbs: Crumb[] = details
    ? [
        ...(inShare ? [{ name: t('nav.sharedWithMe'), href: '/shared', icon: icons.shared }] : []),
        ...[
          { id: details.drive.rootId, name: driveLabel(t, details.drive), icon: driveIcon(details.drive) },
          ...details.path.slice(1),
          ...(isRoot ? [] : [{ id: details.node.id, name: details.node.name }]),
        ].map((c) => ({ ...c, href: inShare ? folderPath(c.id) : hrefFor(c.id, drives) })),
      ]
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
      <PageHead crumbs={crumbs} title={title} count={items.length} view={view} setView={setView}
        actions={isRoot && details.drive.kind === 'shared' && (
          <DriveActions drive={details.drive} me={user}
            onChanged={() => {
              load()
              notifyChanged('drives')
            }} />
        )} />
      <UploadPanel />
      {isMyFiles && <QuickAccess folders={quick} drives={drives} />}

      <div className="files-row">
        <section ref={filesRef} aria-label={title} className="files">
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
            <Grid items={items} selected={selected} onOpen={open} onInfo={info} zoomed={zoomed} />
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
            drive={details.drive}
            location={location}
            readOnly={readOnly}
            onClose={() => setSelected(null)}
            actions={infoActions(actions, sel, () => open(sel), () => setSelected(null))}
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
function LocatedView({ list, q, drives, user }: { list: ListName; q: string; drives: Drive[]; user: User }) {
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

  const actions = useItemActions({ drives, me: user, reload: load })
  const where = useMemo(() => (n: Node) => whereIs(t, n as Located), [t])

  if (error) return <p className="form-error" role="alert">{error}</p>
  if (!items) return null

  const title = list === 'search' ? t('search.title', { q }) : t(list === 'recent' ? 'nav.recent' : 'nav.starred')
  const crumbs: Crumb[] = [
    { name: t('nav.myFiles'), icon: icons.home, href: '/' },
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
            drive={sel.drive}
            location={where(sel)}
            readOnly={sel.drive.role === 'viewer'}
            onClose={() => setSelected(null)}
            actions={infoActions(actions, sel, () => open(sel), () => setSelected(null))}
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
  const [confirm, setConfirm] = useState<'empty' | Located | null>(null)

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
  // What the user trashed inside something shared with them waits for the
  // drive's editors to delete it for good.
  const deletable = (n: Located) => n.drive.kind !== 'share'
  return (
    <>
      <PageHead crumbs={[]} title={t('nav.trash')} count={items.length} view={view} setView={setView}
        actions={items.some(deletable) && (
          <button type="button" className="btn btn--small btn--danger" onClick={() => setConfirm('empty')}>
            <Icon d={icons.trash} size={16} />
            {t('trash.empty')}
          </button>
        )} />
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
          <InfoPanel node={sel} drive={sel.drive} location={where(sel)} readOnly={sel.drive.role === 'viewer'}
            onClose={() => setSelected(null)} onRestore={() => restore(sel)}
            onDeleteForever={deletable(sel) ? () => setConfirm(sel) : undefined} />
        )}
      </div>
      {confirm === 'empty' && (
        <ConfirmDialog title={t('trash.emptyTitle')} body={t('trash.emptyBody')} confirm={t('trash.empty')}
          onClose={() => setConfirm(null)}
          onConfirm={async () => {
            await api.emptyTrash()
            setConfirm(null)
            setSelected(null)
            notifyChanged('trash')
            notifyChanged('starred')
            showToast(t('toast.trashEmptied'))
          }} />
      )}
      {confirm && confirm !== 'empty' && (
        <ConfirmDialog title={t('trash.deleteTitle', { name: displayName(confirm) })} body={t('trash.deleteBody')}
          confirm={t('trash.deleteForever')} onClose={() => setConfirm(null)}
          onConfirm={async () => {
            await api.deleteForever(confirm.id)
            setConfirm(null)
            setSelected(null)
            notifyChanged('trash')
            notifyChanged('starred')
            showToast(t('toast.deletedForever'))
          }} />
      )}
    </>
  )
}
