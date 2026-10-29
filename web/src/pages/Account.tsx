import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { api, type SessionInfo, type User } from '../api'
import { errorText } from '../components/Dialogs'
import { Icon, icons } from '../components/Icon'
import { SelectField } from '../components/Select'
import { showToast } from '../components/Toast'
import { formatWhen } from '../format'
import { detectLocale, translator, useLocale, useT, type Translate } from '../i18n'
import { Field } from './Login'
import { ShortcutDevices } from './Shortcut'

// deviceName turns a user agent into "Chrome on Windows".
export function deviceName(ua: string, t: Translate): string {
  const os = /Android/.test(ua)
    ? 'Android'
    : /iPhone|iPad|iPod/.test(ua)
      ? 'iOS'
      : /Windows/.test(ua)
        ? 'Windows'
        : /Mac OS X|Macintosh/.test(ua)
          ? 'macOS'
          : /Linux/.test(ua)
            ? 'Linux'
            : ''
  const browser = /Edg\//.test(ua)
    ? 'Edge'
    : /Firefox\//.test(ua)
      ? 'Firefox'
      : /Chrome\//.test(ua)
        ? 'Chrome'
        : /Safari\//.test(ua)
          ? 'Safari'
          : ''
  if (!browser && !os) return t('account.unknownDevice')
  return [browser, os].filter(Boolean).join(' · ')
}

export function Account({ user, onUser }: { user: User; onUser: (u: User) => void }) {
  const t = useT()
  return (
    <div className="settings">
      <h1>{t('account.title')}</h1>
      <Profile user={user} onUser={onUser} />
      <Password />
      <Devices />
      <ShortcutDevices />
    </div>
  )
}

function Profile({ user, onUser }: { user: User; onUser: (u: User) => void }) {
  const t = useT()
  const [name, setName] = useState(user.name)
  const [locale, setLocale] = useState(user.locale)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const u = await api.updateMe({ name, locale })
      onUser(u)
      // Say it in the language just chosen.
      showToast(translator(u.locale === 'nl' || u.locale === 'en' ? u.locale : detectLocale())('account.saved'))
    } catch (err) {
      setError(errorText(t, err))
    }
    setBusy(false)
  }

  return (
    <section className="panel" aria-labelledby="profile-title">
      <h2 id="profile-title">{t('account.profile')}</h2>
      <form className="form" onSubmit={save}>
        <Field label={t('account.name')} value={name} required maxLength={80} autoComplete="name"
          onChange={(e) => setName(e.target.value)} />
        <SelectField label={t('account.language')} value={locale} onChange={setLocale}
          options={[
            { value: '', label: t('account.langAuto') },
            { value: 'en', label: 'English' },
            { value: 'nl', label: 'Nederlands' },
          ]} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="form__foot">
          <button type="submit" className="btn btn--primary" disabled={busy || (name === user.name && locale === user.locale)}>
            {t('account.save')}
          </button>
        </div>
      </form>
    </section>
  )
}

function Password() {
  const t = useT()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.changePassword(current, next)
      setCurrent('')
      setNext('')
      showToast(t('account.changed'))
    } catch (err) {
      setError(errorText(t, err))
    }
    setBusy(false)
  }

  return (
    <section className="panel" aria-labelledby="password-title">
      <h2 id="password-title">{t('account.password')}</h2>
      <form className="form" onSubmit={save}>
        {/* Lets password managers attach the new password to the account. */}
        <input type="email" autoComplete="username" hidden readOnly />
        <Field label={t('account.current')} type="password" autoComplete="current-password" required
          value={current} onChange={(e) => setCurrent(e.target.value)} />
        <Field label={t('account.new')} type="password" autoComplete="new-password" required minLength={12}
          hint={t('setup.passwordHint')} value={next} onChange={(e) => setNext(e.target.value)} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="form__foot">
          <button type="submit" className="btn btn--primary" disabled={busy || !current || !next}>
            {t('account.change')}
          </button>
        </div>
      </form>
    </section>
  )
}

function Devices() {
  const t = useT()
  const locale = useLocale()
  const [sessions, setSessions] = useState<SessionInfo[] | null>(null)

  const load = useCallback(() => {
    api.sessions().then(setSessions, (err) => showToast(errorText(t, err), { error: true }))
  }, [t])
  useEffect(load, [load])

  async function revoke(id: string) {
    try {
      await api.revokeSession(id)
      load()
      showToast(t('account.deviceSignedOut'))
    } catch (err) {
      showToast(errorText(t, err), { error: true })
    }
  }

  return (
    <section className="panel" aria-labelledby="devices-title">
      <h2 id="devices-title">{t('account.devices')}</h2>
      <p className="panel__body">{t('account.devicesBody')}</p>
      <ul className="devices">
        {sessions?.map((s) => (
          <li key={s.id}>
            <span className="devices__icon">
              <Icon d={/Android|iPhone|iPad|Mobile/.test(s.userAgent) ? icons.phone : icons.desktop} />
            </span>
            <div className="devices__text">
              <strong>
                {deviceName(s.userAgent, t)}
                {s.current && <span className="badge badge--green">{t('account.thisDevice')}</span>}
              </strong>
              <span>
                {t('account.lastActive', { when: formatWhen(s.lastSeenAt, t, locale) })} · {s.ip}
              </span>
            </div>
            {!s.current && (
              <button type="button" className="btn btn--small" onClick={() => revoke(s.id)}>
                {t('account.signOutDevice')}
              </button>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}
