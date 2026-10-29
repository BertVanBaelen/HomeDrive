import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import type { Drive, User } from '../api'
import { useT, type Translate } from '../i18n'
import { drivePath, navigate } from '../router'
import { Icon, icons } from './Icon'
import { Logo } from './Logo'

// Section is the navigation entry for the current page. Inside a shared
// drive it is 'shared'; the sidebar then marks the drive itself. Inside
// something shared with the user it is 'sharedWithMe'.
export type Section = 'files' | 'shared' | 'sharedWithMe' | 'recent' | 'starred' | 'trash' | null

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

export function Header({ user, query, onSignOut }: { user: User; query?: string; onSignOut: () => void }) {
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
                  {user.role === 'admin' && (
                    <>
                      <button type="button" onClick={() => go('/users')}>
                        <Icon d={icons.users} size={18} />
                        {t('account.users')}
                      </button>
                      <button type="button" onClick={() => go('/backup')}>
                        <Icon d={icons.shield} size={18} />
                        {t('backup.title')}
                      </button>
                    </>
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
    </nav>
  )
}

// TabBar is the phone's navigation. Shared lists the shared drives and
// what was shared with the user.
export function TabBar({ section }: { section: Section }) {
  const t = useT()
  const [files, ...lists] = sections(t)
  const tabs = [
    { id: 'files', href: files.href, label: t('nav.filesTab'), icon: files.icon },
    { id: 'shared', href: '/shared', label: t('nav.sharedTab'), icon: icons.family },
    ...lists,
  ]
  const current = section === 'sharedWithMe' ? 'shared' : section
  return (
    <nav className="tabbar" aria-label={t('nav.tabs')}>
      {tabs.map((s) => (
        <Link key={s.id} href={s.href} aria-current={current === s.id ? 'page' : undefined}>
          <Icon d={s.icon} size={22} />
          {s.label}
        </Link>
      ))}
    </nav>
  )
}

export function Fab({ onAdd }: { onAdd: () => void }) {
  const t = useT()
  return (
    <button type="button" aria-label={t('nav.addNew')} className="fab" onClick={onAdd}>
      <Icon d={icons.plus} size={26} width={2.25} />
    </button>
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
