import { useState } from 'react'
import { api, type Node } from '../api'
import { plural, useT, type Translate } from '../i18n'
import { showToast } from './Toast'
import { errorText } from './Dialogs'
import { Icon, icons } from './Icon'

// batchToast says what a selection action did, and how many items were
// skipped because the user can't change them there.
export function batchToast(t: Translate, res: { done: string[]; skipped: unknown[] }) {
  const parts = [plural(t, 'selection.done', res.done.length)]
  if (res.skipped.length) parts.push(plural(t, 'selection.skipped', res.skipped.length))
  showToast(parts.join(' · '), { error: res.done.length === 0 })
}

// SelectionBar holds the actions for the selected items: floating at the
// bottom on computers, just above the tab bar on phones.
export function SelectionBar({
  nodes,
  ids,
  readOnly = false,
  trashed = false,
  onClear,
  onChanged,
  onMove,
  onDeleteForever,
}: {
  nodes: Node[]
  ids: Set<string>
  readOnly?: boolean
  trashed?: boolean
  onClear: () => void
  onChanged: () => void
  onMove?: (ids: string[]) => void
  onDeleteForever?: (ids: string[]) => void
}) {
  const t = useT()
  const [busy, setBusy] = useState(false)
  const count = ids.size
  if (!count) return null
  const selected = nodes.filter((n) => ids.has(n.id))

  async function run(action: () => Promise<void>) {
    if (busy) return
    setBusy(true)
    try {
      await action()
    } catch (err) {
      showToast(errorText(t, err), { error: true })
    } finally {
      setBusy(false)
    }
  }
  const batch = (op: 'star' | 'trash' | 'restore', starred?: boolean) =>
    run(async () => {
      const res = await api.batch({ op, ids: [...ids], starred })
      batchToast(t, res)
      onClear()
      onChanged()
    })

  return (
    <div className="selection-bar" role="toolbar" aria-label={t('selection.actions')}>
      <div className="selection-bar__count">
        <strong>{plural(t, 'selection.count', count)}</strong>
        <button type="button" className="icon-btn" aria-label={t('selection.clear')} onClick={onClear}>
          <Icon d={icons.close} size={18} />
        </button>
      </div>
      <div className="selection-bar__actions">
        {!trashed && (
          <button type="button" className="selection-action" disabled={busy} onClick={() => void run(() => api.zipMany([...ids]))}>
            <Icon d={icons.download} size={18} /><span>{t('selection.download')}</span>
          </button>
        )}
        {!readOnly && !trashed && (
          <>
            {onMove && (
              <button type="button" className="selection-action" disabled={busy} onClick={() => onMove([...ids])}>
                <Icon d={icons.folder} size={18} /><span>{t('selection.move')}</span>
              </button>
            )}
            <button type="button" className="selection-action" disabled={busy}
              onClick={() => void batch('star', !selected.every((n) => n.starred))}>
              <Icon d={icons.star} size={18} /><span>{t('selection.star')}</span>
            </button>
            <button type="button" className="selection-action selection-action--danger" disabled={busy} onClick={() => void batch('trash')}>
              <Icon d={icons.trash} size={18} /><span>{t('selection.trash')}</span>
            </button>
          </>
        )}
        {trashed && (
          <>
            <button type="button" className="selection-action" disabled={busy} onClick={() => void batch('restore')}>
              <Icon d={icons.restore} size={18} /><span>{t('selection.restore')}</span>
            </button>
            {onDeleteForever && (
              <button type="button" className="selection-action selection-action--danger" disabled={busy}
                onClick={() => onDeleteForever([...ids])}>
                <Icon d={icons.trash} size={18} /><span>{t('selection.deleteForever')}</span>
              </button>
            )}
          </>
        )}
      </div>
    </div>
  )
}
