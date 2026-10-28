import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { api, type User } from '../api'
import { errorText } from '../components/Dialogs'
import { Icon, icons } from '../components/Icon'
import { Logo } from '../components/Logo'
import { useT } from '../i18n'

// AuthLayout centres a card under the logo, for sign-in and setup.
export function AuthLayout({ children, help }: { children: ReactNode; help?: string }) {
  const t = useT()
  return (
    <main className="auth">
      <div className="auth__brand">
        <Logo size={36} label={t('app.name')} />
        <span>{t('app.name')}</span>
      </div>
      <div className="auth__card">{children}</div>
      {help && <p className="auth__help">{help}</p>}
    </main>
  )
}

export function Field({
  label,
  hint,
  ...input
}: React.InputHTMLAttributes<HTMLInputElement> & { label: string; hint?: string }) {
  const id = useId()
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <input id={id} {...input} />
      {hint && <span className="field__hint">{hint}</span>}
    </div>
  )
}

// CodeStep asks for the 8-digit code that was emailed.
export function CodeStep({
  email,
  submit,
  back,
  onVerify,
  onBack,
}: {
  email: string
  submit: string
  back: string
  onVerify: (code: string) => Promise<void>
  onBack: () => void
}) {
  const t = useT()
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function verify(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await onVerify(code.replace(/\D/g, ''))
    } catch (err) {
      setError(errorText(t, err))
      setBusy(false)
    }
  }

  return (
    <>
      <span className="auth__mail">
        <Icon d={icons.mail} size={24} />
      </span>
      <h1>{t('login.codeTitle')}</h1>
      <p>{t('login.codeBody', { email })}</p>
      <form onSubmit={verify}>
        <Field label={t('login.code')} className="code-input" value={code} autoFocus required
          inputMode="numeric" autoComplete="one-time-code" maxLength={9} pattern="\d{4} ?\d{4}"
          onChange={(e) => setCode(e.target.value)} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <button type="submit" className="btn btn--primary btn--block" disabled={busy || code.replace(/\D/g, '').length !== 8}>
          {submit}
        </button>
      </form>
      <button type="button" className="link-btn" onClick={onBack}>
        {back}
      </button>
    </>
  )
}

export function Login({ onSignedIn }: { onSignedIn: (u: User) => void }) {
  const t = useT()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [challenge, setChallenge] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const r = await api.login(email, password)
      setPassword('')
      setChallenge(r.challenge)
    } catch (err) {
      setError(errorText(t, err))
    }
    setBusy(false)
  }

  if (challenge) {
    return (
      <AuthLayout>
        <CodeStep email={email} submit={t('login.verify')} back={t('login.otherAccount')}
          onVerify={async (code) => onSignedIn(await api.verify(challenge, code))}
          onBack={() => setChallenge('')} />
      </AuthLayout>
    )
  }
  return (
    <AuthLayout help={t('login.help')}>
      <h1>{t('login.title')}</h1>
      <p>{t('login.intro')}</p>
      <form onSubmit={submit}>
        <Field label={t('login.email')} type="email" autoComplete="username" required autoFocus
          value={email} onChange={(e) => setEmail(e.target.value)} />
        <Field label={t('login.password')} type="password" autoComplete="current-password" required
          value={password} onChange={(e) => setPassword(e.target.value)} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <button type="submit" className="btn btn--primary btn--block" disabled={busy}>
          {t('login.submit')}
        </button>
      </form>
    </AuthLayout>
  )
}
