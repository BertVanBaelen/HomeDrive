// Kokonut UI-inspired status button: pending, success and error motion
// stay on the button and respect the system reduced-motion preference.
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { useRef, useState, type CSSProperties, type MouseEvent, type ReactNode } from 'react'
import { AlertCircle, Check } from 'lucide-react'
import { cn } from '../../lib/utils'

export type ParticleStatus = 'idle' | 'pending' | 'success' | 'error'

export function ParticleStatusButton({
  children,
  onClick,
  status,
  pendingLabel = 'Working',
  successLabel = 'Done',
  errorLabel = 'Try again',
  icon,
  className,
  disabled,
}: {
  children: ReactNode
  onClick?: (event: MouseEvent<HTMLButtonElement>) => unknown
  status?: ParticleStatus
  pendingLabel?: ReactNode
  successLabel?: ReactNode
  errorLabel?: ReactNode
  icon?: ReactNode
  className?: string
  disabled?: boolean
}) {
  const reduce = useReducedMotion()
  const [internal, setInternal] = useState<ParticleStatus>('idle')
  const controlled = status !== undefined
  const state = status ?? internal
  const origin = useRef(0)

  function click(event: MouseEvent<HTMLButtonElement>) {
    if (disabled || state === 'pending') return
    const rect = event.currentTarget.getBoundingClientRect()
    origin.current = event.clientX ? event.clientX - rect.left : rect.width / 2
    if (!onClick) return
    if (controlled) {
      onClick(event)
      return
    }
    setInternal('pending')
    Promise.resolve(onClick(event)).then(
      () => {
        setInternal('success')
        window.setTimeout(() => setInternal('idle'), 1600)
      },
      () => {
        setInternal('error')
        window.setTimeout(() => setInternal('idle'), 3200)
      },
    )
  }

  const label = state === 'pending' ? pendingLabel : state === 'success' ? successLabel : state === 'error' ? errorLabel : children

  return (
    <motion.button
      type="button"
      className={cn(
        'btn btn--primary relative inline-flex items-center justify-center gap-2 overflow-hidden',
        state === 'error' && 'is-error',
        className,
      )}
      disabled={disabled || state === 'pending'}
      onClick={click}
      animate={state === 'error' && !reduce ? { x: [0, -4, 4, -2, 2, 0] } : undefined}
      transition={{ duration: 0.32 }}
      aria-busy={state === 'pending'}
      style={{ '--particle-origin': `${origin.current}px` } as CSSProperties}
    >
      <AnimatePresence mode="wait" initial={false}>
        <motion.span
          key={state}
          className="inline-flex items-center justify-center gap-2"
          initial={reduce ? false : { opacity: 0, y: 4 }}
          animate={{ opacity: 1, y: 0 }}
          exit={reduce ? undefined : { opacity: 0, y: -4 }}
          transition={{ duration: 0.16 }}
        >
          {state === 'pending' ? (
            <span className="status-spinner" aria-hidden="true" />
          ) : state === 'success' ? (
            <Check size={17} aria-hidden="true" />
          ) : state === 'error' ? (
            <AlertCircle size={17} aria-hidden="true" />
          ) : (
            icon
          )}
          {label}
        </motion.span>
      </AnimatePresence>
      {state === 'success' && !reduce && (
        <motion.span
          aria-hidden="true"
          className="pointer-events-none absolute h-1.5 w-1.5 rounded-full bg-white"
          initial={{ left: `var(--particle-origin)`, top: '75%', scale: 0.5, opacity: 1 }}
          animate={{ y: -28, x: [0, 12, -8], scale: [1, 0.8, 0], opacity: [1, 1, 0] }}
          transition={{ duration: 0.55 }}
        />
      )}
    </motion.button>
  )
}
