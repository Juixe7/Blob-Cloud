import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from 'react'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { Button } from './ui/Button'
import { Input } from './ui/Input'
import { Alert } from './ui/Alert'
import type { FileItem } from '../types/file'

interface NewFolderModalProps {
  open: boolean
  onClose: () => void
  /** The parent folder under which the new folder is created. null = root. */
  parentId: string | null
  /** Callback after successful creation — receives the new FileItem. */
  onCreated: (folder: FileItem) => void
}

/**
 * Accessible modal for creating a new folder. Implements:
 * - Focus trapping (Tab / Shift+Tab cycles within the dialog)
 * - Escape to close
 * - Backdrop click to close
 * - POST /api/folders on submit, with error handling
 */
export function NewFolderModal({ open, onClose, parentId, onCreated }: NewFolderModalProps) {
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const dialogRef = useRef<HTMLDivElement>(null)

  // Focus the input when the modal opens
  useEffect(() => {
    if (open) {
      // Small delay so the transition renders first
      const t = requestAnimationFrame(() => inputRef.current?.focus())
      return () => cancelAnimationFrame(t)
    }
    // Reset state on close
    setName('')
    setError(null)
    setLoading(false)
  }, [open])

  // Focus trapping (native DOM listener — uses globalThis.KeyboardEvent,
  // not React's synthetic event type)
  useEffect(() => {
    if (!open) return

    function handleTab(e: globalThis.KeyboardEvent) {
      if (e.key !== 'Tab' || !dialogRef.current) return

      const focusable = dialogRef.current.querySelectorAll<HTMLElement>(
        'input, button, [tabindex]:not([tabindex="-1"])',
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

    document.addEventListener('keydown', handleTab)
    return () => document.removeEventListener('keydown', handleTab)
  }, [open])

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) {
      setError('Folder name is required.')
      return
    }
    if (trimmed.length > 255) {
      setError('Folder name must be 255 characters or fewer.')
      return
    }

    setLoading(true)
    setError(null)

    try {
      const res = await apiClient.post<FileItem>('/folders', {
        name: trimmed,
        parent_id: parentId,
      })
      onCreated(res.data)
      onClose()
    } catch (err) {
      if (axios.isAxiosError(err)) {
        const data = err.response?.data as { error?: string; message?: string } | undefined
        setError(data?.error || data?.message || 'Failed to create folder.')
      } else {
        setError('An unexpected error occurred.')
      }
    } finally {
      setLoading(false)
    }
  }

  const handleBackdropClick = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      if (e.target === e.currentTarget) onClose()
    },
    [onClose],
  )

  const handleEscape = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    },
    [onClose],
  )

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-zinc-950/60 backdrop-blur-sm animate-fade-in"
      onClick={handleBackdropClick}
      onKeyDown={handleEscape}
      role="presentation"
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label="Create new folder"
        className="relative w-full max-w-md rounded-xl border border-zinc-800 bg-zinc-900 p-6 shadow-2xl"
      >
        {/* Title */}
        <h2 className="mb-5 text-lg font-semibold text-zinc-50">New Folder</h2>

        {/* Server error */}
        {error && (
          <div className="mb-4">
            <Alert variant="error">{error}</Alert>
          </div>
        )}

        {/* Form */}
        <form onSubmit={handleSubmit} noValidate>
          <div className="mb-5">
            <label htmlFor="new-folder-name" className="mb-1.5 block text-sm font-medium text-zinc-300">
              Folder name
            </label>
            <Input
              ref={inputRef}
              id="new-folder-name"
              type="text"
              placeholder="Untitled Folder"
              value={name}
              onChange={(e) => {
                setName(e.target.value)
                if (error) setError(null)
              }}
              disabled={loading}
              autoFocus
            />
          </div>

          <div className="flex items-center justify-end gap-3">
            <Button
              type="button"
              variant="secondary"
              onClick={onClose}
              disabled={loading}
            >
              Cancel
            </Button>
            <Button type="submit" loading={loading}>
              Create
            </Button>
          </div>
        </form>
      </div>
    </div>
  )
}

export default NewFolderModal
