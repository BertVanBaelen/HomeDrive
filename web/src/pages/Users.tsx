import { useCallback, useEffect, useId, useState, type FormEvent } from 'react'
import { api, type AdminDrive, type User, type UserInfo } from '../api'
import { errorText, Modal } from '../components/Dialogs'
import { MembersDialog } from '../components/Drives'
import { Icon, icons } from '../components/Icon'
import { showToast } from '../components/Toast'
import { notifyChanged, onChanged } from '../events'
import { formatSize, formatWhen } from '../format'
import { plural, useLocale, useT } from '../i18n'
import { Field } from './Login'

// Users is the admin page: invite people, send password links, change
// roles, disable accounts, and manage who is in each shared drive.
export function Users({ me }: { me: User }) {
  const t = useT()
  const locale = useLocale()
  const [users, setUsers] = useState<UserInfo[] | null>(null)
  const [inviting, setInviting] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(() => {
    api.users().then(setUsers, (err) => showToast(errorText(t, err), { error: true }))
  }, [t])
  useEffect(load, [load])

  async function act(u: UserInfo, run: () => Promise<unknown>, done: string) {
    setBusy(u.id)
    try {
      await run()
      load()
      showToast(done)
    } catch (err) {
      showToast(errorText(t, err), { error: true })
    }
    setBusy(null)
  }

  return (
    <div className="settings settings--wide">
      <div className="settings__head">
        <div className="page-title">
          <h1>{t('users.title')}</h1>
          {users && <span>{plural(t, 'count.people', users.length)}</span>}
        </div>
        <button type="button" className="btn btn--primary" onClick={() => setInviting(true)}>
          <Icon d={icons.userAdd} size={18} />
          {t('users.invite')}
        </button>
      </div>

      <ul className="people">
        {users?.map((u) => {
          const you = u.id === me.id
          return (
            <li key={u.id} className={'person' + (u.status === 'disabled' ? ' is-disabled' : '')}>
              <span className="person__avatar" aria-hidden="true">{u.name.slice(0, 1).toUpperCase()}</span>
              <div className="person__text">
                <strong>
                  {u.name}
                  {you && <span className="person__you"> ({t('users.you')})</span>}
                </strong>
                <span>{u.email}</span>
                <span className="person__meta">
                  <span className={'badge' + (u.role === 'admin' ? ' badge--green' : '')}>
                    {u.role === 'admin' && <Icon d={icons.shield} size={14} />}
                    {t(u.role === 'admin' ? 'role.admin' : 'role.member')}
                  </span>
                  <span className={'badge badge--' + u.status}>{t(`status.${u.status}`)}</span>
                  <span>
                    {u.lastSeenAt ? t('account.lastActive', { when: formatWhen(u.lastSeenAt, t, locale) }) : t('users.never')}
                  </span>
                </span>
              </div>
              <div className="person__actions" role="group" aria-label={t('users.actions', { name: u.name })}>
                {u.status !== 'disabled' && (
                  <button type="button" className="btn btn--small" disabled={busy === u.id}
                    onClick={() => act(u, () => api.sendLink(u.id), t('users.linkSent', { email: u.email }))}>
                    <Icon d={u.status === 'invited' ? icons.send : icons.key} size={16} />
                    {t(u.status === 'invited' ? 'users.resend' : 'users.reset')}
                  </button>
                )}
                {!you && (
                  <>
                    <button type="button" className="btn btn--small" disabled={busy === u.id}
                      onClick={() => act(u, () => api.updateUser(u.id, { role: u.role === 'admin' ? 'member' : 'admin' }), t('users.saved'))}>
                      <Icon d={icons.shield} size={16} />
                      {t(u.role === 'admin' ? 'users.makeMember' : 'users.makeAdmin')}
                    </button>
                    <button type="button" className={'btn btn--small' + (u.status === 'disabled' ? '' : ' btn--danger')}
                      disabled={busy === u.id}
                      onClick={() => act(u, () => api.updateUser(u.id, { disabled: u.status !== 'disabled' }), t('users.saved'))}>
                      <Icon d={u.status === 'disabled' ? icons.check : icons.ban} size={16} />
                      {t(u.status === 'disabled' ? 'users.enable' : 'users.disable')}
                    </button>
                  </>
                )}
              </div>
            </li>
          )
        })}
      </ul>

      <AdminDrives me={me} />

      {inviting && (
        <InviteDialog onClose={() => setInviting(false)}
          onDone={() => {
            load()
            // The invitee joined drives, so their member lists changed.
            notifyChanged('drives')
          }} />
      )}
    </div>
  )
}

