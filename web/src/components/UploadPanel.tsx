import { useState } from 'react'
import { plural, useT } from '../i18n'
import { answerUpload, dismissUpload, retryUpload, useUploads, type UploadItem } from '../uploads'
import type { Choice } from '../uploadChoice'
import { errorText, Modal } from './Dialogs'
import { fileTypes, Icon, icons } from './Icon'

// UploadPanel shows running uploads above the files, like the design, and
// asks about one upload at a time whose name is taken.
export function UploadPanel() {
  const t = useT()
  const uploads = useUploads()
  if (!uploads.length) return null
  const active = uploads.filter((u) => u.state !== 'failed')
  const folders = [...new Set(uploads.map((u) => u.folderName))]
  const asking = uploads.find((u) => u.state === 'asking')
  return (
    <>
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
                    <span className="is-failed">{t('upload.failed')}</span>
                  ) : (
                    <span>{u.state === 'asking' ? t('replace.waiting') : `${pct}%`}</span>
                  )}
                </div>
                <div className={'bar' + (failed ? ' bar--failed' : '')}>
                  <div style={{ width: `${failed ? 100 : pct}%` }} />
                </div>
                {failed && <span className="upload__error">{errorText(t, u.error)}</span>}
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
      {asking && (
        <Collision
          key={asking.key}
          item={asking}
          many={uploads.some((u) => u.batch === asking.batch && u !== asking && u.state !== 'done' && u.state !== 'failed')}
        />
      )}
    </>
  )
}

// Collision asks what to do with an upload whose name is taken. Closing it
// skips the file: nothing is replaced unless the user says so. many offers
// to do the same for the rest of the files added with it.
function Collision({ item, many }: { item: UploadItem; many: boolean }) {
  const t = useT()
  const [all, setAll] = useState(false)
  const [offerAll] = useState(many)
  const answer = (c: Choice) => answerUpload(item.key, c, all)
  return (
    <Modal title={t('replace.title')} onClose={() => answer('skip')}>
      <p className="modal__body replace__body">
        {t('replace.body', { name: item.existing?.name ?? item.name, folder: item.folderName })}
      </p>
      {offerAll && (
        <label className="check">
          <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} />
          <span>{t('replace.all')}</span>
        </label>
      )}
      <div className="modal__foot replace__foot">
        <button type="button" className="btn" onClick={() => answer('skip')}>{t('replace.skip')}</button>
        <button type="button" className="btn" onClick={() => answer('keep')}>{t('replace.keep')}</button>
        <button type="button" className="btn btn--primary" onClick={() => answer('replace')}>{t('replace.replace')}</button>
      </div>
    </Modal>
  )
}
