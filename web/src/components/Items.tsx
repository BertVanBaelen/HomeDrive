import { useState, type ReactNode } from 'react'
import type { Drive, Located, Node, SortKey } from '../api'
import { displayName, formatSize, formatWhen } from '../format'
import { plural, useLocale, useT, type Locale, type MessageKey, type Translate } from '../i18n'
import { folderPath } from '../router'
import { fileTypes, Icon, icons } from './Icon'
import { Link } from './Shell'
import { Thumb } from './Thumb'

export type View = 'grid' | 'list'
export interface Sort {
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

export function useViewPrefs() {
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

export function driveLabel(t: Translate, d: Drive): string {
  return d.kind === 'personal' ? t('nav.myFiles') : t('nav.family')
}

// hrefFor is a folder's URL: drive tops have short ones.
export function hrefFor(id: string, drives: Drive[]): string {
  const d = drives.find((x) => x.rootId === id)
  if (d) return d.kind === 'personal' ? '/' : '/family'
  return folderPath(id)
}

// whereIs is "My Files / Documents" for an item listed outside its folder.
export function whereIs(t: Translate, n: Located): string {
  return [driveLabel(t, n.drive), ...n.path.slice(1).map((c) => c.name)].join(' / ')
}

export function ViewToggle({ view, setView }: { view: View; setView: (v: View) => void }) {
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

export interface Crumb {
  id?: string
  href?: string
  name: string
  home?: boolean
}

// PageHead is breadcrumb + back, title + count, page actions and the view
// toggle.
export function PageHead({
  crumbs,
  title,
  count,
  view,
  setView,
  actions,
}: {
  crumbs: Crumb[]
  title: string
  count: number
  view: View
  setView: (v: View) => void
  actions?: ReactNode
}) {
  const t = useT()
  const parent = crumbs.length > 1 ? crumbs[crumbs.length - 2] : null
  const backLabel = parent ? t('crumbs.backTo', { name: parent.name }) : t('crumbs.back')
  return (
    <div className="page-head">
      <div className="page-head__text">
        {crumbs.length > 0 && (
          <nav aria-label={t('crumbs.label')} className="crumbs">
            {parent?.href ? (
              <Link className="back-btn" href={parent.href} aria-label={backLabel}>
                <Icon d={icons.back} size={18} />
                <span className="back-btn__label">{backLabel}</span>
              </Link>
            ) : (
              <button type="button" className="back-btn" disabled aria-label={backLabel}>
                <Icon d={icons.back} size={18} />
                <span className="back-btn__label">{backLabel}</span>
              </button>
            )}
            <ol>
              {crumbs.map((c, i) => {
                const here = i === crumbs.length - 1
                const inner = (
                  <>
                    {c.home && <Icon d={icons.home} size={16} />}
                    {c.name}
                  </>
                )
                return (
                  <li key={i}>
                    {i > 0 && <Icon d={icons.chevronRight} size={14} stroke="#9ca3af" />}
                    {here || !c.href ? (
                      <span className="crumb" aria-current={here ? 'page' : undefined}>{inner}</span>
                    ) : (
                      <Link className="crumb" href={c.href}>{inner}</Link>
                    )}
                  </li>
                )
              })}
            </ol>
          </nav>
        )}
        <div className="page-title">
          <h1>{title}</h1>
          <span>{plural(t, 'count.items', count)}</span>
        </div>
      </div>
      <div className="page-head__actions">
        {actions}
        <ViewToggle view={view} setView={setView} />
      </div>
    </div>
  )
}

export function Empty({ icon, title, body, children }: { icon: string; title: string; body: string; children?: ReactNode }) {
  return (
    <div className="empty">
      <Icon d={icon} size={40} stroke="#6b7280" />
      <strong>{title}</strong>
      <span>{body}</span>
      {children}
    </div>
  )
}

// A light stagger on first appearance, capped so long lists are quick.
const delay = (i: number) => ({ animationDelay: `${Math.min(i, 8) * 15}ms` })

function sizeLabel(n: Node, t: Translate, locale: Locale) {
  return n.kind === 'folder' ? plural(t, 'count.items', n.childCount ?? 0) : formatSize(n.size, locale)
}

function StarMark() {
  const t = useT()
  return (
    <svg role="img" aria-label={t('item.starred')} width="14" height="14" viewBox="0 0 24 24" fill="#f59e0b" stroke="#f59e0b"
      strokeWidth="1.5" strokeLinejoin="round" className="star-mark">
      <path d={icons.star} />
    </svg>
  )
}

interface ItemsProps<T extends Node> {
  items: T[]
  selected: string | null
  onOpen: (n: T) => void
  onInfo: (n: T) => void
}

export function Grid<T extends Node>({ items, selected, onOpen, onInfo }: ItemsProps<T>) {
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
              <span className="card__meta">
                {n.starred && <StarMark />}
                <span>{meta}</span>
              </span>
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

export function List<T extends Node>({
  items,
  selected,
  sort,
  setSort,
  onOpen,
  onInfo,
  trashed,
  location,
}: ItemsProps<T> & {
  sort?: Sort
  setSort?: (s: Sort) => void
  trashed?: boolean
  location?: (n: T) => string
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
  const variant = trashed ? ' table--trash' : location ? ' table--located' : ''
  return (
    <div className={'table' + variant}>
      <div className="table__inner">
        <div className="table__head">
          <div className="table__cols">
            {col('name', t('col.name'))}
            {trashed ? <span>{t('info.deleted')}</span> : col('modified', t('col.modified'))}
            {!trashed && col('size', t('col.size'))}
            {location && <span>{t('col.location')}</span>}
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
                  {n.starred && <StarMark />}
                </span>
                <span className="row__cell">{formatWhen(trashed ? n.trashedAt! : n.modifiedAt, t, locale)}</span>
                {!trashed && <span className="row__cell">{sizeLabel(n, t, locale)}</span>}
                {location && <span className="row__cell">{location(n)}</span>}
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
export function Rail({ crumbs }: { crumbs: Crumb[] }) {
  const t = useT()
  return (
    <nav aria-label={t('rail.label')} className="rail">
      <span className="rail__title">{t('rail.here')}</span>
      {crumbs.map((c, d) => {
        const last = d === crumbs.length - 1
        return (
          <Link key={d} href={c.href ?? '/'} className="rail__item" aria-current={last ? 'page' : undefined}
            style={{ paddingLeft: `${10 + d * 16}px` }}>
            {d > 0 && <span aria-hidden="true" className="rail__line" style={{ left: `${d * 16 - 2}px` }} />}
            {c.home ? <Icon d={icons.home} size={18} /> : <Icon d={icons.folder} size={18} stroke="#a16207" fill="#fef9c3" />}
            <span>{c.name}</span>
          </Link>
        )
      })}
    </nav>
  )
}

// QuickAccess shows starred folders at the top of My Files.
export function QuickAccess({ folders, drives }: { folders: Located[]; drives: Drive[] }) {
  const t = useT()
  if (!folders.length) return null
  return (
    <section aria-label={t('quick.title')} className="quick">
      <h2>{t('quick.title')}</h2>
      <div className="quick__grid">
        {folders.map((f, i) => (
          <Link key={f.id} href={hrefFor(f.id, drives)} className="quick__item" style={delay(i)}>
            <Icon d={icons.folder} size={24} stroke="#a16207" fill="#fef9c3" />
            <span>
              <strong>{f.name}</strong>
              <span>{plural(t, 'count.items', f.childCount ?? 0)}</span>
            </span>
          </Link>
        ))}
      </div>
    </section>
  )
}
