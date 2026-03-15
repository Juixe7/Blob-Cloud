import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { cn } from '../lib/format'
import { XIcon, CheckIcon, ShareIcon } from './icons'

/** Visual tone of a toast, mirroring the Alert variants. */
type ToastVariant = 'success' | 'info' | 'warning' | 'error'

/** An optional call-to-action rendered as a button on the toast. */
export interface ToastAction {
  label: string
  onClick: () => void
}

/** Programmatic API exposed to consumers via useToast(). */
export interface ToastInput {
  variant?: ToastVariant
  message: string
  /** Optional icon node; defaults based on variant. */
  icon?: ReactNode
  /** Auto-dismiss after this many ms. 0 = sticky (must be closed manually). */
  duration?: number
  action?: ToastAction
}

interface ToastRecord {
  id: number
  variant: ToastVariant
  message: string
  icon?: ReactNode
  duration: number
  action?: ToastAction
}

interface ToastContextValue {
  push: (toast: ToastInput) => void
}

const ToastContext = createContext<ToastContextValue | undefined>(undefined)

let nextId = 1

/**
 * Toast provider + viewport. Renders a stack of auto-dismissing toasts in the
 * bottom-right corner. Used by the Dashboard to surface WebSocket push events
 * (UPLOAD_COMPLETED, FILE_SHARED) with subtle slide-in feedback.
 */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastRecord[]>([])

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }, [])

  const push = useCallback(
    (input: ToastInput) => {
      setToasts((prev) => {
        // If an active toast already has the exact same message, skip adding a duplicate
        if (prev.some((t) => t.message === input.message)) {
          return prev
        }
        const id = nextId++
        const record: ToastRecord = {
          id,
          variant: input.variant ?? 'info',
          message: input.message,
          icon: input.icon,
          duration: input.duration ?? 5000,
        }
        if (input.action) record.action = input.action

        if (record.duration > 0) {
          setTimeout(() => dismiss(id), record.duration)
        }

        const next = [...prev, record]
        // Cap visible toasts to max 3
        return next.length > 3 ? next.slice(next.length - 3) : next
      })
    },
    [dismiss],
  )

  const value = useMemo<ToastContextValue>(() => ({ push }), [push])

  return (
    <ToastContext.Provider value={value}>
      {children}
      {/* Viewport: fixed bottom-right stack, pointer-events none on the
          container but each toast re-enables interaction. */}
      <div
        className="pointer-events-none fixed bottom-24 md:bottom-10 left-1/2 -translate-x-1/2 z-[70] flex w-max max-w-[calc(100vw-2rem)] md:max-w-sm flex-col gap-2 items-center"
        aria-live="polite"
        aria-atomic="false"
      >
        {toasts.map((t) => (
          <ToastCard key={t.id} toast={t} onDismiss={() => dismiss(t.id)} />
        ))}
      </div>
    </ToastContext.Provider>
  )
}

interface ToastCardProps {
  toast: ToastRecord
  onDismiss: () => void
}

function ToastCard({ toast, onDismiss }: ToastCardProps) {
  const defaultIcon =
    toast.variant === 'success' ? <CheckIcon size={16} /> : <ShareIcon size={16} />

  return (
    <div
      role="status"
      className={cn(
        'pointer-events-auto flex items-start gap-2.5 md:gap-3 rounded border border-arch-border bg-arch-950 px-3 py-2.5 md:px-4 md:py-3 shadow-sharp text-zinc-100',
        'animate-[fade-in_0.2s_ease-out]',
        toast.variant === 'success' || toast.variant === 'info'
          ? 'border-zinc-700 bg-zinc-900 text-zinc-100'
          : toast.variant === 'warning'
            ? 'border-amber-500/40 bg-amber-500/10 text-amber-200'
            : toast.variant === 'error'
              ? 'border-rose-500/40 bg-rose-500/10 text-rose-200'
              : 'border-zinc-700 bg-zinc-900 text-zinc-100',
      )}
    >
      <span className="mt-0.5 flex-shrink-0 opacity-90">{toast.icon ?? defaultIcon}</span>
      <p className="flex-1 text-xs md:text-sm leading-snug">{toast.message}</p>
      {toast.action && (
        <button
          type="button"
          onClick={() => {
            toast.action?.onClick()
            onDismiss()
          }}
          className="flex-shrink-0 rounded-md border border-zinc-600/60 bg-zinc-900/60 px-2.5 py-1 text-xs font-medium text-zinc-100 transition-colors hover:bg-zinc-800"
        >
          {toast.action.label}
        </button>
      )}
      <button
        type="button"
        onClick={onDismiss}
        aria-label="Dismiss notification"
        className="flex-shrink-0 rounded p-0.5 text-current opacity-60 transition-opacity hover:opacity-100"
      >
        <XIcon size={14} />
      </button>
    </div>
  )
}

/** Access the toast API. Must be used inside <ToastProvider>. */
// eslint-disable-next-line react-refresh/only-export-components
export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used within a <ToastProvider>')
  return ctx
}

export default ToastProvider
