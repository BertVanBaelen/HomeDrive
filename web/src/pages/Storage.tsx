import { useCallback, useEffect, useId, useMemo, useState, type ReactNode } from 'react'
import {
  api,
  type Disk,
  type Drive,
  type HouseholdStorage,
  type Located,
  type Node,
  type StorageCategory,
  type StoragePage,
  type StorageUsage,
  type User,
} from '../api'
import { ConfirmDialog, errorText } from '../components/Dialogs'
import { fileTypes, Icon, icons } from '../components/Icon'
import { useItemActions } from '../components/ItemActions'
import { PageHead, whereIs, type Crumb } from '../components/Items'
import { Link } from '../components/Shell'
import { showToast } from '../components/Toast'
import { notifyChanged, onChanged } from '../events'
import { displayName, formatAmount, formatSize } from '../format'
import { plural, useLocale, useT, type Locale, type MessageKey, type Translate } from '../i18n'
import { BackupCard } from './Backup'

// The categories in the order of the bar, in the file types' colours.
const categories: [StorageCategory, string][] = [
  ['documents', fileTypes.doc.c],
  ['spreadsheets', fileTypes.sheet.c],
  ['pdfs', fileTypes.pdf.c],
  ['images', fileTypes.image.c],
  ['video', fileTypes.video.c],
  ['audio', fileTypes.audio.c],
  ['archives', fileTypes.archive.c],
  ['other', fileTypes.code.c],
]

const diskColors = { homedrive: '#15803d', other: '#9ca3af', free: '#e5e7eb' }
const usageColors = { files: '#15803d', trash: '#dc2626', oldVersions: '#d97706' }

// The data disk counts as nearly full from here, like the email to admins.
const nearlyFull = 0.85

interface Seg {
  label: string
  color: string
  value: number
}

function percent(value: number, total: number, locale: Locale): string {
  if (!total || !value) return (0).toLocaleString(locale, { style: 'percent' })
  if (value / total < 0.01) return '<' + (0.01).toLocaleString(locale, { style: 'percent' })
  return (value / total).toLocaleString(locale, { style: 'percent', maximumFractionDigits: 0 })
}

// Bar is a segmented bar of total; what the segments leave is the track.
function Bar({ segs, total, thin }: { segs: Seg[]; total: number; thin?: boolean }) {
  return (
    <div className={'storage-bar' + (thin ? ' storage-bar--thin' : '')} aria-hidden="true">
      {total > 0 &&
        segs
          .filter((s) => s.value > 0)
          .map((s) => (
            <span key={s.label} style={{ width: `${(s.value / total) * 100}%`, background: s.color }} />
          ))}
    </div>
  )
}

function Legend({ segs, total }: { segs: Seg[]; total: number }) {
  const locale = useLocale()
  return (
    <ul className="storage-legend">
      {segs.map((s) => (
        <li key={s.label}>
          <span className="storage-legend__dot" style={{ background: s.color }} />
          <span className="storage-legend__text">
            <strong>{s.label}</strong>
            <span>
              {formatAmount(s.value, locale)} · {percent(s.value, total, locale)}
            </span>
          </span>
        </li>
      ))}
    </ul>
  )
}

// Card is a titled panel; level is its heading's.
function Card({
  title,
  level = 2,
  className,
  children,
}: {
  title: string
  level?: 2 | 3
  className?: string
  children: ReactNode
}) {
  const id = useId()
  const H = level === 2 ? 'h2' : 'h3'
  return (
    <section className={'panel' + (className ? ' ' + className : '')} aria-labelledby={id}>
      <H id={id}>{title}</H>
      {children}
    </section>
  )
}

