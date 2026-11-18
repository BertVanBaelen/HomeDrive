import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { api, type Drive, type StorageMeter as Meter, type User } from '../api'
import { onChanged } from '../events'
import { formatAmount } from '../format'
import { useLocale, useT, type Translate } from '../i18n'
import { drivePath, navigate } from '../router'
import { onBatchDone } from '../uploads'
import { Icon, icons } from './Icon'
import { Logo } from './Logo'

// Section is the navigation entry for the current page. Inside a shared
// drive it is 'shared'; the sidebar then marks the drive itself. Inside
// something shared with the user it is 'sharedWithMe'.
export type Section = 'files' | 'shared' | 'sharedWithMe' | 'recent' | 'starred' | 'trash' | 'storage' | null

// Link navigates inside the app without a page load.
export function Link({ href, children, ...rest }: React.AnchorHTMLAttributes<HTMLAnchorElement> & { href: string }) {
  return (
    <a
      href={href}
      {...rest}
      onClick={(e) => {
        rest.onClick?.(e)
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
        e.preventDefault()
        navigate(href)
      }}
    >
      {children}
    </a>
  )
}

export function initials(name: string): string {
  const parts = name.trim().split(/\s+/)
  return ((parts[0]?.[0] ?? '') + (parts.length > 1 ? parts[parts.length - 1][0] : '')).toUpperCase()
}

// Where clearing the search box returns to.
let beforeSearch = '/'

// Search filters across every folder as you type (design: header search).
function Search({ query }: { query: string }) {
  const t = useT()
  const id = useId()
  const [value, setValue] = useState(query)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)

  // Follow the address bar (back/forward, clearing elsewhere).
  useEffect(() => setValue(query), [query])

  function change(v: string) {
    setValue(v)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => {
      const onSearch = location.pathname === '/search'
      if (v.trim()) {
        if (!onSearch) beforeSearch = location.pathname + location.search
        navigate(`/search?q=${encodeURIComponent(v.trim())}`, onSearch)
      } else if (onSearch) navigate(beforeSearch)
    }, 250)
  }

  return (
    <div className="search" role="search">
      <Icon d={icons.search} stroke="#6b7280" className="search__icon" />
      <label htmlFor={id} className="sr-only">{t('search.label')}</label>
      <input id={id} type="search" placeholder={t('search.placeholder')} value={value} autoComplete="off"
        onChange={(e) => change(e.target.value)}
        onKeyDown={(e) => e.key === 'Escape' && value && change('')} />
      {value && (
        <button type="button" aria-label={t('search.clear')} className="icon-btn search__clear" onClick={() => change('')}>
          <Icon d={icons.close} size={16} />
        </button>
      )}
    </div>
  )
}

export function Header({ user, query, onSignOut, trashCount = 0 }: { user: User; query?: string; onSignOut: () => void; trashCount?: number }) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const go = (href: string) => {
    setOpen(false)
    navigate(href)
  }
  return (
    <header className="app-header">
      <div className="app-header__inner">
        <Link href="/" className="app-header__brand">
          <Logo label={t('app.name')} />
          <span>{t('app.name')}</span>
        </Link>
        {query !== undefined && <Search query={query} />}
        <div className="app-header__end">
          <button type="button" className="avatar" aria-expanded={open} aria-label={t('account.signedInAs', { name: user.name })}
            onClick={() => setOpen(!open)}>
            {initials(user.name)}
          </button>
          {open && (
            <>
              <button type="button" aria-label={t('common.close')} className="popover-scrim" onClick={() => setOpen(false)} />
              <div role="dialog" aria-label={t('account.signedInAs', { name: user.name })} className="popover">
                <div className="popover__who">
                  <strong>{user.name}</strong>
                  <span>{user.email}</span>
                </div>
                <div className="menu">
                  <button type="button" onClick={() => go('/account')}>
                    <Icon d={icons.cog} size={18} />
                    {t('account.menu')}
                  </button>
                  <button type="button" onClick={() => go('/storage')}>
                    <Icon d={icons.storage} size={18} />
                    {t('storage.title')}
                  </button>
                  <button type="button" className="account-menu__phone-trash" onClick={() => go('/trash')}>
                    <Icon d={icons.trash} size={18} />
                    {t('nav.trash')}
                    {!!trashCount && <span className="nav__count">{trashCount}</span>}
                  </button>
                  {user.role === 'admin' && (
                    <button type="button" onClick={() => go('/users')}>
                      <Icon d={icons.users} size={18} />
                      {t('account.users')}
                    </button>
                  )}
                  <button type="button" onClick={onSignOut}>
                    <Icon d={icons.logout} size={18} />
                    {t('account.signOut')}
                  </button>
                </div>
              </div>
            </>
          )}
        </div>
      </div>
    </header>
  )
}

const sections = (t: Translate) =>
  [
    { id: 'files', href: '/', label: t('nav.myFiles'), icon: icons.folder },
    { id: 'recent', href: '/recent', label: t('nav.recent'), icon: icons.recent },
    { id: 'starred', href: '/starred', label: t('nav.starred'), icon: icons.star },
    { id: 'trash', href: '/trash', label: t('nav.trash'), icon: icons.trash },
  ] as const

type NavSection = { id: NonNullable<Section>; href: string; label: string; icon: string }
type Counts = Partial<Record<NonNullable<Section>, number>>

function NavItem({ s, section, counts }: { s: NavSection; section: Section; counts: Counts }) {
  return (
    <Link href={s.href} className="nav__item" aria-current={section === s.id ? 'page' : undefined}>
      <Icon d={s.icon} />
      <span>{s.label}</span>
      {!!counts[s.id] && <span className="nav__count">{counts[s.id]}</span>}
    </Link>
  )
}

