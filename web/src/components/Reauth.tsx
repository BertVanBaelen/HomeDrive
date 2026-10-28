import { useEffect, useState } from 'react'
import { api, setReauthHandler } from '../api'
import { useT } from '../i18n'
import { errorText, Modal } from './Dialogs'
import { Field } from '../pages/Login'

// ReauthDialog asks for the password when the server wants it confirmed
// before a sensitive action; the request is retried once it is.
export function ReauthDialog() {
  const t = useT()
  const [pending, setPending] = useState<((ok: boolean) => void) | null>(null)
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setReauthHandler(
      () =>
        new Promise<boolean>((resolve) => {
          setPassword('')
          setError('')
          setBusy(false)
          setPending(() => resolve)
        }),
    )
    return () => setReauthHandler(async () => false)
  }, [])

  if (!pending) return null
  const finish = (ok: boolean) => {
    pending(ok)
    setPending(null)
    setPassword('')
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.reauth(password)
      finish(true)
    } catch (err) {
      setError(errorText(t, err))
      setBusy(false)
    }
  }

  return (
    <Modal title={t('reauth.title')} subtitle={t('reauth.body')} onClose={() => finish(false)}>
      <form className="form" onSubmit={submit}>
        <Field label={t('login.password')} type="password" autoComplete="current-password" required
          value={password} onChange={(e) => setPassword(e.target.value)} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="modal__foot">
          <button type="button" className="btn" onClick={() => finish(false)}>{t('common.cancel')}</button>
          <button type="submit" className="btn btn--primary" disabled={busy || !password}>{t('reauth.submit')}</button>
        </div>
      </form>
    </Modal>
  )
}
