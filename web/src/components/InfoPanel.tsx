import { contentUrl, type Node } from '../api'
import { displayName, formatDateTime, formatSize } from '../format'
import { plural, useLocale, useT, type MessageKey } from '../i18n'
import { fileTypes, Icon, icons } from './Icon'
import { Thumb } from './Thumb'

// InfoPanel is the ⓘ side panel (a bottom sheet on phones).
export function InfoPanel({
  node,
  location,
  readOnly,
  onClose,
  onOpen,
  onRename,
  onMove,
  onTrash,
  onRestore,
}: {
  node: Node
  location: string
  readOnly: boolean
  onClose: () => void
  onOpen: () => void
  onRename: () => void
  onMove: () => void
  onTrash: () => void
  onRestore: () => void
}) {
  const t = useT()
  const locale = useLocale()
  const type = fileTypes[node.type]
  const isFolder = node.kind === 'folder'
  const sizeLabel = isFolder ? plural(t, 'count.items', node.childCount ?? 0) : formatSize(node.size, locale)
  const trashed = !!node.trashedAt

  return (
    <>
      <button type="button" aria-label={t('info.close')} className="info-scrim" onClick={onClose} />
      <aside aria-label={t('info.title')} className="info">
        <div className="info__head">
          <span>{t('info.title')}</span>
          <button type="button" aria-label={t('info.close')} className="icon-btn" onClick={onClose}>
            <Icon d={icons.close} size={18} />
          </button>
        </div>
        <Thumb node={node} />
        <div className="info__title">
          <Icon d={type.d} stroke={type.c} />
          <div>
            <strong>{displayName(node)}</strong>
            <span>
              {t(`type.${node.type}` as MessageKey)} · {sizeLabel}
            </span>
          </div>
        </div>
        {trashed ? (
          !readOnly && (
            <button type="button" className="btn btn--primary btn--block" onClick={onRestore}>
              <Icon d={icons.restore} size={18} />
              {t('info.restore')}
            </button>
          )
        ) : (
          <div className="info__actions">
            <div>
              <button type="button" className="btn btn--primary" onClick={onOpen}>
                <Icon d={isFolder ? icons.folder : icons.eye} size={18} />
                {t('info.open')}
              </button>
              {isFolder ? (
                <button type="button" className="btn btn--green-text" onClick={onRename} disabled={readOnly}>
                  <Icon d={icons.pencil} size={18} />
                  {t('info.rename')}
                </button>
              ) : (
                <a className="btn btn--green-text" href={contentUrl(node.id, true)} download>
                  <Icon d={icons.download} size={18} />
                  {t('info.download')}
                </a>
              )}
            </div>
            <div className={isFolder ? '' : 'three'}>
              {!isFolder && (
                <button type="button" className="btn btn--small" onClick={onRename} disabled={readOnly}>
                  <Icon d={icons.pencil} size={16} />
                  {t('info.rename')}
                </button>
              )}
              <button type="button" className="btn btn--small" onClick={onMove} disabled={readOnly}>
                <Icon d={icons.move} size={16} />
                {t('info.move')}
              </button>
              <button type="button" className="btn btn--small btn--danger" onClick={onTrash} disabled={readOnly}>
                <Icon d={icons.trash} size={16} />
                {t('info.delete')}
              </button>
            </div>
          </div>
        )}
        <dl>
          <dt>{t('info.fileName')}</dt>
          <dd>{node.name}</dd>
          <dt>{t('info.modified')}</dt>
          <dd>{formatDateTime(node.modifiedAt, locale)}</dd>
          {trashed && (
            <>
              <dt>{t('info.deleted')}</dt>
              <dd>{formatDateTime(node.trashedAt!, locale)}</dd>
            </>
          )}
          <dt>{t('info.location')}</dt>
          <dd>{location}</dd>
        </dl>
      </aside>
    </>
  )
}
