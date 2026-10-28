// Files shared to Homedrive from another app (Android share sheet) wait here
// until the user picks a folder and they are uploaded. Used by both the
// service worker and the page, so it only relies on IndexedDB.

export interface PendingShare {
  id: string
  files: File[]
  title: string
  text: string
  url: string
  receivedAt: number
}

const DB_NAME = 'homedrive'
const DB_VERSION = 1
const STORE = 'pending-shares'

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION)
    req.onupgradeneeded = () => req.result.createObjectStore(STORE, { keyPath: 'id' })
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
}

async function withStore<T>(
  mode: IDBTransactionMode,
  run: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T> {
  const db = await openDb()
  try {
    return await new Promise<T>((resolve, reject) => {
      const tx = db.transaction(STORE, mode)
      const req = run(tx.objectStore(STORE))
      tx.oncomplete = () => resolve(req.result)
      tx.onerror = () => reject(tx.error)
      tx.onabort = () => reject(tx.error)
    })
  } finally {
    db.close()
  }
}

export async function putShare(share: Omit<PendingShare, 'id'>): Promise<string> {
  const id = crypto.randomUUID()
  await withStore('readwrite', (s) => s.put({ ...share, id }))
  return id
}

export async function listShares(): Promise<PendingShare[]> {
  const all = await withStore<PendingShare[]>('readonly', (s) => s.getAll())
  return all.sort((a, b) => a.receivedAt - b.receivedAt)
}

export async function deleteShare(id: string): Promise<void> {
  await withStore('readwrite', (s) => s.delete(id))
}
