import { useEffect, useState } from 'react'
import { api, ApiError, setReauthHandler } from '../api'
import { useT } from '../i18n'
import { errorText, Modal } from './Dialogs'
import { Field } from '../pages/Login'

// ReauthDialog asks for the password when the server wants it confirmed
// before a sensitive action; the request is retried once it is. Someone
// without a Homedrive password (haOnly) confirms with their Home Assistant
// password; the server checks it for their own linked user.
export function ReauthDialog({ haOnly = false }: { haOnly?: boolean }) {
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
      // A wrong Home Assistant password: there is no username to get wrong.
      const wrong = err instanceof ApiError && err.code === 'ha_invalid_credentials'
      setError(wrong ? t('error.wrong_password') : errorText(t, err))
      setBusy(false)
    }
  }

  return (
    <Modal title={t('reauth.title')} subtitle={t(haOnly ? 'reauth.haBody' : 'reauth.body')} onClose={() => finish(false)}>
      <form className="form" onSubmit={submit}>
        <Field label={t(haOnly ? 'ha.password' : 'login.password')} type="password" autoComplete="current-password" required
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
