import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { api, type DeviceToken } from '../api'
import { errorText, Modal } from '../components/Dialogs'
import { Icon, icons } from '../components/Icon'
import { showToast } from '../components/Toast'
import { formatWhen } from '../format'
import { useLocale, useT } from '../i18n'
import { Field } from './Login'

// ShortcutDevices is the account section for iPhones and iPads: iOS has
// no share target for web apps, so an Apple Shortcut uploads with a
// per-device key that can only add files to the Inbox.
export function ShortcutDevices() {
  const t = useT()
  const locale = useLocale()
  const [devices, setDevices] = useState<DeviceToken[] | null>(null)
  const [adding, setAdding] = useState(false)
  const [created, setCreated] = useState<{ name: string; token: string } | null>(null)

  const load = useCallback(() => {
    api.deviceTokens().then(setDevices, (err) => showToast(errorText(t, err), { error: true }))
  }, [t])
  useEffect(load, [load])

  async function remove(d: DeviceToken) {
    try {
      await api.revokeDeviceToken(d.id)
      load()
      showToast(t('shortcut.removed'))
    } catch (err) {
      showToast(errorText(t, err), { error: true })
    }
  }

  return (
    <section className="panel" aria-labelledby="shortcut-title">
      <h2 id="shortcut-title">{t('shortcut.title')}</h2>
      <p className="panel__body">{t('shortcut.body')}</p>
      {devices?.length === 0 && <p className="panel__body">{t('shortcut.none')}</p>}
      {!!devices?.length && (
        <ul className="devices">
          {devices.map((d) => (
            <li key={d.id}>
              <span className="devices__icon">
                <Icon d={icons.phone} />
              </span>
              <div className="devices__text">
                <strong>{d.name}</strong>
                <span>
                  {d.lastUsedAt ? t('shortcut.lastUsed', { when: formatWhen(d.lastUsedAt, t, locale) }) : t('shortcut.neverUsed')}
                </span>
              </div>
              <button type="button" className="btn btn--small btn--danger" onClick={() => remove(d)}>
                {t('shortcut.remove')}
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="form__foot">
        <button type="button" className="btn" onClick={() => setAdding(true)}>
          <Icon d={icons.phone} size={18} />
          {t('shortcut.add')}
        </button>
      </div>
      {adding && (
        <NewDevice onClose={() => setAdding(false)}
          onCreated={(name, token) => {
            setAdding(false)
            setCreated({ name, token })
            load()
          }} />
      )}
      {created && <Instructions token={created.token} onClose={() => setCreated(null)} />}
    </section>
  )
}

function NewDevice({ onClose, onCreated }: { onClose: () => void; onCreated: (name: string, token: string) => void }) {
  const t = useT()
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const r = await api.createDeviceToken(name || t('shortcut.namePlaceholder'))
      onCreated(r.device.name, r.token)
    } catch (err) {
      setError(errorText(t, err))
      setBusy(false)
    }
  }

  return (
    <Modal title={t('shortcut.add')} onClose={onClose}>
      <form className="form" onSubmit={submit}>
        <Field label={t('shortcut.name')} placeholder={t('shortcut.namePlaceholder')} maxLength={60}
          value={name} onChange={(e) => setName(e.target.value)} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="modal__foot">
          <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button type="submit" className="btn btn--primary" disabled={busy}>{t('shortcut.create')}</button>
        </div>
      </form>
    </Modal>
  )
}

function Copyable({ label, value }: { label: string; value: string }) {
  const t = useT()
  const [copied, setCopied] = useState(false)
  return (
    <div className="copyable">
      <span className="copyable__label">{label}</span>
      <div className="copyable__row">
        <code>{value}</code>
        <button type="button" className="btn btn--small"
          onClick={() =>
            navigator.clipboard.writeText(value).then(() => {
              setCopied(true)
              setTimeout(() => setCopied(false), 2000)
            })
          }>
          <Icon d={copied ? icons.check : icons.copy} size={16} />
          {copied ? t('shortcut.copied') : t('shortcut.copy')}
        </button>
      </div>
    </div>
  )
}

// Instructions walks through building the Shortcut, with the address and
// the key to copy. The key is shown only here.
function Instructions({ token, onClose }: { token: string; onClose: () => void }) {
  const t = useT()
  return (
    <Modal title={t('shortcut.setupTitle')} subtitle={t('shortcut.keyOnce')} onClose={onClose}>
      <ol className="steps">
        <li>{t('shortcut.step1')}</li>
        <li>{t('shortcut.step2')}</li>
        <li>{t('shortcut.step3')}</li>
        <li>
          {t('shortcut.step4')}
          <Copyable label="URL" value={`${location.origin}/api/inbox?name=`} />
        </li>
        <li>
          {t('shortcut.step5')}
          <Copyable label={t('shortcut.header')} value={`Bearer ${token}`} />
        </li>
        <li>{t('shortcut.step6')}</li>
      </ol>
      <div className="modal__foot">
        <button type="button" className="btn btn--primary" onClick={onClose}>{t('shortcut.done')}</button>
      </div>
    </Modal>
  )
}
