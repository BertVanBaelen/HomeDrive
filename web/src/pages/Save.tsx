import { useEffect, useState } from 'react'
import { useT } from '../i18n'
import { deleteShare, listShares, type PendingShare } from '../shared/shareStore'

// Landing page for files shared from another app. For now it shows what
// arrived; choosing a folder and uploading follow once sign-in exists.
export function Save() {
  const t = useT()
  const status = new URLSearchParams(location.search).get('status')
  const [shares, setShares] = useState<PendingShare[] | null>(null)

  useEffect(() => {
    listShares()
      .then(setShares)
      .catch(() => setShares([]))
  }, [])

  async function discard(id: string) {
    await deleteShare(id)
    setShares((s) => s?.filter((x) => x.id !== id) ?? null)
  }

  return (
    <main className="hd-page">
      <h1>{t('save.title')}</h1>

      {status === 'no-worker' && <p className="hd-notice">{t('save.noWorker')}</p>}
      {status === 'failed' && <p className="hd-notice hd-notice--error">{t('save.failed')}</p>}

      {shares?.length === 0 && !status && <p className="hd-muted">{t('save.empty')}</p>}

      {shares?.map((share) => (
        <section key={share.id} className="hd-card">
          <strong>{t('save.received', { count: share.files.length })}</strong>
          <ul className="hd-files">
            {share.files.map((f, i) => (
              <li key={i}>
                <span className="hd-files__name">{f.name}</span>
                <span className="hd-muted">{formatSize(f.size)}</span>
              </li>
            ))}
          </ul>
          {(share.text || share.url) && (
            <p className="hd-muted">
              {t('save.sharedText')}: {[share.title, share.text, share.url].filter(Boolean).join(' · ')}
            </p>
          )}
          <p className="hd-notice">{t('save.pendingNote')}</p>
          <div className="hd-actions">
            <button type="button" className="hd-button" onClick={() => discard(share.id)}>
              {t('save.discard')}
            </button>
          </div>
        </section>
      ))}
    </main>
  )
}

function formatSize(bytes: number): string {
  const units = ['B', 'KB', 'MB', 'GB']
  let n = bytes
  let u = 0
  while (n >= 1024 && u < units.length - 1) {
    n /= 1024
    u++
  }
  return `${n < 10 && u > 0 ? n.toFixed(1) : Math.round(n)} ${units[u]}`
}
