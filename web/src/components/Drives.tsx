import { useCallback, useEffect, useId, useState } from 'react'
import { api, type Drive, type DriveRole, type Member, type Person, type User } from '../api'
import { notifyChanged } from '../events'
import { plural, useT, type MessageKey, type Translate } from '../i18n'
import { navigate } from '../router'
import { ConfirmDialog, errorText, Modal, NameDialog } from './Dialogs'
import { Icon, icons } from './Icon'
import { initials } from './Shell'
import { Select } from './Select'
import { showToast } from './Toast'

export const driveRoles: DriveRole[] = ['viewer', 'editor', 'manager']

export function roleLabel(t: Translate, r: DriveRole): string {
  return t(`driveRole.${r}` as MessageKey)
}

// A drive that was just made opens its members dialog once, so its maker
// can add people straight away.
let fresh: string | null = null
function takeFresh(driveId: string): boolean {
  if (fresh !== driveId) return false
  fresh = null
  return true
}

// NewDriveDialog makes a shared drive. onCreated opens it.
export function NewDriveDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (d: Drive) => Promise<void> }) {
  const t = useT()
  return (
    <NameDialog
      title={t('drive.newTitle')}
      subtitle={t('drive.newBody')}
      label={t('drive.name')}
      initial=""
      submit={t('drive.create')}
      maxLength={60}
      onClose={onClose}
      onSubmit={async (name) => {
        const d = await api.createDrive(name)
        fresh = d.id
        await onCreated(d)
        showToast(t('toast.driveCreated', { name: d.name }))
      }}
    />
  )
}

type Open = 'members' | 'menu' | 'rename' | 'delete' | 'leave' | null

// DriveActions are the buttons in a shared drive's header: who is in it,
// and a menu to rename, delete or leave it. onChanged reloads the drive
// after its name or the user's role changed.
export function DriveActions({ drive, me, onChanged }: { drive: Drive; me: User; onChanged: () => void }) {
  const t = useT()
  const [open, setOpen] = useState<Open>(null)
  const [count, setCount] = useState<number | null>(null)
  const manager = drive.role === 'manager'

  useEffect(() => {
    if (takeFresh(drive.id)) setOpen('members')
  }, [drive.id])

  const loadCount = useCallback(() => {
    api.members(drive.id).then((l) => setCount(l.length), () => {})
  }, [drive.id])
  useEffect(loadCount, [loadCount])

  useEffect(() => {
    if (open !== 'menu') return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(null)
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open])

  // gone leaves the drive's pages after the user left or deleted it.
  function gone(toast: MessageKey) {
    setOpen(null)
    notifyChanged('drives')
    navigate('/', true)
    showToast(t(toast, { name: drive.name }))
  }

  const pick = (o: Open) => () => setOpen(o)

  return (
    <>
      <button type="button" className="btn btn--small" onClick={pick('members')}
        aria-label={count === null ? t('members.button') : plural(t, 'members.buttonAria', count)}>
        <Icon d={icons.users} size={18} />
        <span className="hide-mobile">{t('members.button')}</span>
        {count !== null && <span className="btn__count">{count}</span>}
      </button>
      <div className="menu-anchor">
        <button type="button" className="btn btn--small btn--square" aria-label={t('drive.more', { name: drive.name })}
          aria-expanded={open === 'menu'} onClick={() => setOpen(open === 'menu' ? null : 'menu')}>
          <Icon d={icons.dots} size={18} />
        </button>
        {open === 'menu' && (
          <>
            <button type="button" aria-label={t('common.close')} className="popover-scrim" onClick={pick(null)} />
            <div className="popover popover--menu" role="group" aria-label={t('drive.more', { name: drive.name })}>
              <div className="menu">
                {manager && (
                  <button type="button" onClick={pick('rename')}>
                    <Icon d={icons.pencil} size={18} />
                    {t('drive.rename')}
                  </button>
                )}
                <button type="button" onClick={pick('leave')}>
                  <Icon d={icons.logout} size={18} />
                  {t('drive.leave')}
                </button>
                {manager && (
                  <button type="button" className="menu__danger" onClick={pick('delete')}>
                    <Icon d={icons.trash} size={18} />
                    {t('drive.delete')}
                  </button>
                )}
              </div>
            </div>
          </>
        )}
      </div>

      {open === 'members' && (
        <MembersDialog driveId={drive.id} name={drive.name} me={me} role={drive.role}
          onClose={() => {
            setOpen(null)
            loadCount()
          }}
          onLeave={pick('leave')}
          onSelfChanged={onChanged} />
      )}
      {open === 'rename' && (
        <NameDialog title={t('drive.renameTitle')} label={t('drive.name')} initial={drive.name} submit={t('rename.submit')}
          maxLength={60} onClose={pick(null)}
          onSubmit={async (name) => {
            await api.renameDrive(drive.id, name)
            setOpen(null)
            onChanged()
            showToast(t('toast.renamed'))
          }} />
      )}
      {open === 'delete' && (
        <ConfirmDialog title={t('drive.deleteTitle', { name: drive.name })} body={t('drive.deleteBody')}
          confirm={t('drive.deleteConfirm')} onClose={pick(null)}
          onConfirm={async () => {
            await api.deleteDrive(drive.id)
            gone('toast.driveDeleted')
          }} />
      )}
      {open === 'leave' && (
        <ConfirmDialog title={t('drive.leaveTitle', { name: drive.name })}
          body={t(manager ? 'drive.leaveBodyManager' : 'drive.leaveBody')}
          confirm={t('drive.leaveConfirm')} onClose={pick(null)}
          onConfirm={async () => {
            await api.removeMember(drive.id, me.id)
            gone('toast.driveLeft')
          }} />
      )}
    </>
  )
}

