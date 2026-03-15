import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react'
import { cn } from '../../lib/format'

interface ModalProps {
  /** Whether the dialog is mounted and visible. */
  open: boolean
  /** Close handler — invoked on backdrop click, Escape, or explicit close. */
  onClose: () => void
  /** Accessible name of the dialog (rendered for screen readers). */
  label: string
  /** Dialog content. */
  children: ReactNode
  /** Max-width utility class. Defaults to a 28rem (md) dialog. */
  maxWidthClass?: string
  /** Vertical anchor position. 'center' (default) or 'top' for taller dialogs. */
  position?: 'center' | 'top'
  /** When true, Escape/backdrop clicks are ignored (use during async submit). */
  locked?: boolean
}

/**
 * Reusable accessible modal shell shared by every Phase 7.4 dialog.
 *
 * Implements:
 *  - Body scroll lock while open.
 *  - Focus trap (Tab / Shift+Tab cycles within the dialog) with restoration
 *    of focus to the previously active element on close.
 *  - Escape to close (unless `locked`).
 *  - Backdrop click to close (unless `locked`).
 *
 * Visual style matches NewFolderModal: dark zinc-900 card, subtle blur backdrop.
 */
export function Modal({
  open,
  onClose,
  label,
  children,
  maxWidthClass = 'max-w-md',
  position = 'center',
  locked = false,
}: ModalProps) {
  const dialogRef = useRef<HTMLDivElement>(null)
  // The element that held focus before the modal opened — restored on close.
  const previousFocusRef = useRef<HTMLElement | null>(null)
  const [mounted, setMounted] = useState(open)

  // Mount/unmount with a frame's delay so transitions can play. We keep it
  // simple here: render when open, and tear down immediately on close.
  useEffect(() => {
    setMounted(open)
  }, [open])

  // Focus the first focusable element when the dialog opens; restore on close.
  useEffect(() => {
    if (!open) return

    previousFocusRef.current = (document.activeElement as HTMLElement) ?? null
    const t = requestAnimationFrame(() => {
      const first = dialogRef.current?.querySelector<HTMLElement>(
        'input, button, select, textarea, [tabindex]:not([tabindex="-1"])',
      )
      first?.focus()
    })

    return () => {
      cancelAnimationFrame(t)
      previousFocusRef.current?.focus?.()
    }
  }, [open])

  // Lock background scroll while any modal is open.
  useEffect(() => {
    if (!open) return
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = prev
    }
  }, [open])

  // Focus trap + Escape (uses the native KeyboardEvent, not React's synthetic).
  useEffect(() => {
    if (!open) return

    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape' && !locked) {
        e.stopPropagation()
        onClose()
        return
      }
      if (e.key !== 'Tab' || !dialogRef.current) return

      const focusable = dialogRef.current.querySelectorAll<HTMLElement>(
        'input, button, select, textarea, [tabindex]:not([tabindex="-1"])',
      )
      if (focusable.length === 0) return

      const first = focusable[0]
      const last = focusable[focusable.length - 1]

      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [open, onClose, locked])

  const handleBackdropClick = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      if (locked) return
      if (e.target === e.currentTarget) onClose()
    },
    [onClose, locked],
  )

  if (!mounted) return null

  const wrapperPosition: CSSProperties =
    position === 'top'
      ? { alignItems: 'flex-start', paddingTop: '8vh' }
      : { alignItems: 'center' }

  return (
    <div
      className={cn(
        'fixed inset-0 z-50 flex justify-center bg-arch-950/80 backdrop-blur-md animate-fade-in p-4',
      )}
      style={wrapperPosition}
      onClick={handleBackdropClick}
      role="presentation"
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={label}
        tabIndex={-1}
        className={cn(
          'relative w-full rounded-lg border border-arch-border bg-arch-900 p-6 shadow-sharp text-zinc-100',
          'focus:outline-none',
          maxWidthClass,
          'animate-[modal-in_150ms_ease-out]',
        )}
      >
        {children}
      </div>
    </div>
  )
}

export default Modal
