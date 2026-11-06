import { useEffect, useRef, useState } from 'react'
import { api, type Node } from '../api'
import { Session } from './session'

export type { Payload, SaveStatus } from './session'
export { Session }

// Saver writes an editor's payload as the file's new content, replacing
// version, and answers the file's details (with its new version).
export type Saver<P> = (lockId: string, version: string, payload: P) => Promise<Node>

// EditorBodyProps are what the editor frame gives each kind of editor: the
// session (read its content again when session.revision changes, call
// session.change with each edit, respect session.readOnly) and an element
// in the frame's header for extra controls (through a portal).
export interface EditorBodyProps<P> {
  session: Session<Node, P>
  header: HTMLElement | null
}

// useEditSession edits one file with any kind of content (P): it takes the
// file's edit lock unless lockId is given, keeps it while the page is
// visible, saves changes a moment after the last one, and warns before the
// page closes with changes not saved. It is null until the session starts.
export function useEditSession<P>(node: Node, lockId: string | undefined, save: Saver<P>): Session<Node, P> | null {
  const [session, setSession] = useState<Session<Node, P> | null>(null)
  const [, setTick] = useState(0)
  const saver = useRef(save)
  const given = useRef(lockId)
  const start = useRef(node)
  useEffect(() => {
    saver.current = save
  })

  useEffect(() => {
    const id = start.current.id
    const s = new Session<Node, P>(
      start.current,
      given.current,
      {
        load: () => api.node(id).then((d) => d.node),
        take: () => api.takeLock(id).then((r) => r.lockId),
        renew: (l) => api.renewLock(id, l),
        release: (l) => api.releaseLock(id, l),
        save: (l, v, p) => saver.current(l, v, p),
      },
      () => setTick((n) => n + 1),
    )
    // The lock given is used once: a remount takes a new one.
    given.current = undefined
    setSession(s)
    const onVisibility = () => (document.visibilityState === 'hidden' ? void s.hide() : s.show())
    const onHide = () => void s.leave()
    const onShow = () => s.show()
    const onUnload = (e: BeforeUnloadEvent) => s.dirty && e.preventDefault()
    const onOnline = () => s.retry()
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('pagehide', onHide)
    window.addEventListener('pageshow', onShow)
    window.addEventListener('beforeunload', onUnload)
    window.addEventListener('online', onOnline)
    return () => {
      document.removeEventListener('visibilitychange', onVisibility)
      window.removeEventListener('pagehide', onHide)
      window.removeEventListener('pageshow', onShow)
      window.removeEventListener('beforeunload', onUnload)
      window.removeEventListener('online', onOnline)
      void s.dispose()
    }
  }, [])

  return session
}
