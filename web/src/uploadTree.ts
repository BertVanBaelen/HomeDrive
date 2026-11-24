// Folders to upload: what a drop or the folder picker gives, as files with
// the folders they sit in. Only folders that hold files are made; browsers
// don't tell the picker about empty ones. No imports: `npm test` loads this
// file with Node directly.

// Picked is a file to upload and the folders it goes in, outermost first,
// below the folder the upload is for.
export interface Picked {
  file: File
  dir: string[]
}

// Limits on one upload, so a mistaken drop (a whole disk) or a loop in the
// folders doesn't flood the queue.
export const maxFiles = 10000
export const maxDepth = 32

export class UploadLimit extends Error {
  kind: 'files' | 'depth'
  max: number
  constructor(kind: 'files' | 'depth', max: number) {
    super(`too many ${kind}`)
    this.kind = kind
    this.max = max
  }
}

// fromFolderInput reads the files of an <input webkitdirectory>, whose
// relative paths start with the chosen folder. Where a browser has no folder
// picker, the files have no path and are uploaded plainly.
export function fromFolderInput(files: Iterable<File>): Picked[] {
  const out: Picked[] = []
  for (const file of files) {
    if (out.length >= maxFiles) throw new UploadLimit('files', maxFiles)
    const dir = file.webkitRelativePath ? file.webkitRelativePath.split('/').slice(0, -1) : []
    if (dir.length > maxDepth) throw new UploadLimit('depth', maxDepth)
    out.push({ file, dir })
  }
  return out
}

// readDropped reads everything in a drop, folders with their subfolders.
// The browser only shows the dropped items to the drop handler, so they are
// all looked at before the first await.
export async function readDropped(dt: DataTransfer): Promise<Picked[]> {
  const entries: FileSystemEntry[] = []
  const loose: File[] = []
  for (const item of dt.items) {
    if (item.kind !== 'file') continue
    const entry = item.webkitGetAsEntry?.()
    const file = entry ? null : item.getAsFile()
    if (entry) entries.push(entry)
    else if (file) loose.push(file)
  }
  const picked = await readEntries(entries)
  if (loose.length + picked.length > maxFiles) throw new UploadLimit('files', maxFiles)
  return [...loose.map((file) => ({ file, dir: [] })), ...picked]
}

// readEntries walks dropped files and folders.
export async function readEntries(entries: FileSystemEntry[]): Promise<Picked[]> {
  const out: Picked[] = []
  const walk = async (entry: FileSystemEntry, dir: string[]): Promise<void> => {
    if (entry.isFile) {
      if (out.length >= maxFiles) throw new UploadLimit('files', maxFiles)
      const file = await new Promise<File>((resolve, reject) => (entry as FileSystemFileEntry).file(resolve, reject))
      out.push({ file, dir })
    } else if (entry.isDirectory) {
      const here = [...dir, entry.name]
      if (here.length > maxDepth) throw new UploadLimit('depth', maxDepth)
      const reader = (entry as FileSystemDirectoryEntry).createReader()
      // A reader answers in parts (100 at a time in Chrome) until an empty one.
      for (;;) {
        const part = await new Promise<FileSystemEntry[]>((resolve, reject) => reader.readEntries(resolve, reject))
        if (!part.length) break
        for (const child of part) await walk(child, here)
      }
    }
  }
  for (const entry of entries) await walk(entry, [])
  return out
}
