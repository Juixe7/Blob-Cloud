import { useEffect, useRef, useState, type FormEvent } from 'react'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Input } from './ui/Input'
import { Alert } from './ui/Alert'

interface RenameModalProps {
  open: boolean
  onClose: () => void
  /** The item being renamed. null = closed. */
  file: { id: string; name: string; is_directory: boolean } | null
  /** Called with the new name after a successful PATCH. */
  onRenamed: (itemId: string, newName: string) => void
}

/** Characters we block from filenames. Matches typical FS hygiene. */
const INVALID_CHARS = /[\\/:*?"<>|]/

/**
 * Rename dialog. Prompts for a new name and PATCHes /api/files/{id}.
 *
 * - Pre-fills the input with the current name and, for files, selects just the
 *   stem (excluding the extension) so renaming feels natural.
 * - Validates locally (non-empty, length, forbidden characters) before
 *   round-tripping.
 * - On success, calls onRenamed so the parent can patch the local item list.
 */
export function RenameModal({ open, onClose, file, onRenamed }: RenameModalProps) {
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  // Seed the input when the target changes; pre-select the filename stem.
  useEffect(() => {
    if (!open || !file) return
    setError(null)
    setLoading(false)
    setName(file.name)

    const t = requestAnimationFrame(() => {
      const input = inputRef.current
      if (!input) return
      // For files, select only the name without the extension.
      if (!file.is_directory && file.name.includes('.')) {
        const dot = file.name.lastIndexOf('.')
        input.setSelectionRange(0, dot)
      } else {
        input.select()
      }
      input.focus()
    })
    return () => cancelAnimationFrame(t)
  }, [open, file])

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!file) return

    const trimmed = name.trim()
    if (!trimmed) {
      setError('Name cannot be empty.')
      return
    }
    if (trimmed.length > 255) {
      setError('Name must be 255 characters or fewer.')
      return
    }
    if (INVALID_CHARS.test(trimmed)) {
      setError('Name cannot contain: \\ / : * ? " < > |')
      return
    }
    // No-op rename — just close.
    if (trimmed === file.name) {
      onClose()
      return
    }

    setLoading(true)
    setError(null)

    try {
      await apiClient.patch(`/files/${file.id}`, { name: trimmed })
      onRenamed(file.id, trimmed)
      onClose()
    } catch (err) {
      setError(extractError(err, 'Failed to rename. Please try again.'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      label={`Rename ${file?.name ?? 'item'}`}
      maxWidthClass="max-w-md"
      locked={loading}
    >
      <h2 className="mb-1 text-lg font-semibold text-zinc-50">
        Rename {file?.is_directory ? 'folder' : 'file'}
      </h2>
      <p className="mb-5 text-sm text-zinc-500">
        Enter a new name for “{file?.name ?? ''}”.
      </p>

      {error && (
        <div className="mb-4">
          <Alert variant="error">{error}</Alert>
        </div>
      )}

      <form onSubmit={handleSubmit} noValidate>
        <label htmlFor="rename-input" className="mb-1.5 block text-sm font-medium text-zinc-300">
          Name
        </label>
        <Input
          ref={inputRef}
          id="rename-input"
          type="text"
          value={name}
          onChange={(e) => {
            setName(e.target.value)
            if (error) setError(null)
          }}
          disabled={loading}
          autoComplete="off"
          spellCheck={false}
        />

        <div className="mt-6 flex items-center justify-end gap-3">
          <Button type="button" variant="secondary" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button type="submit" loading={loading}>
            Save
          </Button>
        </div>
      </form>
    </Modal>
  )
}

export default RenameModal

function extractError(err: unknown, fallback: string): string {
  if (axios.isAxiosError(err)) {
    const status = err.response?.status
    const data = err.response?.data as { error?: string; message?: string } | undefined
    if (status === 409) return 'An item with that name already exists here.'
    if (status === 404) return 'Item not found.'
    if (status === 401) return 'Session expired. Please sign in again.'
    return data?.error || data?.message || fallback
  }
  return 'An unexpected error occurred.'
}