// AdminDrives lists every shared drive with its size and members. Admins
// manage the members and pick the drives new people join; they don't see
// the files unless they are a member themselves.
function AdminDrives({ me }: { me: User }) {
  const t = useT()
  const locale = useLocale()
  const headingId = useId()
  const [drives, setDrives] = useState<AdminDrive[] | null>(null)
  const [managing, setManaging] = useState<AdminDrive | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(() => {
    api.adminDrives().then(setDrives, (err) => showToast(errorText(t, err), { error: true }))
  }, [t])
  useEffect(load, [load])
  useEffect(() => onChanged((id) => id === 'drives' && load()), [load])

  async function toggleDefault(d: AdminDrive) {
    setBusy(d.id)
    try {
      await api.setJoinByDefault(d.id, !d.joinByDefault)
      load()
      showToast(t('users.saved'))
    } catch (err) {
      showToast(errorText(t, err), { error: true })
    }
    setBusy(null)
  }

  return (
    <section className="admin-drives" aria-labelledby={headingId}>
      <div className="admin-drives__head">
        <h2 id={headingId}>{t('nav.sharedDrives')}</h2>
        <p>{t('adminDrives.body')}</p>
      </div>
      {drives?.length === 0 && <p className="admin-drives__none">{t('adminDrives.none')}</p>}
      <ul className="people">
        {drives?.map((d) => (
          <li key={d.id} className="person admin-drive">
            <span className="person__avatar admin-drive__icon" aria-hidden="true">
              <Icon d={icons.family} size={20} />
            </span>
            <div className="person__text">
              <strong>{d.name}</strong>
              <span>
                {d.size ? formatSize(d.size, locale) : t('adminDrives.empty')} · {plural(t, 'count.members', d.members.length)}
              </span>
              <span className="admin-drive__names">
                {d.members.map((m) => m.name).join(', ') || t('adminDrives.noMembers')}
              </span>
              <label className="check">
                <input type="checkbox" checked={d.joinByDefault} disabled={busy === d.id} onChange={() => toggleDefault(d)} />
                <span>{t('adminDrives.joinByDefault')}</span>
              </label>
            </div>
            <div className="person__actions">
              <button type="button" className="btn btn--small" onClick={() => setManaging(d)}
                aria-label={t('adminDrives.manageAria', { name: d.name })}>
                <Icon d={icons.users} size={16} />
                {t('adminDrives.manage')}
              </button>
            </div>
          </li>
        ))}
      </ul>
      {managing && (
        <MembersDialog driveId={managing.id} name={managing.name} me={me} admin
          onClose={() => {
            setManaging(null)
            load()
          }} />
      )}
    </section>
  )
}

function InviteDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const t = useT()
  const locale = useLocale()
  const drivesId = useId()
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [role, setRole] = useState('member')
  const [lang, setLang] = useState<string>(locale)
  const [drives, setDrives] = useState<AdminDrive[]>([])
  // undefined until the drives load: the server then picks the default ones.
  const [picked, setPicked] = useState<string[] | undefined>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.adminDrives().then(
      (l) => {
        setDrives(l)
        setPicked(l.filter((d) => d.joinByDefault).map((d) => d.id))
      },
      () => {},
    )
  }, [])

  const toggle = (id: string) =>
    setPicked((p = []) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]))

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const r = await api.inviteUser({ name, email, role, locale: lang, driveIds: picked })
      onDone()
      if (r.error) {
        // The account exists, the email didn't go out.
        setError(t('users.mailFailed'))
        setBusy(false)
        return
      }
      onClose()
      showToast(t('users.invited', { email: r.user.email }))
    } catch (err) {
      setError(errorText(t, err))
      setBusy(false)
    }
  }

  return (
    <Modal title={t('users.inviteTitle')} subtitle={t('users.inviteBody')} onClose={onClose}>
      <form className="form" onSubmit={submit}>
        <Field label={t('account.name')} required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
        <Field label={t('login.email')} type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        <div className="form__row">
          <div className="field">
            <label htmlFor="invite-role">{t('users.role')}</label>
            <select id="invite-role" value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="member">{t('role.member')}</option>
              <option value="admin">{t('role.admin')}</option>
            </select>
          </div>
          <div className="field">
            <label htmlFor="invite-lang">{t('users.language')}</label>
            <select id="invite-lang" value={lang} onChange={(e) => setLang(e.target.value)}>
              <option value="en">English</option>
              <option value="nl">Nederlands</option>
            </select>
          </div>
        </div>
        {drives.length > 0 && (
          <fieldset className="checks" aria-describedby={drivesId}>
            <legend>{t('users.inviteDrives')}</legend>
            {drives.map((d) => (
              <label key={d.id} className="check">
                <input type="checkbox" checked={!!picked?.includes(d.id)} onChange={() => toggle(d.id)} />
                <Icon d={icons.family} size={18} />
                <span>{d.name}</span>
              </label>
            ))}
            <span id={drivesId} className="field__hint">{t('users.inviteDrivesHint')}</span>
          </fieldset>
        )}
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="modal__foot">
          <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button type="submit" className="btn btn--primary" disabled={busy}>
            <Icon d={icons.send} size={18} />
            {t('users.send')}
          </button>
        </div>
      </form>
    </Modal>
  )
}
