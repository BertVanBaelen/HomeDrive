import { useEffect, useState, type FormEvent } from 'react'
import { api, type PasswordLink, type User } from '../api'
import { errorText } from '../components/Dialogs'
import { useT } from '../i18n'
import { AuthLayout, Field } from './Login'

// Welcome is where an emailed link lands: /welcome#<token>. New people
// choose their first password; after a reset, a new one. Opening the link
// proves the address, so they're signed in right after.
export function Welcome({ onSignedIn }: { onSignedIn: (u: User) => void }) {
  const t = useT()
  const [token] = useState(() => location.hash.slice(1))
  const [link, setLink] = useState<PasswordLink | null>(null)
  const [invalid, setInvalid] = useState(!token)
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    // Keep the token out of the address bar and history from now on.
    if (location.hash) history.replaceState(null, '', location.pathname)
    if (token) api.welcomeCheck(token).then(setLink, () => setInvalid(true))
  }, [token])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const u = await api.welcomeFinish(token, password)
      setPassword('')
      onSignedIn(u)
    } catch (err) {
      setError(errorText(t, err))
      setBusy(false)
    }
  }

  if (invalid) {
    return (
      <AuthLayout>
        <h1>{t('setup.title')}</h1>
        <p className="form-error" role="alert">{t('welcome.invalid')}</p>
      </AuthLayout>
    )
  }
  if (!link) return <AuthLayout>{t('loading')}</AuthLayout>
  const reset = link.purpose === 'reset'
  return (
    <AuthLayout>
      <h1>{reset ? t('welcome.resetTitle') : t('welcome.title', { name: link.name })}</h1>
      <p>{t(reset ? 'welcome.resetIntro' : 'welcome.intro', { email: link.email })}</p>
      <form onSubmit={submit}>
        <input type="email" autoComplete="username" value={link.email} hidden readOnly />
        <Field label={t('account.new')} type="password" autoComplete="new-password" required minLength={12} autoFocus
          hint={t('setup.passwordHint')} value={password} onChange={(e) => setPassword(e.target.value)} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <button type="submit" className="btn btn--primary btn--block" disabled={busy}>
          {t('welcome.submit')}
        </button>
      </form>
    </AuthLayout>
  )
}
