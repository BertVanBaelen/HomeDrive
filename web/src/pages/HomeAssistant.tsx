import { useEffect, useState, type FormEvent } from 'react'
import { api, type User, type UserInfo } from '../api'
import { errorText, Modal } from '../components/Dialogs'
import { Icon, icons } from '../components/Icon'
import { Logo } from '../components/Logo'
import { showToast } from '../components/Toast'
import { useT } from '../i18n'
import { navigate } from '../router'
import { AuthLayout, CodeStep, Field } from './Login'

// Home Assistant login: an add-on option. The Supervisor checks the Home
// Assistant password, and the email code follows as for any login.

// The login options don't change while the app runs, so they are asked once.
let options: Promise<boolean> | null = null

// useHomeAssistantLogin is true when the login page offers Home Assistant.
export function useHomeAssistantLogin(): boolean {
  const [on, setOn] = useState(false)
  useEffect(() => {
    options ??= api.authOptions().then(
      (o) => o.homeAssistant,
      () => {
        options = null
        return false
      },
    )
    let live = true
    options.then((v) => live && setOn(v))
    return () => {
      live = false
    }
  }, [])
  return on
}

// HomeAssistantLogo is the Home Assistant logomark, as the Open Home
// Foundation publishes it: in colour on a light background, without a
// container, outline or shadow, with clear space around it. Home Assistant
// and its logo are trademarks of the Open Home Foundation.
export function HomeAssistantLogo({ size = 36 }: { size?: number }) {
  return (
    <svg role="img" aria-label="Home Assistant" width={size} height={size} viewBox="0 0 240 240">
      <path
        fill="#18BCF2"
        d="M120.001 0c-3.787 0-7.573 1.499-10.444 4.49L12.211 105.905a25.921 25.921 0 0 0-2.098 2.501 35.25 35.25 0 0 0-1.96 2.942c-3.01 5.021-5.285 11.318-6.074 16.898-.03.21-.088.429-.11.636a27.355 27.355 0 0 0-.213 3.317v93.023a14.78 14.78 90 0 0 14.78 14.78h90.92L67.422 198.29a20.2 20.2 90 1 1 12.542-13.06l31.17 32.474V98.726a20.2 20.2 90 1 1 17.734 0v83.44l31.001-32.299a20.2 20.2 90 1 1 12.267 13.357l-43.269 45.082V240h94.9a14.479 14.479 90 0 0 14.478-14.479v-93.314c0-1.059-.069-2.168-.214-3.314-.7-5.73-3.06-12.327-6.183-17.537a35.801 35.801 0 0 0-1.955-2.937 26.271 26.271 0 0 0-2.102-2.506L130.444 4.486C127.573 1.494 123.786-.002 120.001 0"
      />
    </svg>
  )
}

// The page's head: Home Assistant and Homedrive joined by a chain link.
function LinkedLogos() {
  const t = useT()
  return (
    <div className="ha-brand" role="img" aria-label={t('ha.logos')}>
      <HomeAssistantLogo />
      <span className="ha-brand__chain" aria-hidden="true">
        <Icon d={icons.link} size={18} />
      </span>
      <span className="ha-brand__homedrive">
        <Logo size={36} />
      </span>
    </div>
  )
}

export function HomeAssistantLogin({ onSignedIn }: { onSignedIn: (u: User) => void }) {
  const t = useT()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [sent, setSent] = useState<{ challenge: string; email: string } | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const r = await api.homeAssistantLogin(username, password)
      setPassword('')
      setSent(r)
    } catch (err) {
      setError(errorText(t, err))
    }
    setBusy(false)
  }

  if (sent) {
    return (
      <AuthLayout brand={<LinkedLogos />}>
        <CodeStep email={sent.email} submit={t('login.verify')} back={t('login.otherAccount')}
          onVerify={async (code) => onSignedIn(await api.verify(sent.challenge, code))}
          onBack={() => setSent(null)} />
      </AuthLayout>
    )
  }
  return (
    <AuthLayout brand={<LinkedLogos />} help={t('ha.help')}>
      <h1>{t('ha.title')}</h1>
      <p>{t('ha.intro')}</p>
      <form onSubmit={submit}>
        <Field label={t('ha.username')} autoComplete="username" autoCapitalize="none" spellCheck={false}
          required autoFocus maxLength={100} value={username} onChange={(e) => setUsername(e.target.value)} />
        <Field label={t('login.password')} type="password" autoComplete="current-password" required
          value={password} onChange={(e) => setPassword(e.target.value)} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <button type="submit" className="btn btn--primary btn--block" disabled={busy}>
          {t('login.submit')}
        </button>
      </form>
      <button type="button" className="link-btn" onClick={() => navigate('/login')}>
        {t('ha.withEmail')}
      </button>
    </AuthLayout>
  )
}