// MembersDialog shows who is in a shared drive. Managers add people,
// change roles and remove members. Admins do the same from the users page
// through the admin API, which is audited and works without being a member.
export function MembersDialog({
  driveId,
  name,
  me,
  role,
  admin = false,
  onClose,
  onLeave,
  onSelfChanged,
}: {
  driveId: string
  name: string
  me: User
  role?: DriveRole
  admin?: boolean
  onClose: () => void
  onLeave?: () => void
  onSelfChanged?: () => void
}) {
  const t = useT()
  const addId = useId()
  const [members, setMembers] = useState<Member[] | null>(null)
  const [people, setPeople] = useState<Person[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(
    () =>
      (admin
        ? api.adminDrives().then((l) => l.find((d) => d.id === driveId)?.members ?? [])
        : api.members(driveId)
      ).then(setMembers, (err) => setError(errorText(t, err))),
    [admin, driveId, t],
  )
  useEffect(() => {
    load()
  }, [load])

  // The user's role as the list says, so it follows changes made here.
  const myRole = members?.find((m) => m.userId === me.id)?.role ?? role
  const manager = admin || myRole === 'manager'
  useEffect(() => {
    if (manager) api.people().then(setPeople, () => {})
  }, [manager])

  async function change(userId: string, run: () => Promise<void>) {
    setBusy(userId)
    setError('')
    try {
      await run()
      await load()
      if (userId === me.id) onSelfChanged?.()
    } catch (err) {
      setError(errorText(t, err))
    }
    setBusy(null)
  }
  const setRole = (userId: string, r: DriveRole) =>
    change(userId, () => (admin ? api.adminSetMember : api.setMember)(driveId, userId, r))
  const remove = (userId: string) =>
    change(userId, () => (admin ? api.adminRemoveMember : api.removeMember)(driveId, userId))

  const candidates = members ? people.filter((p) => !members.some((m) => m.userId === p.id)) : []

  return (
    <Modal title={t('members.title', { name })}
      subtitle={members ? plural(t, 'count.members', members.length) : undefined} onClose={onClose}>
      {members && (
        <ul className="members">
          {members.map((m) => {
            const you = m.userId === me.id
            // Managers leave with the button below; admins may remove anyone.
            const removable = admin || !you
            return (
              <li key={m.userId} className={'member' + (m.disabled ? ' is-disabled' : '')}>
                <span className="member__avatar" aria-hidden="true">{initials(m.name)}</span>
                <span className="member__text">
                  <strong>
                    {m.name}
                    {you && <span className="member__you"> ({t('users.you')})</span>}
                  </strong>
                  <span>{m.email}</span>
                </span>
                {m.disabled && <span className="badge badge--disabled">{t('status.disabled')}</span>}
                {manager ? (
                  <span className="member__actions">
                    <Select size="small" value={m.role} disabled={busy === m.userId}
                      ariaLabel={t('members.roleFor', { name: m.name })} title={t('members.roleFor', { name: m.name })}
                      options={driveRoles.map((r) => ({ value: r, label: roleLabel(t, r), hint: t(`driveRole.${r}.hint`) }))}
                      onChange={(r) => setRole(m.userId, r)} />
                    {removable ? (
                      <button type="button" className="icon-btn" aria-label={t('members.remove', { name: m.name })}
                        disabled={busy === m.userId} onClick={() => remove(m.userId)}>
                        <Icon d={icons.close} size={18} />
                      </button>
                    ) : (
                      <span className="member__spacer" />
                    )}
                  </span>
                ) : (
                  <span className="member__role">{roleLabel(t, m.role)}</span>
                )}
              </li>
            )
          })}
        </ul>
      )}
      {members && manager && (
        <section className="add-people" aria-labelledby={addId}>
          <h3 id={addId}>{t('members.add')}</h3>
          {candidates.length > 0 ? (
            <>
              <div className="chips">
                {candidates.map((p) => (
                  <button key={p.id} type="button" className="chip" title={p.email} disabled={busy === p.id}
                    aria-label={t('members.addPerson', { name: p.name, email: p.email })}
                    onClick={() => setRole(p.id, 'editor')}>
                    <span className="chip__avatar" aria-hidden="true">{initials(p.name)}</span>
                    <span className="chip__name">{p.id === me.id ? t('members.addMe') : p.name}</span>
                    <Icon d={icons.plus} size={16} width={2.5} className="chip__plus" />
                  </button>
                ))}
              </div>
              <p className="add-people__hint">{t('members.addHint')}</p>
            </>
          ) : (
            <p className="add-people__none">{t('members.everyone')}</p>
          )}
        </section>
      )}
      <p className="members__roles">{t(admin ? 'members.adminNote' : 'members.roles')}</p>
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className={'modal__foot' + (onLeave ? ' modal__foot--split' : '')}>
        {onLeave && (
          <button type="button" className="btn btn--danger" onClick={onLeave}>
            <Icon d={icons.logout} size={18} />
            {t('drive.leave')}
          </button>
        )}
        <button type="button" className="btn btn--primary" onClick={onClose}>{t('common.done')}</button>
      </div>
    </Modal>
  )
}