// StorageView is the Storage page: what the user's files take and what
// they can free, and for admins the data disk, everyone's usage and the
// backup.
export function StorageView({ drives, user }: { drives: Drive[]; user: User }) {
  const t = useT()
  const locale = useLocale()
  const admin = user.role === 'admin'
  const [page, setPage] = useState<StoragePage | null>(null)
  const [household, setHousehold] = useState<HouseholdStorage | null>(null)
  const [error, setError] = useState('')
  const [confirm, setConfirm] = useState<'trash' | 'versions' | null>(null)

  const load = useCallback(() => {
    api.storage().then(setPage, (err) => setError(errorText(t, err)))
    if (admin) api.householdStorage().then(setHousehold, (err) => setError(errorText(t, err)))
  }, [admin, t])
  useEffect(load, [load])
  useEffect(() => onChanged((id) => (id === 'trash' || id === 'storage') && load()), [load])

  const actions = useItemActions({ drives, me: user, reload: load })
  const where = useMemo(() => (n: Node) => whereIs(t, n as Located), [t])

  if (error) return <p className="form-error" role="alert">{error}</p>
  if (!page) return null

  const crumbs: Crumb[] = [{ name: t('nav.myFiles'), icon: icons.home, href: '/' }, { name: t('storage.title') }]
  const disk = household?.disk
  const note = disk ? t('storage.freeOf', { free: formatAmount(disk.free, locale), total: formatAmount(disk.total, locale) }) : ''
  const mine = page.myFiles
  const typeSegs: Seg[] = categories
    .map(([c, color]) => ({ label: t(`storage.cat.${c}` as MessageKey), color, value: mine.types[c] ?? 0 }))
    .filter((s) => s.value > 0)
  const days = plural(t, 'count.days', page.retentionDays)

  return (
    <>
      <PageHead crumbs={crumbs} title={t('storage.title')} note={note} />
      <Card title={t('storage.mine')} className="storage-card">
        <div className="storage-total">
          <span className="storage-total__big">{formatAmount(mine.files, locale)}</span>
          <span>{t('storage.inMyFiles')}</span>
        </div>
        {mine.files > 0 ? (
          <>
            <Bar segs={typeSegs} total={mine.files} />
            <Legend segs={typeSegs} total={mine.files} />
          </>
        ) : (
          <p className="panel__body storage-card__empty">{t('storage.mineEmpty')}</p>
        )}
        <dl className="storage-lines">
          <div>
            <dt>
              <Icon d={icons.trash} size={16} />
              {t('storage.inTrash')}
            </dt>
            <dd>{formatAmount(mine.trash, locale)}</dd>
          </div>
          <div>
            <dt>
              <Icon d={icons.archive} size={16} />
              {t('storage.oldVersions')}
            </dt>
            <dd>{formatAmount(mine.oldVersions, locale)}</dd>
          </div>
        </dl>
      </Card>

      <div className="storage-grid">
        <Card title={t('storage.largest')}>
          {page.largest.length === 0 ? (
            <p className="panel__body">{t('storage.largestNone')}</p>
          ) : (
            <ul className="largest">
              {page.largest.map((n) => {
                const type = fileTypes[n.type]
                const name = displayName(n)
                return (
                  <li key={n.id}>
                    <Icon d={type.d} stroke={type.c} />
                    <button type="button" className="largest__open" aria-label={t('item.open', { name })}
                      onClick={() => actions.open(n, page.largest, where)}>
                      <span className="largest__name">{name}</span>
                      <span className="largest__meta">
                        {t(`short.${n.type}` as MessageKey)} · {where(n)}
                      </span>
                    </button>
                    <span className="largest__size">{formatSize(n.size, locale)}</span>
                    <button type="button" className="icon-btn largest__trash" aria-label={t('storage.trashAria', { name })}
                      title={t('storage.trashAria', { name })} onClick={() => actions.trash(n)}>
                      <Icon d={icons.trash} size={18} />
                    </button>
                  </li>
                )
              })}
            </ul>
          )}
        </Card>

        <Card title={t('storage.freeUp')}>
          <div className="cleanup">
            <span className="cleanup__icon cleanup__icon--trash">
              <Icon d={icons.trash} />
            </span>
            <span className="cleanup__text">
              <strong>{t('nav.trash')}</strong>
              <span>{page.trash ? t('storage.frees', { size: formatAmount(page.trash, locale) }) : t('storage.trashNone')}</span>
            </span>
            <span className="cleanup__actions">
              <Link href="/trash" className="btn btn--small">{t('storage.open')}</Link>
              <button type="button" className="btn btn--small btn--danger" disabled={!page.trash} onClick={() => setConfirm('trash')}>
                {t('storage.empty')}
              </button>
            </span>
          </div>
          <div className="cleanup">
            <span className="cleanup__icon cleanup__icon--versions">
              <Icon d={icons.archive} />
            </span>
            <span className="cleanup__text">
              <strong>{t('storage.oldVersions')}</strong>
              <span>
                {page.oldVersions
                  ? t('storage.oldSome', { size: formatAmount(page.oldVersions, locale), days })
                  : t('storage.oldNone', { days })}
              </span>
            </span>
            <span className="cleanup__actions">
              <button type="button" className="btn btn--small btn--danger" disabled={!page.oldVersions}
                onClick={() => setConfirm('versions')}>
                {t('storage.delete')}
              </button>
            </span>
          </div>
        </Card>
      </div>

      {admin && household && <Household household={household} />}

      {actions.elements}
      {confirm === 'trash' && (
        <ConfirmDialog title={t('trash.emptyTitle')} body={t('trash.emptyBody')} confirm={t('trash.empty')}
          onClose={() => setConfirm(null)}
          onConfirm={async () => {
            await api.emptyTrash()
            setConfirm(null)
            notifyChanged('trash')
            notifyChanged('starred')
            showToast(t('toast.trashEmptied'))
          }} />
      )}
      {confirm === 'versions' && (
        <ConfirmDialog title={t('storage.deleteOldTitle')} body={t('storage.deleteOldBody')} confirm={t('storage.deleteOld')}
          onClose={() => setConfirm(null)}
          onConfirm={async () => {
            const { freed } = await api.deleteOldVersions()
            setConfirm(null)
            notifyChanged('storage')
            showToast(t('storage.oldDeleted', { size: formatAmount(freed, locale) }))
          }} />
      )}
    </>
  )
}

