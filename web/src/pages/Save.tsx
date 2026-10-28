import { useEffect, useState } from 'react'
import { api, type Drive } from '../api'
import { FolderPicker } from '../components/Dialogs'
import { fileTypes, Icon, icons } from '../components/Icon'
import { driveLabel, hrefFor } from '../components/Items'
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

// filesOf is what a share uploads: its files, or its text as a .txt when
// only text or a link was shared.
function filesOf(share: PendingShare, textName: string): File[] {
  if (share.files.length) return share.files
  const text = [share.title, share.text, share.url].filter(Boolean).join('\n')
  return text ? [new File([text + '\n'], `${textName}.txt`, { type: 'text/plain' })] : []
}

// Save is where files shared from another app land (Android share sheet,
// through the service worker). The user picks a folder (the last one is
// preselected), may rename, and uploads. Files stay on the device until
// they are uploaded.
export function Save() {
  const t = useT()
  const locale = useLocale()
  const status = new URLSearchParams(location.search).get('status')
  const [shares, setShares] = useState<PendingShare[] | null>(null)
  const [names, setNames] = useState<Record<string, string[]>>({})
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
          const path = [driveLabel(t, d.drive), ...d.path.slice(1).map((c) => c.name)]
          if (d.node.id !== d.drive.rootId) path.push(d.node.name)
          setTarget({ id: last.id, name: path[path.length - 1], path: path.join(' / ') })
          return
        } catch {
          // gone or no longer reachable: fall through
        }
      }
      const mine = ds.find((d) => d.kind === 'personal')
      if (mine) setTarget({ id: mine.rootId, name: driveLabel(t, mine), path: driveLabel(t, mine) })
    }, () => {})
  }, [t])

  async function save(share: PendingShare) {
    if (!target) return
    setSaving(share.id)
    const original = filesOf(share, t('save.textFile'))
    const files = original.map((f, i) => {
      const name = names[share.id]?.[i]?.trim() || f.name
      return name === f.name ? f : new File([f], name, { type: f.type, lastModified: f.lastModified })
    })
    const r = await addUploads(files, target.id, target.name)
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

  const pending = shares?.filter((s) => filesOf(s, 'text').length > 0) ?? []

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
        const files = filesOf(share, t('save.textFile'))
        return (
          <section key={share.id} className="panel">
            <h2>{t('save.received', { count: files.length })}</h2>
            <ul className="save-files">
              {files.map((f, i) => {
                const type = fileTypes[typeFromName(names[share.id]?.[i] || f.name)]
                return (
                  <li key={i}>
                    <Icon d={type.d} stroke={type.c} />
                    <input aria-label={t('save.fileName', { name: f.name })} value={names[share.id]?.[i] ?? f.name}
                      maxLength={255} disabled={saving === share.id}
                      onChange={(e) =>
                        setNames((n) => ({ ...n, [share.id]: (n[share.id] ?? []).map((x, j) => (j === i ? e.target.value : x)) }))
                      } />
                    <span>{formatSize(f.size, locale)}</span>
                  </li>
                )
              })}
            </ul>
            {share.files.length > 0 && (share.text || share.url) && (
              <p className="panel__body">
                {t('save.sharedText')}: {[share.title, share.text, share.url].filter(Boolean).join(' · ')}
              </p>
            )}
            <div className="form__foot save-actions">
              <button type="button" className="btn" disabled={saving === share.id} onClick={() => discard(share.id)}>
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
