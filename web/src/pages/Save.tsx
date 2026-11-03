import { useEffect, useId, useState } from 'react'
import { api, type Drive, type LinkPreview } from '../api'
import { FolderPicker } from '../components/Dialogs'
import { fileTypes, Icon, icons } from '../components/Icon'
import { driveLabel, hrefFor } from '../components/Items'
import { linkByline } from '../components/LinkCard'
import { Link } from '../components/Shell'
import { showToast } from '../components/Toast'
import { UploadPanel } from '../components/UploadPanel'
import { formatSize } from '../format'
import { useLocale, useT } from '../i18n'
import { deleteShare, listShares, type PendingShare } from '../shared/shareStore'
import { addUploads, typeFromName } from '../uploads'

interface Target {
  id: string
  name: string
  path: string
}

const lastFolderKey = 'hd.saveFolder'

function lastFolder(): Target | null {
  try {
    return JSON.parse(localStorage.getItem(lastFolderKey) ?? 'null')
  } catch {
    return null
  }
}

function rememberFolder(t: Target) {
  try {
    localStorage.setItem(lastFolderKey, JSON.stringify(t))
  } catch {
    // ignore
  }
}

// The server's rules for a saved link's address (server/internal/savedlink).
const maxUrl = 2048

function parseLink(raw: string): URL | null {
  raw = raw.trim()
  if (!raw || raw.length > maxUrl) return null
  try {
    const u = new URL(raw)
    if ((u.protocol !== 'http:' && u.protocol !== 'https:') || !u.hostname || u.username || u.password) return null
    return u
  } catch {
    return null
  }
}

interface SharedLink {
  url: string
  host: string
  // text is what was shared besides the address.
  text: string
}

// sharedLink is the link a share without files holds: its url field, or
// the first address in its text, the way most Android apps share links.
function sharedLink(share: PendingShare): SharedLink | null {
  if (share.files.length) return null
  const text = share.text.trim()
  let u = parseLink(share.url)
  let rest = text
  if (!u) {
    for (const word of text.split(/\s+/)) {
      const at = word.search(/https?:\/\//i)
      if (at < 0) continue
      const candidate = word.slice(at).replace(/[.,;:!?)\]}>"']+$/, '')
      u = parseLink(candidate)
      if (u) {
        rest = text.replace(candidate, '').replace(/ {2,}/g, ' ').trim()
        break
      }
    }
  } else if (rest === share.url.trim()) {
    rest = ''
  }
  if (!u) return null
  if (rest === share.title.trim()) rest = ''
  return { url: u.href, host: u.hostname.replace(/^www\./, ''), text: rest }
}

// linkFileName mirrors the server's name for a link: no slashes or control
// characters, at most 255 bytes with .url.
function linkFileName(title: string): string {
  let name = title
    .replace(/[/\\]/g, '-')
    .replace(/[\u0000-\u001f\u007f]/g, ' ')
    .split(/\s+/)
    .filter(Boolean)
    .join(' ')
  const enc = new TextEncoder()
  let chars = Array.from(name)
  while (enc.encode(chars.join('')).length > 255 - 4) chars = chars.slice(0, -1)
  name = chars.join('').replace(/[ .]+$/, '')
  return (name || 'Link') + '.url'
}

// shortcutFile is a saved link as Windows writes it.
function shortcutFile(link: SharedLink, title: string): File {
  return new File([`[InternetShortcut]\r\nURL=${link.url}\r\n`], linkFileName(title), { type: 'application/x-mswinurl' })
}

// filesOf is what a share with files uploads: its files, or its text as a
// .txt when only text was shared. A shared link isn't a file yet.
function filesOf(share: PendingShare, textName: string): File[] {
  if (share.files.length) return share.files
  if (sharedLink(share)) return []
  const text = [share.title, share.text, share.url].filter(Boolean).join('\n')
  return text ? [new File([text + '\n'], `${textName}.txt`, { type: 'text/plain' })] : []
}

// sharedText is the text shared together with files: it becomes their
// description instead of being dropped.
function sharedText(share: PendingShare): string {
  if (!share.files.length) return ''
  const text = share.text.trim()
  const url = share.url.trim()
  return [share.title.trim(), text, url && !text.includes(url) ? url : ''].filter(Boolean).join('\n')
}

