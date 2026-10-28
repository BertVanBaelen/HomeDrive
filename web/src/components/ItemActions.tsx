import { useState } from 'react'
import { api, type Drive, type Node } from '../api'
import { notifyChanged } from '../events'
import { useT } from '../i18n'
import { openFolder } from '../motion'
import { errorText, MoveDialog, NameDialog } from './Dialogs'
import { hrefFor, storedSort } from './Items'
import { showToast } from './Toast'
import { Viewer } from './Viewer'

type Dialog = { kind: 'rename' | 'move'; node: Node } | null

interface ViewerState {
  files: Node[]
  index: number
  location: (n: Node) => string
}

// useItemActions is what can be done to an item wherever it is listed:
// in its folder, in Recent, Starred or search results. elements holds the
// dialogs and viewer to render.
export function useItemActions({ drives, reload }: { drives: Drive[]; reload: () => void }) {
  const t = useT()
  const [dialog, setDialog] = useState<Dialog>(null)
  const [viewer, setViewer] = useState<ViewerState | null>(null)

  const fail = (err: unknown) => showToast(errorText(t, err), { error: true })

  // open goes into a folder (zooming in from its card), or shows a file
  // among the files of list.
  function open(n: Node, list: Node[], location: (n: Node) => string) {
    if (n.kind === 'folder') {
      const sort = storedSort()
      openFolder(n.id, hrefFor(n.id, drives), sort.key, sort.desc)
      return
    }
    const files = list.filter((x) => x.kind === 'file')
    setViewer({ files, index: files.findIndex((f) => f.id === n.id), location })
  }

  async function star(n: Node) {
    try {
      await api.star(n.id, !n.starred)
      reload()
      notifyChanged('starred')
      showToast(t(n.starred ? 'toast.unstarred' : 'toast.starred'))
    } catch (err) {
      fail(err)
    }
  }

  async function trash(n: Node) {
    const changed = () => {
      reload()
      notifyChanged('trash')
      notifyChanged('starred')
      if (n.parentId) notifyChanged(n.parentId)
    }
    try {
      await api.trash(n.id)
      changed()
      showToast(t('toast.trashed'), { undo: () => api.restore(n.id).then(changed, fail) })
    } catch (err) {
      fail(err)
    }
  }

  const elements = (
    <>
      {dialog?.kind === 'rename' && (
        <NameDialog
          title={t('rename.title')}
          label={t('rename.label')}
          initial={dialog.node.name}
          submit={t('rename.submit')}
          selectStem={dialog.node.kind === 'file'}
          onClose={() => setDialog(null)}
          onSubmit={async (name) => {
            await api.rename(dialog.node.id, name)
            setDialog(null)
            reload()
            showToast(t('toast.renamed'))
          }}
        />
      )}
      {dialog?.kind === 'move' && (
        <MoveDialog
          node={dialog.node}
          drives={drives}
          startFolder={dialog.node.parentId ?? drives[0].rootId}
          onClose={() => setDialog(null)}
          onMove={async (target, targetName) => {
            await api.move(dialog.node.id, target)
            setDialog(null)
            reload()
            notifyChanged(target)
            showToast(t('toast.moved', { folder: targetName }))
          }}
        />
      )}
      {viewer && viewer.files[viewer.index] && (
        <Viewer
          files={viewer.files}
          index={viewer.index}
          location={viewer.location(viewer.files[viewer.index])}
          onIndex={(index) => setViewer({ ...viewer, index })}
          onClose={() => setViewer(null)}
        />
      )}
    </>
  )

  return {
    open,
    star,
    trash,
    rename: (n: Node) => setDialog({ kind: 'rename', node: n }),
    move: (n: Node) => setDialog({ kind: 'move', node: n }),
    elements,
  }
}
