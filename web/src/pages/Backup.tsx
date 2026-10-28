import { useCallback, useEffect, useState } from 'react'
import { api, type BackupStatus } from '../api'
import { errorText } from '../components/Dialogs'
import { Icon, icons } from '../components/Icon'
import { showToast } from '../components/Toast'
import { formatDateTime, formatSize } from '../format'
import { useLocale, useT } from '../i18n'

// Backup shows admins how the nightly backup is doing and lets them start
// one now.
export function Backup() {
  const t = useT()
  const locale = useLocale()
  const [st, setSt] = useState<BackupStatus | null>(null)

  const load = useCallback(() => {
    api.backupStatus().then(setSt, (err) => showToast(errorText(t, err), { error: true }))
  }, [t])
  useEffect(load, [load])

  // Follow a running backup until it is done.
  useEffect(() => {
    if (!st?.running) return
    const timer = setInterval(load, 2000)
    return () => clearInterval(timer)
  }, [st?.running, load])

  async function start() {
    try {
      await api.backupNow()
      showToast(t('backup.started'))
      load()
    } catch (err) {
      showToast(errorText(t, err), { error: true })
    }
  }

  if (!st) return null
  return (
    <div className="settings">
      <h1>{t('backup.title')}</h1>
      {!st.enabled ? (
        <section className="panel">
          <p className="panel__body backup__off">{t('backup.off')}</p>
        </section>
      ) : (
        <section className="panel" aria-live="polite">
          <p className="panel__body">{t('backup.schedule', { time: st.time ?? '', days: st.retentionDays ?? 0 })}</p>
          <dl className="facts">
            <dt>{t('backup.last')}</dt>
            <dd>
              {st.running ? (
                <span className="badge badge--invited">{t('backup.running')}</span>
              ) : st.lastRun ? (
                <>
                  {formatDateTime(st.lastRun, locale)}{' '}
                  <span className={'badge ' + (st.lastError ? 'badge--disabled' : 'badge--active')}>
                    {t(st.lastError ? 'backup.failed' : 'backup.ok')}
                  </span>
                </>
              ) : (
                t('backup.never')
              )}
            </dd>
            {st.nextRun && (
              <>
                <dt>{t('backup.next')}</dt>
                <dd>{formatDateTime(st.nextRun, locale)}</dd>
              </>
            )}
            {st.lastSuccess && (
              <>
                <dt>{t('backup.contents')}</dt>
                <dd>
                  {t('backup.summary', { objects: st.objects, size: formatSize(st.bytes, locale), snapshots: st.snapshots })}
                  <br />
                  <span className="facts__muted">{t('backup.copied', { n: st.copied })}</span>
                </dd>
              </>
            )}
          </dl>
          {st.lastError && !st.running && <p className="form-error">{st.lastError}</p>}
          <div className="form__foot">
            <button type="button" className="btn btn--primary" disabled={st.running} onClick={start}>
              <Icon d={icons.cloudUpload} size={18} />
              {t('backup.now')}
            </button>
          </div>
          <p className="panel__body backup__restore">{t('backup.restore')}</p>
        </section>
      )}
    </div>
  )
}
