import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { api, linkAddress, type LinkExpiry, type Node, type Person, type ShareAccess, type Sharing, type User } from '../api'
import { displayName, formatDateTime } from '../format'
import { plural, useLocale, useT, type MessageKey } from '../i18n'
import { errorText, Modal } from './Dialogs'
import { Icon, icons } from './Icon'
import { Select } from './Select'
import { initials } from './Shell'

const accesses: ShareAccess[] = ['view', 'edit']
const expiries: LinkExpiry[] = ['never', '7d', '1d']
const minPassword = 8

// ShareDialog shares an item with people who have an account here, and by
// a link that works without one. Changes are saved straight away.
export function ShareDialog({ node, me, onClose }: { node: Node; me: User; onClose: () => void }) {
  const t = useT()
  const locale = useLocale()
  const addId = useId()
  const linkId = useId()
  const [sharing, setSharing] = useState<Sharing | null>(null)
  const [people, setPeople] = useState<Person[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(
    () => api.sharing(node.id).then(setSharing, (err) => setError(errorText(t, err))),
    [node.id, t],
  )
  useEffect(() => {
    load()
    api.people().then(setPeople, () => {})
  }, [load])

  async function change(key: string, run: () => Promise<unknown>): Promise<boolean> {
    setBusy(key)
    setError('')
    let ok = true
    try {
      await run()
      await load()
    } catch (err) {
      setError(errorText(t, err))
      ok = false
    }
    setBusy(null)
    return ok
  }

  const accessOptions = accesses.map((a) => ({ value: a, label: t(`share.access.${a}` as MessageKey) }))
  const listed = new Set([
    me.id,
    ...(sharing?.editors ?? []),
    ...(sharing?.people ?? []).map((p) => p.userId),
    ...(sharing?.inherited ?? []).map((p) => p.userId),
  ])
  const candidates = people.filter((p) => !listed.has(p.id))
  const drive = sharing?.drive
  const link = sharing?.link ?? null

  return (
    <Modal title={t('share.title')} subtitle={displayName(node)} onClose={onClose}>
      {sharing && drive && (
        <section className="share-people" aria-label={t('share.people')}>
          <h3>{t('share.people')}</h3>
          <ul className="members">
            {drive.kind === 'personal' ? (
              <li className="member">
                <span className="member__avatar" aria-hidden="true">{initials(me.name)}</span>
                <span className="member__text">
                  <strong>
                    {me.name}
                    <span className="member__you"> ({t('users.you')})</span>
                  </strong>
                  <span>{me.email}</span>
                </span>
                <span className="member__role">{t('share.owner')}</span>
              </li>
            ) : (
              <li className="member">
                <span className="member__avatar member__avatar--drive" aria-hidden="true">
                  <Icon d={icons.family} size={18} />
                </span>
                <span className="member__text">
                  <strong>{t('share.membersOf', { name: drive.name })}</strong>
                  <span>{plural(t, 'count.members', sharing.members)}</span>
                </span>
              </li>
            )}
            {sharing.people.map((p) => (
              <li key={p.userId} className={'member' + (p.disabled ? ' is-disabled' : '')}>
                <span className="member__avatar" aria-hidden="true">{initials(p.name)}</span>
                <span className="member__text">
                  <strong>{p.name}</strong>
                  <span>{p.email}</span>
                </span>
                {p.disabled && <span className="badge badge--disabled">{t('status.disabled')}</span>}
                <span className="member__actions">
                  <Select size="small" value={p.access} disabled={busy === p.userId}
                    ariaLabel={t('share.accessFor', { name: p.name })} title={t('share.accessFor', { name: p.name })}
                    options={accessOptions}
                    onChange={(a) => change(p.userId, () => api.setShare(node.id, p.userId, a))} />
                  <button type="button" className="icon-btn" aria-label={t('share.remove', { name: p.name })}
                    disabled={busy === p.userId} onClick={() => change(p.userId, () => api.removeShare(node.id, p.userId))}>
                    <Icon d={icons.close} size={18} />
                  </button>
                </span>
              </li>
            ))}
            {sharing.inherited.map((p) => (
              <li key={`${p.from?.id}-${p.userId}`} className={'member' + (p.disabled ? ' is-disabled' : '')}>
                <span className="member__avatar" aria-hidden="true">{initials(p.name)}</span>
                <span className="member__text">
                  <strong>{p.name}</strong>
                  <span>{t('share.from', { name: p.from?.name ?? '' })}</span>
                </span>
                <span className="member__role">{t(`share.access.${p.access}` as MessageKey)}</span>
              </li>
            ))}
          </ul>
          {candidates.length > 0 && (
            <div className="add-people" role="group" aria-labelledby={addId}>
              <h3 id={addId}>{t('members.add')}</h3>
              <div className="chips">
                {candidates.map((p) => (
                  <button key={p.id} type="button" className="chip" title={p.email} disabled={busy === p.id}
                    aria-label={t('members.addPerson', { name: p.name, email: p.email })}
                    onClick={() => change(p.id, () => api.setShare(node.id, p.id, 'view'))}>
                    <span className="chip__avatar" aria-hidden="true">{initials(p.name)}</span>
                    <span className="chip__name">{p.name}</span>
                    <Icon d={icons.plus} size={16} width={2.5} className="chip__plus" />
                  </button>
                ))}
              </div>
              <p className="add-people__hint">{t('share.addHint')}</p>
            </div>
          )}
        </section>
      )}

      {sharing && (
        <section className="share-link" aria-labelledby={linkId}>
          <div className="share-link__head">
            <span className="share-link__icon" aria-hidden="true">
              <Icon d={icons.link} size={20} />
            </span>
            <span className="share-link__text">
              <strong id={linkId}>{t('share.link')}</strong>
              <span>{t(link ? 'share.linkOn' : 'share.linkOff')}</span>
            </span>
            <button type="button" role="switch" className="switch" aria-checked={!!link} aria-labelledby={linkId}
              disabled={busy === 'link'}
              onClick={() => change('link', () => (link ? api.removeLink(node.id) : api.setLink(node.id, {})))}>
              <span className="switch__knob" />
            </button>
          </div>
          {link && (
            <div className="share-link__body">
              <LinkAddress token={link.token} />
              <div className="share-link__field">
                <span className="share-link__label" id={`${linkId}-exp`}>{t('share.expires')}</span>
                <div role="group" aria-labelledby={`${linkId}-exp`} className="toggle toggle--fill">
                  {expiries.map((e) => (
                    <button key={e} type="button" aria-pressed={link.expiry === e} disabled={busy === 'link'}
                      onClick={() => change('link', () => api.setLink(node.id, { expiry: e }))}>
                      {t(`share.expiry.${e}` as MessageKey)}
                    </button>
                  ))}
                </div>
                {link.expired ? (
                  <span className="share-link__note">
                    <span className="badge badge--disabled">{t('share.expired')}</span>
                    {t('share.expiredHint')}
                  </span>
                ) : link.expiresAt ? (
                  <span className="share-link__note">{t('share.expiresAt', { date: formatDateTime(link.expiresAt, locale) })}</span>
                ) : null}
              </div>
              <LinkPassword set={link.password} busy={busy === 'password'}
                onSave={(password) => change('password', () => api.setLink(node.id, { password }))} />
            </div>
          )}
        </section>
      )}

      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="modal__foot">
        <button type="button" className="btn btn--primary" onClick={onClose}>{t('common.done')}</button>
      </div>
    </Modal>
  )
}

// LinkAddress shows the link with a button to copy it. Browsers only allow
// the clipboard API on HTTPS, so on plain HTTP the text is selected and
// copied the old way.
function LinkAddress({ token }: { token: string }) {
  const t = useT()
  const id = useId()
  const input = useRef<HTMLInputElement>(null)
  const [copied, setCopied] = useState(false)
  const address = linkAddress(token)

  async function copy() {
    let ok = false
    try {
      await navigator.clipboard.writeText(address)
      ok = true
    } catch {
      input.current?.select()
      ok = document.execCommand?.('copy') ?? false
    }
    if (!ok) return
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="share-link__address">
      <label htmlFor={id} className="sr-only">{t('share.address')}</label>
      <input id={id} ref={input} readOnly value={address} onFocus={(e) => e.target.select()} />
      <button type="button" className="btn btn--primary" onClick={copy}>
        <Icon d={copied ? icons.check : icons.copy} size={16} />
        <span aria-live="polite">{t(copied ? 'share.copied' : 'share.copy')}</span>
      </button>
    </div>
  )
}

// LinkPassword asks visitors for a password before they see anything.
function LinkPassword({ set, busy, onSave }: { set: boolean; busy: boolean; onSave: (password: string) => Promise<boolean> }) {
  const t = useT()
  const id = useId()
  const hintId = useId()
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState('')
  const [error, setError] = useState('')

  async function save(password: string) {
    if (password && password.length < minPassword) {
      setError(t('error.link_password'))
      return
    }
    setError('')
    if (!(await onSave(password))) return
    setEditing(false)
    setValue('')
  }
  function cancel() {
    setEditing(false)
    setValue('')
    setError('')
  }

  if (editing) {
    return (
      <form className="share-link__field" onSubmit={(e) => {
        e.preventDefault()
        save(value)
      }}>
        <label htmlFor={id} className="share-link__label">{t('share.password')}</label>
        <input id={id} className="share-link__input" type="text" autoComplete="off" spellCheck={false} value={value}
          autoFocus maxLength={256} placeholder={t('share.passwordPlaceholder')} aria-invalid={!!error}
          aria-describedby={hintId} onChange={(e) => setValue(e.target.value)} />
        {error ? (
          <span className="share-link__note share-link__note--error" role="alert" id={hintId}>{error}</span>
        ) : (
          <span className="share-link__note" id={hintId}>{t('share.passwordHint')}</span>
        )}
        <div className="share-link__buttons">
          <button type="button" className="btn btn--small" onClick={cancel}>{t('common.cancel')}</button>
          <button type="submit" className="btn btn--small btn--primary" disabled={busy || !value}>
            {t('share.passwordSave')}
          </button>
        </div>
      </form>
    )
  }
  return (
    <div className="share-link__password">
      <Icon d={icons.key} size={18} />
      <span>{t(set ? 'share.passwordOn' : 'share.passwordOff')}</span>
      {set ? (
        <>
          <button type="button" className="btn btn--small" disabled={busy} onClick={() => setEditing(true)}>
            {t('share.passwordChange')}
          </button>
          <button type="button" className="btn btn--small btn--danger" disabled={busy} onClick={() => save('')}>
            {t('share.passwordRemove')}
          </button>
        </>
      ) : (
        <button type="button" className="btn btn--small" disabled={busy} onClick={() => setEditing(true)}>
          {t('share.passwordAdd')}
        </button>
      )}
    </div>
  )
}
