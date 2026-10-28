import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import type { User } from '../api'
import { useT, type Translate } from '../i18n'
import { navigate } from '../router'
import { Icon, icons } from './Icon'
import { Logo } from './Logo'

export type Section = 'files' | 'family' | 'recent' | 'starred' | 'trash' | null

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

function initials(name: string): string {
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
    { id: 'family', href: '/family', label: t('nav.family'), icon: icons.family },
    { id: 'recent', href: '/recent', label: t('nav.recent'), icon: icons.recent },
    { id: 'starred', href: '/starred', label: t('nav.starred'), icon: icons.star },
    { id: 'trash', href: '/trash', label: t('nav.trash'), icon: icons.trash },
  ] as const

export function Sidebar({
  section,
  counts,
  canAdd,
  onAdd,
}: {
  section: Section
  counts: Partial<Record<NonNullable<Section>, number>>
  canAdd: boolean
  onAdd: () => void
}) {
  const t = useT()
  return (
    <nav className="sidebar" aria-label={t('nav.sections')}>
      <button type="button" className="btn btn--primary sidebar__new" onClick={onAdd} disabled={!canAdd}>
        <Icon d={icons.plus} size={22} width={2.25} />
        {t('nav.new')}
      </button>
      <div className="nav">
        {sections(t).map((s) => (
          <Link key={s.id} href={s.href} className="nav__item" aria-current={section === s.id ? 'page' : undefined}>
            <Icon d={s.icon} />
            <span>{s.label}</span>
            {!!counts[s.id] && <span className="nav__count">{counts[s.id]}</span>}
          </Link>
        ))}
      </div>
    </nav>
  )
}

export function TabBar({ section }: { section: Section }) {
  const t = useT()
  return (
    <nav className="tabbar" aria-label={t('nav.tabs')}>
      {sections(t).map((s) => (
        <Link key={s.id} href={s.href} aria-current={section === s.id ? 'page' : undefined}>
          <Icon d={s.icon} size={22} />
          {s.id === 'files' ? t('nav.filesTab') : s.label}
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
