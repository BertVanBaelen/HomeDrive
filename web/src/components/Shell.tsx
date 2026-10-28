import { useState, type ReactNode } from 'react'
import type { User } from '../api'
import { useT } from '../i18n'
import { navigate } from '../router'
import { Icon, icons } from './Icon'
import { Logo } from './Logo'

export type Section = 'files' | 'family' | 'trash' | null

// Link navigates inside the app without a page load.
export function Link({ href, children, ...rest }: React.AnchorHTMLAttributes<HTMLAnchorElement> & { href: string }) {
  return (
    <a
      href={href}
      {...rest}
      onClick={(e) => {
        if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
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

export function Header({ user, onSignOut }: { user: User; onSignOut: () => void }) {
  const t = useT()
  const [open, setOpen] = useState(false)
  return (
    <header className="app-header">
      <div className="app-header__inner">
        <Link href="/" className="app-header__brand">
          <Logo label={t('app.name')} />
          <span>{t('app.name')}</span>
        </Link>
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
                <button type="button" className="btn btn--block" onClick={onSignOut}>
                  <Icon d={icons.logout} size={18} />
                  {t('account.signOut')}
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    </header>
  )
}

const sections = (t: ReturnType<typeof useT>) =>
  [
    { id: 'files', href: '/', label: t('nav.myFiles'), icon: icons.folder },
    { id: 'family', href: '/family', label: t('nav.family'), icon: icons.family },
    { id: 'trash', href: '/trash', label: t('nav.trash'), icon: icons.trash },
  ] as const

export function Sidebar({
  section,
  trashCount,
  canAdd,
  onAdd,
}: {
  section: Section
  trashCount: number
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
            {s.id === 'trash' && trashCount > 0 && <span className="nav__count">{trashCount}</span>}
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

export function Shell({ children }: { children: ReactNode }) {
  return <div className="app">{children}</div>
}
