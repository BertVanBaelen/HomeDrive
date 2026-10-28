import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { api, type User, type UserInfo } from '../api'
import { errorText, Modal } from '../components/Dialogs'
import { Icon, icons } from '../components/Icon'
import { showToast } from '../components/Toast'
import { formatWhen } from '../format'
import { plural, useLocale, useT } from '../i18n'
import { Field } from './Login'

// Users is the admin page: invite people, send password links, change
// roles, disable accounts.
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

      {inviting && <InviteDialog onClose={() => setInviting(false)} onDone={load} />}
    </div>
  )
}

function InviteDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const t = useT()
  const locale = useLocale()
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [role, setRole] = useState('member')
  const [lang, setLang] = useState<string>(locale)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const r = await api.inviteUser({ name, email, role, locale: lang })
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