// Save is where things shared from another app land (Android share sheet,
// through the service worker). The user picks a folder (the last one is
// preselected), may rename and describe them, and uploads. A link becomes
// a saved link, named after its title. Everything stays on the device
// until it is uploaded.
export function Save() {
  const t = useT()
  const locale = useLocale()
  const status = new URLSearchParams(location.search).get('status')
  const [shares, setShares] = useState<PendingShare[] | null>(null)
  const [names, setNames] = useState<Record<string, string[]>>({})
  const [notes, setNotes] = useState<Record<string, string>>({})
  const [titles, setTitles] = useState<Record<string, string>>({})
  const [previews, setPreviews] = useState<Record<string, LinkPreview>>({})
  const [drives, setDrives] = useState<Drive[]>([])
  const [target, setTarget] = useState<Target | null>(null)
  const [picking, setPicking] = useState(false)
  const [saving, setSaving] = useState<string | null>(null)
  const [savedTo, setSavedTo] = useState<Target | null>(null)

  useEffect(() => {
    listShares()
      .then((list) => {
        setShares(list)
        setNames(Object.fromEntries(list.map((s) => [s.id, filesOf(s, t('save.textFile')).map((f) => f.name)])))
        setNotes(Object.fromEntries(list.map((s) => [s.id, sharedLink(s)?.text ?? sharedText(s)])))
        setTitles(Object.fromEntries(list.map((s) => [s.id, s.title.trim()])))
        // The provider's title and author, for YouTube and TikTok; other
        // sites are named after their host. Saving never waits for this.
        for (const s of list) {
          const link = sharedLink(s)
          if (!link) continue
          api.linkPreview(link.url).then(
            (p) => {
              setPreviews((all) => ({ ...all, [s.id]: p }))
              setTitles((all) => (all[s.id] ? all : { ...all, [s.id]: p.title || link.host }))
            },
            () => setTitles((all) => (all[s.id] ? all : { ...all, [s.id]: link.host })),
          )
        }
      })
      .catch(() => setShares([]))
  }, [t])

  // Preselect the last folder used, if it still exists; else My Files.
  useEffect(() => {
    api.drives().then(async (ds) => {
      setDrives(ds)
      const last = lastFolder()
      if (last) {
        try {
          const d = await api.node(last.id)
          if (d.drive.role === 'viewer') throw new Error('read only')
          const path = [driveLabel(t, d.drive), ...d.path.slice(1).map((c) => c.name)]
          if (d.node.id !== d.drive.rootId) path.push(d.node.name)
          setTarget({ id: last.id, name: path[path.length - 1], path: path.join(' / ') })
          return
        } catch {
          // gone, no longer reachable or read-only: fall through
        }
      }
      const mine = ds.find((d) => d.kind === 'personal')
      if (mine) setTarget({ id: mine.rootId, name: driveLabel(t, mine), path: driveLabel(t, mine) })
    }, () => {})
  }, [t])

  async function save(share: PendingShare) {
    if (!target) return
    setSaving(share.id)
    const link = sharedLink(share)
    let files: File[]
    if (link) {
      files = [shortcutFile(link, titles[share.id]?.trim() || previews[share.id]?.title || link.host)]
    } else {
      files = filesOf(share, t('save.textFile')).map((f, i) => {
        const name = names[share.id]?.[i]?.trim() || f.name
        return name === f.name ? f : new File([f], name, { type: f.type, lastModified: f.lastModified })
      })
    }
    const r = await addUploads(files, target.id, target.name, (notes[share.id] ?? '').slice(0, 5000))
    setSaving(null)
    if (r.failed) {
      showToast(t('save.someFailed'), { error: true })
      return
    }
    await deleteShare(share.id).catch(() => {})
    rememberFolder(target)
    setShares((s) => s?.filter((x) => x.id !== share.id) ?? null)
    setSavedTo(target)
    showToast(t('save.saved', { folder: target.path }))
  }

  async function discard(id: string) {
    await deleteShare(id)
    setShares((s) => s?.filter((x) => x.id !== id) ?? null)
  }

  const pending = shares?.filter((s) => sharedLink(s) || filesOf(s, 'text').length > 0) ?? []

  return (
    <div className="settings">
      <h1>{t('save.title')}</h1>

      {status === 'no-worker' && <p className="notice">{t('save.noWorker')}</p>}
      {status === 'failed' && <p className="form-error">{t('save.failed')}</p>}

      {shares && pending.length === 0 && !savedTo && !status && (
        <div className="empty">
          <Icon d={icons.cloudUpload} size={40} stroke="#6b7280" />
          <span>{t('save.empty')}</span>
        </div>
      )}

      {savedTo && pending.length === 0 && (
        <div className="empty">
          <Icon d={icons.check} size={40} stroke="#15803d" />
          <strong>{t('save.saved', { folder: savedTo.path })}</strong>
          <Link className="btn btn--primary" href={hrefFor(savedTo.id, drives)}>
            <Icon d={icons.folder} size={18} />
            {t('save.openFolder')}
          </Link>
        </div>
      )}

      {pending.length > 0 && (
        <section className="panel save-target" aria-label={t('save.saveIn')}>
          <span className="save-target__label">{t('save.saveIn')}</span>
          <span className="save-target__folder">
            <Icon d={icons.folder} stroke="#a16207" fill="#fef9c3" />
            <strong>{target?.path ?? t('loading')}</strong>
          </span>
          <button type="button" className="btn btn--small" disabled={!target || !!saving} onClick={() => setPicking(true)}>
            {t('save.change')}
          </button>
        </section>
      )}

      <UploadPanel />

      {pending.map((share) => {
        const link = sharedLink(share)
        const files = filesOf(share, t('save.textFile'))
        const busy = saving === share.id
        const setNote = (v: string) => setNotes((n) => ({ ...n, [share.id]: v }))
        return (
          <section key={share.id} className="panel">
            <h2>{link ? t('save.linkReceived') : t('save.received', { count: files.length })}</h2>
            {link ? (
              <div className="save-link">
                <span className="save-link__icon" style={{ background: fileTypes.link.bg }}>
                  <Icon d={icons.globe} size={28} stroke={fileTypes.link.c} width={1.5} />
                </span>
                <div className="save-link__text">
                  <input aria-label={t('save.linkTitle')} value={titles[share.id] ?? ''} maxLength={200} disabled={busy}
                    placeholder={previews[share.id]?.title || link.host}
                    onChange={(e) => setTitles((all) => ({ ...all, [share.id]: e.target.value }))} />
                  <span>{linkByline({ url: link.url, site: previews[share.id]?.site ?? link.host, author: previews[share.id]?.author })}</span>
                  <span className="save-link__url">{link.url}</span>
                </div>
              </div>
            ) : (
              <ul className="save-files">
                {files.map((f, i) => {
                  const type = fileTypes[typeFromName(names[share.id]?.[i] || f.name)]
                  return (
                    <li key={i}>
                      <Icon d={type.d} stroke={type.c} />
                      <input aria-label={t('save.fileName', { name: f.name })} value={names[share.id]?.[i] ?? f.name}
                        maxLength={255} disabled={busy}
                        onChange={(e) =>
                          setNames((n) => ({ ...n, [share.id]: (n[share.id] ?? []).map((x, j) => (j === i ? e.target.value : x)) }))
                        } />
                      <span>{formatSize(f.size, locale)}</span>
                    </li>
                  )
                })}
              </ul>
            )}
            {(link || share.files.length > 0) && (
              <DescriptionField value={notes[share.id] ?? ''} onChange={setNote} disabled={busy}
                hint={link ? undefined : t('save.descriptionHint')} />
            )}
            <div className="form__foot save-actions">
              <button type="button" className="btn" disabled={busy} onClick={() => discard(share.id)}>
                {t('save.discard')}
              </button>
              <button type="button" className="btn btn--primary" disabled={!target || !!saving} onClick={() => save(share)}>
                <Icon d={icons.upload} size={18} />
                {t('save.save')}
              </button>
            </div>
          </section>
        )
      })}

      {picking && target && drives.length > 0 && (
        <FolderPicker
          title={t('save.pickTitle')}
          submit={t('save.pickHere')}
          drives={drives}
          startFolder={target.id}
          onClose={() => setPicking(false)}
          onPick={async (id, name, path) => {
            setTarget({ id, name, path })
            setPicking(false)
          }}
        />
      )}
    </div>
  )
}

function DescriptionField({ value, onChange, disabled, hint }: {
  value: string
  onChange: (v: string) => void
  disabled: boolean
  hint?: string
}) {
  const t = useT()
  const id = useId()
  return (
    <div className="field">
      <label htmlFor={id}>{t('info.description')}</label>
      <textarea id={id} className="textarea" rows={3} maxLength={5000} value={value} disabled={disabled}
        placeholder={t('info.descriptionPlaceholder')} onChange={(e) => onChange(e.target.value)} />
      {hint && <span className="field__hint">{hint}</span>}
    </div>
  )
}
