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
import { XIcon } from './icons'
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
 * Precision Utilitarian New Folder Dialog.
 * Zero glassmorphism, solid surface `#111317`, 1px border rule.
 */
export function NewFolderModal({ open, onClose, parentId, onCreated }: NewFolderModalProps) {
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const dialogRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (open) {
      const t = requestAnimationFrame(() => inputRef.current?.focus())
      return () => cancelAnimationFrame(t)
    }
    setName('')
    setError(null)
    setLoading(false)
  }, [open])

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
      className="fixed inset-0 z-50 flex items-center justify-center bg-arch-950/90 animate-fade-in p-4 select-none"
      onClick={handleBackdropClick}
      onKeyDown={handleEscape}
      role="presentation"
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label="Create new folder"
        className="relative w-full max-w-md rounded-md border border-arch-border bg-arch-900 p-5 shadow-sharp text-zinc-100"
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-arch-border pb-3 mb-4">
          <h2 className="font-display text-sm font-bold text-zinc-100">CREATE NEW FOLDER</h2>
          <button type="button" onClick={onClose} className="rounded p-1 text-zinc-400 hover:bg-arch-850 hover:text-zinc-200 transition-colors" aria-label="Close dialog">
            <XIcon size={16} />
          </button>
        </div>

        {/* Server error */}
        {error && (
          <div className="mb-4">
            <Alert variant="error">{error}</Alert>
          </div>
        )}

        {/* Form */}
        <form onSubmit={handleSubmit} noValidate>
          <div className="mb-5">
            <label htmlFor="new-folder-name" className="mb-1.5 block font-mono text-[11px] font-semibold text-zinc-400 uppercase tracking-wider">
              FOLDER NAME
            </label>
            <Input
              ref={inputRef}
              id="new-folder-name"
              type="text"
              placeholder="e.g. Production Assets"
              value={name}
              onChange={(e) => {
                setName(e.target.value)
                if (error) setError(null)
              }}
              disabled={loading}
              autoFocus
            />
          </div>

          <div className="flex items-center justify-end gap-2.5 pt-2 border-t border-arch-border">
            <Button
              type="button"
              variant="secondary"
              onClick={onClose}
              disabled={loading}
            >
              Cancel
            </Button>
            <Button type="submit" loading={loading}>
              Create Folder
            </Button>
          </div>
        </form>
      </div>
    </div>
  )
}

export default NewFolderModal
