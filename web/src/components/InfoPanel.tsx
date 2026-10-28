import { contentUrl, type Node } from '../api'
import { displayName, formatDateTime, formatSize } from '../format'
import { plural, useLocale, useT, type MessageKey } from '../i18n'
import { fileTypes, Icon, icons } from './Icon'
import { Thumb } from './Thumb'

export interface InfoActions {
  onOpen: () => void
  onRename: () => void
  onMove: () => void
  onTrash: () => void
  onStar: () => void
}

// InfoPanel is the ⓘ side panel (a bottom sheet on phones). Items in the
// trash only offer Restore.
export function InfoPanel({
  node,
  location,
  readOnly,
  onClose,
  actions,
  onRestore,
}: {
  node: Node
  location: string
  readOnly: boolean
  onClose: () => void
  actions?: InfoActions
  onRestore?: () => void
}) {
  const t = useT()
  const locale = useLocale()
  const type = fileTypes[node.type]
  const isFolder = node.kind === 'folder'
  const sizeLabel = isFolder ? plural(t, 'count.items', node.childCount ?? 0) : formatSize(node.size, locale)
  const name = displayName(node)

  const starBtn = actions && (
    <button type="button" className={'btn' + (isFolder ? ' btn--green-text' : ' btn--small')} aria-pressed={node.starred}
      onClick={actions.onStar}>
      <Icon d={icons.star} size={isFolder ? 18 : 16} fill={node.starred ? '#f59e0b' : 'none'}
        stroke={node.starred ? '#f59e0b' : 'currentColor'} />
      {node.starred ? t('info.unstar') : t('info.star')}
    </button>
  )

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
            <strong>{name}</strong>
            <span>
              {t(`type.${node.type}` as MessageKey)} · {sizeLabel}
            </span>
          </div>
          {actions && !readOnly && (
            <button type="button" className="icon-btn" aria-label={t('info.renameAria', { name })} onClick={actions.onRename}>
              <Icon d={icons.pencil} size={18} />
            </button>
          )}
        </div>
        {onRestore && !readOnly && (
          <button type="button" className="btn btn--primary btn--block" onClick={onRestore}>
            <Icon d={icons.restore} size={18} />
            {t('info.restore')}
          </button>
        )}
        {actions && (
          <div className="info__actions">
            <div>
              <button type="button" className="btn btn--primary" onClick={actions.onOpen}>
                <Icon d={isFolder ? icons.folder : icons.eye} size={18} />
                {t('info.open')}
              </button>
              {isFolder ? (
                starBtn
              ) : (
                <a className="btn btn--green-text" href={contentUrl(node.id, true)} download>
                  <Icon d={icons.download} size={18} />
                  {t('info.download')}
                </a>
              )}
            </div>
            <div className={isFolder ? '' : 'three'}>
              {!isFolder && starBtn}
              <button type="button" className="btn btn--small" onClick={actions.onMove} disabled={readOnly}>
                <Icon d={icons.move} size={16} />
                {t('info.move')}
              </button>
              <button type="button" className="btn btn--small btn--danger" onClick={actions.onTrash} disabled={readOnly}>
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
          {node.trashedAt && (
            <>
              <dt>{t('info.deleted')}</dt>
              <dd>{formatDateTime(node.trashedAt, locale)}</dd>
            </>
          )}
          <dt>{t('info.location')}</dt>
          <dd>{location}</dd>
        </dl>
      </aside>
    </>
  )
}
