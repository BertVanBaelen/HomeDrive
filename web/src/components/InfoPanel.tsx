import { useContentUrl } from './content'
import { zipUrl, type Drive, type Node } from '../api'
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
  onShare?: () => void
}

// InfoPanel is the ⓘ side panel (a bottom sheet on phones). Items in the
// trash only offer Restore. drive is where the item lives: it tells whether
// the user can share it, and who shared it with them.
export function InfoPanel({
  node,
  drive,
  location,
  readOnly,
  onClose,
  actions,
  onRestore,
  onDeleteForever,
}: {
  node: Node
  drive?: Drive
  location: string
  readOnly: boolean
  onClose: () => void
  actions?: InfoActions
  onRestore?: () => void
  onDeleteForever?: () => void
}) {
  const t = useT()
  const locale = useLocale()
  const contentUrl = useContentUrl()
  const type = fileTypes[node.type]
  const isFolder = node.kind === 'folder'
  const sizeLabel = isFolder ? plural(t, 'count.items', node.childCount ?? 0) : formatSize(node.size, locale)
  const name = displayName(node)
  // What was shared with the user can't be renamed, moved or deleted by
  // them, only what is in it.
  const isShareRoot = drive?.kind === 'share' && drive.rootId === node.id
  const locked = readOnly || isShareRoot
  const canShare = !!actions?.onShare && !!drive?.canShare && !node.trashedAt

  const starBtn = actions && (
    <button type="button" className="btn btn--small" aria-pressed={node.starred} onClick={actions.onStar}>
      <Icon d={icons.star} size={16} fill={node.starred ? '#f59e0b' : 'none'}
        stroke={node.starred ? '#f59e0b' : 'currentColor'} />
      {node.starred ? t('info.unstar') : t('info.star')}
    </button>
  )
  const download = (cls: string, size: number) => (
    <a
      className={cls}
      href={isFolder ? zipUrl(node.id) : contentUrl(node.id, true)}
      title={isFolder ? t('info.zip') : undefined}
      download
    >
      <Icon d={isFolder ? icons.archive : icons.download} size={size} />
      {t('info.download')}
    </a>
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
          {actions && !locked && (
            <button type="button" className="icon-btn" aria-label={t('info.renameAria', { name })} onClick={actions.onRename}>
              <Icon d={icons.pencil} size={18} />
            </button>
          )}
        </div>
        {onRestore && !readOnly && (
          <div className="info__actions">
            <button type="button" className="btn btn--primary btn--block" onClick={onRestore}>
              <Icon d={icons.restore} size={18} />
              {t('info.restore')}
            </button>
            {onDeleteForever && (
              <button type="button" className="btn btn--small btn--danger btn--block" onClick={onDeleteForever}>
                <Icon d={icons.trash} size={16} />
                {t('trash.deleteForever')}
              </button>
            )}
          </div>
        )}
        {actions && (
          <div className="info__actions">
            <div>
              <button type="button" className="btn btn--primary" onClick={actions.onOpen}>
                <Icon d={isFolder ? icons.folder : icons.eye} size={18} />
                {t('info.open')}
              </button>
              {canShare ? (
                <button type="button" className="btn btn--green-text" onClick={actions.onShare}>
                  <Icon d={icons.share} size={18} />
                  {t('info.share')}
                </button>
              ) : (
                download('btn btn--green-text', 18)
              )}
            </div>
            <div className={canShare ? '' : 'three'}>
              {canShare && download('btn btn--small btn--green-text', 16)}
              {starBtn}
              <button type="button" className="btn btn--small" onClick={actions.onMove} disabled={locked}>
                <Icon d={icons.move} size={16} />
                {t('info.move')}
              </button>
              <button type="button" className="btn btn--small btn--danger" onClick={actions.onTrash} disabled={locked}>
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
          {drive?.kind === 'share' && drive.sharedBy && (
            <>
              <dt>{t('info.sharedBy')}</dt>
              <dd>{drive.sharedBy}</dd>
            </>
          )}
        </dl>
      </aside>
    </>
  )
}
