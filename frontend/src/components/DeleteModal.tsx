import { useEffect, useState } from 'react'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Alert } from './ui/Alert'
import { TrashIcon } from './icons'
import { getFileIcon } from '../lib/format'
import { FileIcon } from './FileIcon'

interface DeleteModalProps {
  open: boolean
  onClose: () => void
  /** The item pending deletion. null = closed. */
  file: { id: string; name: string; is_directory: boolean } | null
  /** Called after a successful DELETE — parent removes the item locally. */
  onDeleted: (itemId: string) => void
  /** If true, fires DELETE /api/files/{id}/permanent instead of soft delete. */
  isPermanent?: boolean
}

/**
 * Destructive-action confirmation. Asks the user to confirm before firing
 * DELETE /api/files/{id} or DELETE /api/files/{id}/permanent.
 */
export function DeleteModal({ open, onClose, file, onDeleted, isPermanent = false }: DeleteModalProps) {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (open) {
      setError(null)
      setLoading(false)
    }
  }, [open, file?.id])

  const handleConfirm = async () => {
    if (!file) return
    setLoading(true)
    setError(null)
    try {
      const endpoint = isPermanent ? `/files/${file.id}/permanent` : `/files/${file.id}`
      await apiClient.delete(endpoint)
      onDeleted(file.id)
      onClose()
    } catch (err) {
      setError(extractError(err, 'Failed to delete. Please try again.'))
    } finally {
      setLoading(false)
    }
  }

  if (!file) return null

  const kind = file.is_directory ? 'folder' : 'file'
  const titleText = isPermanent ? `Delete ${kind} permanently?` : `Move ${kind} to Trash?`
  const warningText = isPermanent
    ? 'This item and all its contents will be permanently purged from storage. This action CANNOT be undone.'
    : file.is_directory
      ? 'This folder and all its contents will be moved to Trash where they can be restored.'
      : 'This item will be moved to Trash where it can be restored.'

  return (
    <Modal
      open={open}
      onClose={onClose}
      label={titleText}
      maxWidthClass="max-w-md"
      locked={loading}
    >
      <div className="mb-5 flex items-start gap-4">
        <span className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-full border border-red-500/30 bg-red-500/10 text-red-400">
          <TrashIcon size={20} />
        </span>
        <div className="min-w-0">
          <h2 className="text-lg font-semibold text-slate-900 dark:text-zinc-50">{titleText}</h2>
          <p className="mt-1 text-sm text-slate-600 dark:text-zinc-400">{warningText}</p>
        </div>
      </div>

      {/* Item preview chip */}
      <div className="mb-5 flex items-center gap-3 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2.5 dark:border-zinc-800 dark:bg-zinc-950/40">
        <FileIcon variant={getFileIcon(file.name, file.is_directory)} size={20} />
        <span className="truncate text-sm text-slate-700 dark:text-zinc-200" title={file.name}>
          {file.name}
        </span>
      </div>

      {error && (
        <div className="mb-4">
          <Alert variant="error">{error}</Alert>
        </div>
      )}

      <div className="flex items-center justify-end gap-3">
        <Button type="button" variant="secondary" onClick={onClose} disabled={loading}>
          Cancel
        </Button>
        <button
          type="button"
          onClick={handleConfirm}
          disabled={loading}
          className="inline-flex items-center justify-center gap-2 rounded-lg bg-red-600 px-5 py-2.5 text-sm font-medium text-white transition-all duration-200 hover:bg-red-500 active:bg-red-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-red-500/60 focus-visible:ring-offset-2 focus-visible:ring-offset-zinc-950 disabled:pointer-events-none disabled:opacity-50"
        >
          {loading && (
            <svg className="animate-spin" width={16} height={16} viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
              <path className="opacity-90" fill="currentColor" d="M4 12a8 8 0 0 1 8-8V0C5.373 0 0 5.373 0 12h4z" />
            </svg>
          )}
          <TrashIcon size={16} />
          {isPermanent ? 'Delete Permanently' : 'Move to Trash'}
        </button>
      </div>
    </Modal>
  )
}

export default DeleteModal

function extractError(err: unknown, fallback: string): string {
  if (axios.isAxiosError(err)) {
    const status = err.response?.status
    const data = err.response?.data as { error?: string; message?: string } | undefined
    if (status === 404) return 'Item not found — it may already be deleted.'
    if (status === 401) return 'Session expired. Please sign in again.'
    return data?.error || data?.message || fallback
  }
  return 'An unexpected error occurred.'
}
