import { plural, useT } from '../i18n'
import { dismissUpload, retryUpload, useUploads } from '../uploads'
import { errorText } from './Dialogs'
import { ApiError } from '../api'
import { fileTypes, Icon, icons } from './Icon'

// UploadPanel shows running uploads above the files, like the design.
export function UploadPanel() {
  const t = useT()
  const uploads = useUploads()
  if (!uploads.length) return null
  const active = uploads.filter((u) => u.state !== 'failed')
  const folders = [...new Set(uploads.map((u) => u.folderName))]
  return (
    <div className="uploads" aria-live="polite">
      <div className="uploads__head">
        <strong>{plural(t, 'upload.title', active.length || uploads.length)}</strong>
        {folders.length === 1 && <span>{t('upload.to', { folder: folders[0] })}</span>}
      </div>
      {uploads.map((u) => {
        const pct = u.file.size ? Math.min(100, Math.round((u.sent / u.file.size) * 100)) : u.state === 'done' ? 100 : 0
        const failed = u.state === 'failed'
        const type = fileTypes[u.type]
        return (
          <div key={u.key} className="upload">
            <Icon d={type.d} stroke={type.c} />
            <div className="upload__body">
              <div className="upload__line">
                <span>{u.name}</span>
                {failed ? (
                  <span className="is-failed" title={errorText(t, new ApiError(0, u.error ?? 'internal'))}>
                    {t('upload.failed')}
                  </span>
                ) : (
                  <span>{pct}%</span>
                )}
              </div>
              <div className={'bar' + (failed ? ' bar--failed' : '')}>
                <div style={{ width: `${failed ? 100 : pct}%` }} />
              </div>
            </div>
            {failed && (
              <>
                <button type="button" className="icon-btn" aria-label={t('upload.retry')} onClick={() => retryUpload(u.key)}>
                  <Icon d={icons.restore} size={18} />
                </button>
                <button type="button" className="icon-btn" aria-label={t('upload.dismiss')} onClick={() => dismissUpload(u.key)}>
                  <Icon d={icons.close} size={18} />
                </button>
              </>
            )}
          </div>
        )
      })}
    </div>
  )
}
