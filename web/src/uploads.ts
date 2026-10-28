import { useSyncExternalStore } from 'react'
import { api, ApiError, type FileType, type Node } from './api'
import { notifyChanged } from './events'

// Uploads run in the background while the user keeps browsing. Each file is
// sent in chunks (the server's chunkSize, under Cloudflare's body limit); a
// dropped connection resumes from what the server has.

export interface UploadItem {
  key: number
  file: File
  name: string
  type: FileType
  folderId: string
  folderName: string
  sent: number
  state: 'queued' | 'uploading' | 'done' | 'failed'
  error?: string
}

type Listener = () => void

const parallel = 2
const maxRetries = 5

let items: UploadItem[] = []
let nextKey = 1
const listeners = new Set<Listener>()
const doneListeners = new Set<(n: number) => void>()

function emit() {
  items = [...items]
  listeners.forEach((fn) => fn())
}

function update(key: number, patch: Partial<UploadItem>) {
  items = items.map((it) => (it.key === key ? { ...it, ...patch } : it))
  listeners.forEach((fn) => fn())
}

export function useUploads(): UploadItem[] {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    () => items,
  )
}

// onBatchDone is told how many files finished when the queue runs empty.
export function onBatchDone(fn: (n: number) => void): () => void {
  doneListeners.add(fn)
  return () => doneListeners.delete(fn)
}

// settled holds, per upload, who waits for it to finish or fail.
const settled = new Map<number, (ok: boolean) => void>()

// addUploads queues files for a folder. The promise resolves once each of
// them has finished or failed (a later retry doesn't count).
export function addUploads(files: Iterable<File>, folderId: string, folderName: string): Promise<{ done: number; failed: number }> {
  const waits: Promise<boolean>[] = []
  for (const file of files) {
    const key = nextKey++
    items.push({
      key,
      file,
      name: file.name,
      type: typeFromName(file.name),
      folderId,
      folderName,
      sent: 0,
      state: 'queued',
    })
    waits.push(new Promise((resolve) => settled.set(key, resolve)))
  }
  emit()
  pump()
  return Promise.all(waits).then((oks) => ({ done: oks.filter(Boolean).length, failed: oks.filter((ok) => !ok).length }))
}

function settle(key: number, ok: boolean) {
  settled.get(key)?.(ok)
  settled.delete(key)
}

export function retryUpload(key: number) {
  update(key, { state: 'queued', error: undefined, sent: 0 })
  pump()
}

export function dismissUpload(key: number) {
  items = items.filter((it) => it.key !== key)
  emit()
}

function pump() {
  const running = items.filter((it) => it.state === 'uploading').length
  const next = items.filter((it) => it.state === 'queued').slice(0, Math.max(0, parallel - running))
  for (const it of next) {
    update(it.key, { state: 'uploading' })
    run(it).then(
      () => {
        update(it.key, { state: 'done', sent: it.file.size })
        notifyChanged(it.folderId)
        settle(it.key, true)
        finish()
      },
      (err) => {
        update(it.key, { state: 'failed', error: err instanceof ApiError ? err.code : 'internal' })
        settle(it.key, false)
        finish()
      },
    )
  }
}

function finish() {
  pump()
  if (items.some((it) => it.state === 'queued' || it.state === 'uploading')) return
  const done = items.filter((it) => it.state === 'done')
  if (done.length) {
    doneListeners.forEach((fn) => fn(done.length))
    items = items.filter((it) => it.state !== 'done')
    emit()
  }
}

async function run(it: UploadItem): Promise<Node> {
  const up = await api.createUpload(it.folderId, it.name, it.file.size)
  let received = 0
  let retries = 0
  while (received < it.file.size) {
    const end = Math.min(received + up.chunkSize, it.file.size)
    try {
      received = await putChunk(up.id, received, it.file.slice(received, end), (loaded) =>
        update(it.key, { sent: received + loaded }),
      )
      retries = 0
    } catch (err) {
      if (err instanceof ApiError && err.code === 'offset_mismatch' && typeof err.body.received === 'number') {
        received = err.body.received
        continue
      }
      if (!(err instanceof ApiError && err.status === 0) || ++retries > maxRetries) throw err
      // Network trouble: wait, then ask the server where to continue.
      await new Promise((r) => setTimeout(r, 1000 * 2 ** retries))
      received = (await api.uploadStatus(up.id)).received
    }
    update(it.key, { sent: received })
  }
  return api.completeUpload(up.id)
}

// putChunk sends one chunk with XMLHttpRequest, which (unlike fetch) reports
// upload progress. It resolves with the server's received count.
function putChunk(id: string, offset: number, blob: Blob, onProgress: (loaded: number) => void): Promise<number> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', `/api/uploads/${encodeURIComponent(id)}?offset=${offset}`)
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    xhr.upload.onprogress = (e) => onProgress(e.loaded)
    xhr.onload = () => {
      let body: Record<string, unknown> = {}
      try {
        body = JSON.parse(xhr.responseText)
      } catch {
        // keep the empty body
      }
      if (xhr.status === 200 && typeof body.received === 'number') resolve(body.received)
      else reject(new ApiError(xhr.status, typeof body.error === 'string' ? body.error : 'internal', body))
    }
    xhr.onerror = () => reject(new ApiError(0, 'network'))
    xhr.send(blob)
  })
}

const extTypes: Record<string, FileType> = {
  md: 'text', txt: 'text', log: 'text',
  xlsx: 'sheet', xls: 'sheet', ods: 'sheet', csv: 'sheet',
  doc: 'doc', docx: 'doc', odt: 'doc', rtf: 'doc', pptx: 'doc',
  pdf: 'pdf',
  jpg: 'image', jpeg: 'image', png: 'image', gif: 'image', webp: 'image', avif: 'image', bmp: 'image',
  heic: 'image', heif: 'image', svg: 'image', tif: 'image', tiff: 'image',
  mp4: 'video', m4v: 'video', mov: 'video', webm: 'video', mkv: 'video', avi: 'video',
  mp3: 'audio', m4a: 'audio', aac: 'audio', wav: 'audio', ogg: 'audio', opus: 'audio', flac: 'audio',
  zip: 'archive', '7z': 'archive', tar: 'archive', gz: 'archive', tgz: 'archive', rar: 'archive',
  json: 'code', yaml: 'code', yml: 'code', xml: 'code', html: 'code', htm: 'code', css: 'code', js: 'code',
  ts: 'code', py: 'code', go: 'code', sh: 'code', ini: 'code', toml: 'code',
}

// Mirrors the server's mapping (server/internal/files/types.go) for files
// that don't exist on the server yet.
export function typeFromName(name: string): FileType {
  const dot = name.lastIndexOf('.')
  return (dot >= 0 && extTypes[name.slice(dot + 1).toLowerCase()]) || 'doc'
}