// HomeAssistantAccount is the Account Settings panel where users link their
// own Home Assistant user. Linking asks the Home Assistant password, to
// prove the user is theirs, and the Homedrive password if it wasn't
// entered in the last few minutes.
export function HomeAssistantAccount() {
  const t = useT()
  const on = useHomeAssistantLogin()
  const [linked, setLinked] = useState<string | null>(null)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (on) api.homeAssistantLink().then((l) => setLinked(l.username), () => {})
  }, [on])

  if (!on || linked === null) return null

  async function link(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const l = await api.linkHomeAssistant(username, password)
      setLinked(l.username)
      setUsername('')
      setPassword('')
      showToast(t('ha.linkedToast'))
    } catch (err) {
      setError(errorText(t, err))
    }
    setBusy(false)
  }

  async function unlink() {
    setBusy(true)
    try {
      await api.unlinkHomeAssistant()
      setLinked('')
      showToast(t('ha.unlinkedToast'))
    } catch (err) {
      showToast(errorText(t, err), { error: true })
    }
    setBusy(false)
  }

  return (
    <section className="panel" aria-labelledby="ha-title">
      <h2 id="ha-title">Home Assistant</h2>
      {linked ? (
        <div className="ha-linked">
          <HomeAssistantLogo size={28} />
          <p>
            {t('ha.linkedTo')} <strong>{linked}</strong>
          </p>
          <button type="button" className="btn btn--small" disabled={busy} onClick={unlink}>
            {t('ha.unlink')}
          </button>
        </div>
      ) : (
        <>
          <p className="panel__body">{t('ha.linkBody')}</p>
          <form className="form" onSubmit={link}>
            <div className="form__row">
              <Field label={t('ha.username')} autoComplete="off" autoCapitalize="none" spellCheck={false}
                required maxLength={100} value={username} onChange={(e) => setUsername(e.target.value)} />
              <Field label={t('ha.password')} type="password" autoComplete="off" required
                value={password} onChange={(e) => setPassword(e.target.value)} />
            </div>
            {error && <p className="form-error" role="alert">{error}</p>}
            <div className="form__foot">
              <button type="submit" className="btn btn--primary" disabled={busy || !username.trim() || !password}>
                <Icon d={icons.link} size={18} />
                {t('ha.link')}
              </button>
            </div>
          </form>
        </>
      )}
    </section>
  )
}

// HomeAssistantUserDialog lets an admin set or clear the Home Assistant
// username someone may log in with.
export function HomeAssistantUserDialog({ user, onClose, onDone }: { user: UserInfo; onClose: () => void; onDone: () => void }) {
  const t = useT()
  const [username, setUsername] = useState(user.haUsername ?? '')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.updateUser(user.id, { haUsername: username.trim() })
      onDone()
      onClose()
      showToast(t('users.saved'))
    } catch (err) {
      setError(errorText(t, err))
      setBusy(false)
    }
  }

  return (
    <Modal title={t('ha.adminTitle')} subtitle={t('ha.adminBody', { name: user.name })} onClose={onClose}>
      <form className="form" onSubmit={save}>
        <Field label={t('ha.username')} autoComplete="off" autoCapitalize="none" spellCheck={false} autoFocus
          maxLength={100} hint={t('ha.adminHint')} value={username} onChange={(e) => setUsername(e.target.value)} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="modal__foot">
          <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button type="submit" className="btn btn--primary" disabled={busy || username.trim() === (user.haUsername ?? '')}>
            {t('account.save')}
          </button>
        </div>
      </form>
    </Modal>
  )
}
