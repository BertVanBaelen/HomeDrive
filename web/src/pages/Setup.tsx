import { useEffect, useState, type FormEvent } from 'react'
import { api, type User } from '../api'
import { errorText } from '../components/Dialogs'
import { useLocale, useT } from '../i18n'
import { AuthLayout, CodeStep, Field } from './Login'

// Setup creates the first admin from the one-time link in the server log:
// https://…/setup#<token>. With mode "recover" the same page serves the
// admin_recovery link (…/recover#<token>): a locked-out admin sets a new
// password. The token stays in the fragment so it never reaches a proxy;
// the page sends it in request bodies only.
export function Setup({ mode = 'setup', onSignedIn }: { mode?: 'setup' | 'recover'; onSignedIn: (u: User) => void }) {
  const t = useT()
  const locale = useLocale()
  const recover = mode === 'recover'
  const [token] = useState(() => location.hash.slice(1))
  const [valid, setValid] = useState<boolean | null>(token ? null : false)
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [codeSent, setCodeSent] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    // Keep the token out of the address bar and history from now on.
    if (location.hash) history.replaceState(null, '', location.pathname)
    if (!token) return
    ;(recover ? api.recoverCheck(token) : api.setupCheck(token)).then(
      () => setValid(true),
      (err) => {
        setValid(false)
        setError(errorText(t, err))
      },
    )
  }, [token, t, recover])

  async function begin(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      if (recover) await api.recoverBegin({ token, email, password })
      else await api.setupBegin({ token, email, name, password, locale })
      setCodeSent(true)
    } catch (err) {
      setError(errorText(t, err))
    }
    setBusy(false)
  }

  if (codeSent) {
    return (
      <AuthLayout>
        <CodeStep email={email} submit={t(recover ? 'recover.finish' : 'setup.finish')} back={t('common.back')}
          onVerify={async (code) => {
            const u = await (recover ? api.recoverFinish(token, code) : api.setupFinish(token, code))
            setPassword('')
            onSignedIn(u)
          }}
          onBack={() => setCodeSent(false)} />
      </AuthLayout>
    )
  }
  return (
    <AuthLayout>
      <h1>{t(recover ? 'recover.title' : 'setup.title')}</h1>
      {valid === false ? (
        <p className="form-error" role="alert">{error || t(recover ? 'recover.noToken' : 'setup.noToken')}</p>
      ) : (
        <>
          <p>{t(recover ? 'recover.intro' : 'setup.intro')}</p>
          <form onSubmit={begin}>
            {!recover && (
              <Field label={t('setup.name')} autoComplete="name" required autoFocus maxLength={80}
                value={name} onChange={(e) => setName(e.target.value)} />
            )}
            <Field label={t('login.email')} type="email" autoComplete={recover ? 'username' : 'email'} required
              autoFocus={recover} value={email} onChange={(e) => setEmail(e.target.value)} />
            <Field label={t(recover ? 'account.new' : 'login.password')} type="password" autoComplete="new-password"
              required minLength={12} hint={t('setup.passwordHint')} value={password}
              onChange={(e) => setPassword(e.target.value)} />
            {error && <p className="form-error" role="alert">{error}</p>}
            <button type="submit" className="btn btn--primary btn--block" disabled={busy || valid === null}>
              {t('setup.submit')}
            </button>
          </form>
        </>
      )}
    </AuthLayout>
  )
}
