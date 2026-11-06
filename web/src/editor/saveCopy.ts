import { api, ApiError, type Node } from '../api'
import { notifyChanged } from '../events'

// saveCopy uploads content as a new file named name next to node, or in My
// Files when node's folder is gone or closed to the user meanwhile.
export async function saveCopy(node: Node, name: string, content: Blob): Promise<Node> {
  let folder = node.parentId ?? ''
  let up
  try {
    up = await api.createUpload(folder, name, content.size)
  } catch (err) {
    if (!(err instanceof ApiError && ['not_found', 'forbidden', 'read_only'].includes(err.code))) throw err
    const mine = (await api.drives()).find((d) => d.kind === 'personal')
    if (!mine) throw err
    folder = mine.rootId
    up = await api.createUpload(folder, name, content.size)
  }
  for (let at = 0; at < content.size; ) {
    const { received } = await api.uploadChunk(up.id, at, content.slice(at, at + up.chunkSize))
    if (received <= at) throw new ApiError(500, 'internal')
    at = received
  }
  const copy = await api.completeUpload(up.id)
  notifyChanged(folder)
  return copy
}
