// Tells open views that a folder's contents changed (an upload finished,
// something was moved in or out), so they reload.

type Listener = (folderId: string) => void

const listeners = new Set<Listener>()

export function notifyChanged(folderId: string) {
  listeners.forEach((fn) => fn(folderId))
}

export function onChanged(fn: Listener): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}