// Sidebar is the desktop navigation: My Files, the shared drives, then the
// lists that span them.
export function Sidebar({
  section,
  drives,
  driveId,
  counts,
  canAdd,
  onAdd,
  onNewDrive,
}: {
  section: Section
  drives: Drive[]
  driveId: string | null
  counts: Counts
  canAdd: boolean
  onAdd: () => void
  onNewDrive: () => void
}) {
  const t = useT()
  const headingId = useId()
  const [files, ...lists] = sections(t)
  const sharedWithMe: NavSection = { id: 'sharedWithMe', href: '/shared', label: t('nav.sharedWithMe'), icon: icons.shared }
  return (
    <nav className="sidebar" aria-label={t('nav.sections')}>
      <button type="button" className="btn btn--primary sidebar__new" onClick={onAdd} disabled={!canAdd}>
        <Icon d={icons.plus} size={22} width={2.25} />
        {t('nav.new')}
      </button>
      <div className="nav">
        <NavItem s={files} section={section} counts={counts} />
      </div>
      <div className="nav" role="group" aria-labelledby={headingId}>
        <h2 id={headingId} className="nav__heading">{t('nav.sharedDrives')}</h2>
        {drives.filter((d) => d.kind === 'shared').map((d) => (
          <Link key={d.id} href={drivePath(d.id)} className="nav__item" aria-current={driveId === d.id ? 'page' : undefined}>
            <Icon d={icons.family} />
            <span className="nav__name">{d.name}</span>
          </Link>
        ))}
        <button type="button" className="nav__item nav__item--new" onClick={onNewDrive}>
          <Icon d={icons.plus} />
          <span>{t('drive.new')}</span>
        </button>
      </div>
      <div className="nav">
        {[sharedWithMe, ...lists].map((s) => (
          <NavItem key={s.id} s={s} section={section} counts={counts} />
        ))}
      </div>
      <StorageMeter current={section === 'storage'} />
    </nav>
  )
}

// StorageMeter closes the sidebar and opens the Storage page: what My
// Files takes, or for admins how full the data disk is.
function StorageMeter({ current }: { current: boolean }) {
  const t = useT()
  const locale = useLocale()
  const [meter, setMeter] = useState<Meter | null>(null)

  // Anything that changes files may change the numbers; a burst of changes
  // is read once.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    const load = () => api.storageMeter().then(setMeter, () => {})
    const later = () => {
      clearTimeout(timer)
      timer = setTimeout(load, 500)
    }
    load()
    const offChanged = onChanged(later)
    const offBatch = onBatchDone(later)
    return () => {
      clearTimeout(timer)
      offChanged()
      offBatch()
    }
  }, [])

  if (!meter) return null
  const disk = meter.disk
  const used = disk ? disk.used : meter.used
  const full = disk && disk.total > 0 ? disk.used / disk.total : 0
  return (
    <Link href="/storage" className="meter" aria-current={current ? 'page' : undefined}>
      <span className="meter__title">
        <Icon d={icons.storage} size={18} />
        {t('storage.title')}
      </span>
      {disk && (
        <span className={'meter__bar' + (full >= 0.85 ? ' meter__bar--full' : '')}>
          <span style={{ width: `${Math.min(100, full * 100)}%` }} />
        </span>
      )}
      <span className="meter__text">
        {t('storage.used', { size: formatAmount(used, locale) })}
        {disk && (
          <span>{t('storage.freeOf', { free: formatAmount(disk.free, locale), total: formatAmount(disk.total, locale) })}</span>
        )}
      </span>
    </Link>
  )
}

// TabBar is the phone's navigation. Shared lists the shared drives and
// what was shared with the user.
export function TabBar({
  section,
  canAdd,
  onAdd,
}: {
  section: Section
  canAdd: boolean
  onAdd: () => void
}) {
  const t = useT()
  const [files] = sections(t)
  const current = section === 'sharedWithMe' ? 'shared' : section
  return (
    <nav className="tabbar" aria-label={t('nav.tabs')}>
      <Link href="/" aria-current={current === 'files' ? 'page' : undefined}>
        <Icon d={files.icon} size={22} />
        {t('nav.filesTab')}
      </Link>
      <Link href="/shared" aria-current={current === 'shared' ? 'page' : undefined}>
        <Icon d={icons.family} size={22} />
        {t('nav.sharedTab')}
      </Link>
      <button
        type="button"
        className="tabbar__add"
        disabled={!canAdd}
        title={canAdd ? t('nav.addNew') : t('nav.addUnavailable')}
        aria-label={t('nav.addNew')}
        onClick={onAdd}
      >
        <span><Icon d={icons.plus} size={22} width={2.25} /></span>
        {t('nav.addNew')}
      </button>
      <Link href="/recent" aria-current={current === 'recent' ? 'page' : undefined}>
        <Icon d={icons.recent} size={22} />
        {t('nav.recentTab')}
      </Link>
      <Link href="/starred" aria-current={current === 'starred' ? 'page' : undefined}>
        <Icon d={icons.star} size={22} />
        {t('nav.starredTab')}
      </Link>
    </nav>
  )
}

// Page is the frame for the pages outside the file browser (account,
// users, save): header and a centred column.
export function Page({ user, onSignOut, children }: { user: User; onSignOut: () => void; children: ReactNode }) {
  return (
    <div className="app">
      <Header user={user} onSignOut={onSignOut} />
      <main className="page">{children}</main>
    </div>
  )
}
