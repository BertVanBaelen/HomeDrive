import { useEffect, useSyncExternalStore } from 'react'
import { useT } from '../i18n'
import { Icon, icons } from './Icon'

interface ToastState {
  id: number
  message: string
  error?: boolean
  undo?: () => void
}

let current: ToastState | null = null
let seq = 0
const listeners = new Set<() => void>()

function set(t: ToastState | null) {
  current = t
  listeners.forEach((fn) => fn())
}

export function showToast(message: string, opts: { error?: boolean; undo?: () => void } = {}) {
  set({ id: ++seq, message, ...opts })
}

// Toast is the one status message at the bottom (role=status, so screen
// readers announce it).
export function Toast() {
  const t = useT()
  const toast = useSyncExternalStore(
    (fn) => {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    () => current,
  )
  useEffect(() => {
    if (!toast) return
    const timer = setTimeout(() => set(null), toast.undo ? 6000 : 3500)
    return () => clearTimeout(timer)
  }, [toast])
  if (!toast) return null
  return (
    <div role="status" key={toast.id} className={'toast' + (toast.error ? ' toast--error' : '')}>
      {toast.error ? (
        <Icon d={icons.exclamation} size={18} />
      ) : (
        <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#4ade80"
          strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
          <path className="toast__check" d={icons.check} />
        </svg>
      )}
      <span>{toast.message}</span>
      {toast.undo && (
        <button type="button" onClick={() => { toast.undo!(); set(null) }}>
          {t('toast.undo')}
        </button>
      )}
    </div>
  )
}