// Household is the admins' part: the data disk, everyone's usage and the
// backup. Sizes only, no file names.
function Household({ household }: { household: HouseholdStorage }) {
  const t = useT()
  const headingId = useId()
  return (
    <section className="storage-household" aria-labelledby={headingId}>
      <div className="storage-household__head">
        <h2 id={headingId}>{t('storage.household')}</h2>
        <p>{t('storage.householdBody')}</p>
      </div>
      <DiskCard disk={household.disk} />
      <div className="storage-grid">
        <Card title={t('storage.people')} level={3}>
          <UsageList rows={household.people.map((p) => ({ ...p, badge: p.disabled ? t('status.disabled') : undefined }))} />
        </Card>
        <Card title={t('nav.sharedDrives')} level={3}>
          {household.drives.length === 0 ? (
            <p className="panel__body">{t('adminDrives.none')}</p>
          ) : (
            <UsageList rows={household.drives.map((d) => ({ ...d, extra: plural(t, 'count.members', d.members) }))} />
          )}
        </Card>
      </div>
      <BackupCard />
    </section>
  )
}

function DiskCard({ disk }: { disk: Disk | null }) {
  const t = useT()
  const locale = useLocale()
  if (!disk) {
    return (
      <Card title={t('storage.disk')} level={3}>
        <p className="panel__body">{t('storage.diskUnknown')}</p>
      </Card>
    )
  }
  const homedrive = Math.min(disk.homedrive, disk.used)
  const segs: Seg[] = [
    { label: t('storage.diskHomedrive'), color: diskColors.homedrive, value: homedrive },
    { label: t('storage.diskOther'), color: diskColors.other, value: disk.used - homedrive },
  ]
  const full = disk.total > 0 && disk.used / disk.total >= nearlyFull
  return (
    <Card title={t('storage.disk')} level={3}>
      <div className="storage-total">
        <span className="storage-total__big">{formatAmount(disk.used, locale)}</span>
        <span>{t('storage.usedOf', { total: formatAmount(disk.total, locale) })}</span>
        <span className={'storage-total__free' + (full ? ' storage-total__free--low' : '')}>
          {t('storage.freeSize', { size: formatAmount(disk.free, locale) })}
        </span>
      </div>
      <Bar segs={segs} total={disk.total} />
      <Legend segs={[...segs, { label: t('storage.diskFree'), color: diskColors.free, value: disk.free }]} total={disk.total} />
      <p className="storage-note">{t('storage.diskReserve', { size: formatAmount(disk.reserve, locale) })}</p>
    </Card>
  )
}

type UsageRow = StorageUsage & { id: string; name: string; badge?: string; extra?: string }

const totalOf = (u: StorageUsage) => u.files + u.trash + u.oldVersions

function usageParts(t: Translate, locale: Locale, r: UsageRow): string[] {
  const parts = r.extra ? [r.extra] : []
  if (r.trash || r.oldVersions) {
    parts.push(t('storage.rowFiles', { size: formatAmount(r.files, locale) }))
    if (r.trash) parts.push(t('storage.rowTrash', { size: formatAmount(r.trash, locale) }))
    if (r.oldVersions) parts.push(t('storage.rowOld', { size: formatAmount(r.oldVersions, locale) }))
  }
  return parts
}

// UsageList is people or shared drives, each with a bar relative to the
// largest.
function UsageList({ rows }: { rows: UsageRow[] }) {
  const t = useT()
  const locale = useLocale()
  const max = Math.max(1, ...rows.map(totalOf))
  return (
    <ul className="usage">
      {rows.map((r) => {
        const parts = usageParts(t, locale, r)
        return (
          <li key={r.id}>
            <div className="usage__top">
              <span className="usage__name">
                <span>{r.name}</span>
                {r.badge && <span className="badge badge--disabled">{r.badge}</span>}
              </span>
              <span className="usage__size">{formatAmount(totalOf(r), locale)}</span>
            </div>
            <Bar thin total={max}
              segs={[
                { label: 'files', color: usageColors.files, value: r.files },
                { label: 'trash', color: usageColors.trash, value: r.trash },
                { label: 'oldVersions', color: usageColors.oldVersions, value: r.oldVersions },
              ]} />
            {parts.length > 0 && <span className="usage__meta">{parts.join(' · ')}</span>}
          </li>
        )
      })}
    </ul>
  )
}
