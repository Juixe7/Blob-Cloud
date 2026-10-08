import { useEffect, useState } from 'react'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Alert } from './ui/Alert'
import { TrashIcon, LinkIcon, UsersIcon } from './icons'
import { getFileIcon } from '../lib/format'
import { FileIcon } from './FileIcon'

export interface DeleteModalTarget {
  id: string
  name: string
  is_directory: boolean
  mime_type?: string
  target_id?: string | null
}

interface DeleteModalProps {
  open: boolean
  onClose: () => void
  /** The item pending deletion. null = closed. */
  file: DeleteModalTarget | null
  /** Called after a successful operation — parent removes the item locally. */
  onDeleted: (itemId: string) => void
  /** If true, fires DELETE /api/files/{id}/permanent instead of soft delete. */
  isPermanent?: boolean
  /** If true, fires DELETE /api/shares/shared-with-me/{id} to unlink collaborator access. */
  isShared?: boolean
}

/**
 * Contextual destructive/removal action modal.
 * Supports:
 *  1. Move to Trash (soft delete)
 *  2. Delete Shortcut (unlinks personal pointer only)
 *  3. Remove from Shared with me (self-revokes recipient access)
 *  4. Delete Permanently (purges DB row and queued physical storage)
 */
export function DeleteModal({
  open,
  onClose,
  file,
  onDeleted,
  isPermanent = false,
  isShared = false,
}: DeleteModalProps) {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (open) {
      setError(null)
      setLoading(false)
    }
  }, [open, file?.id])

  if (!file) return null

  const isShortcut =
    file.mime_type === 'application/vnd.google-apps.shortcut' || Boolean(file.target_id)
  const kind = file.is_directory ? 'folder' : 'file'

  // Determine modal presentation based on context
  let titleText = `Move ${kind} to Trash?`
  let warningText = `"${file.name}" will be moved to Trash. Items in trash are automatically deleted after 30 days.`
  let confirmButtonText = 'Move to Trash'
  let confirmButtonStyle =
    'bg-slate-900 text-white hover:bg-slate-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-white'
  let IconComponent = TrashIcon
  let iconContainerStyle =
    'bg-slate-100 text-slate-700 border-slate-200 dark:bg-zinc-800 dark:text-zinc-300 dark:border-zinc-700'

  if (isPermanent) {
    titleText = `Delete ${kind} permanently?`
    warningText = `"${file.name}" and all associated data will be permanently purged. This action cannot be undone.`
    confirmButtonText = 'Delete permanently'
    confirmButtonStyle = 'bg-rose-600 text-white hover:bg-rose-700 focus-visible:ring-rose-500/50'
    IconComponent = TrashIcon
    iconContainerStyle =
      'bg-rose-50 text-rose-600 border-rose-200 dark:bg-rose-950/40 dark:text-rose-400 dark:border-rose-900/60'
  } else if (isShared) {
    titleText = 'Remove from Shared with me?'
    warningText = `You will lose access to "${file.name}". Other collaborators and the owner will still have access.`
    confirmButtonText = 'Remove'
    confirmButtonStyle =
      'bg-slate-900 text-white hover:bg-slate-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-white'
    IconComponent = UsersIcon
    iconContainerStyle =
      'bg-indigo-50 text-indigo-600 border-indigo-200 dark:bg-indigo-950/40 dark:text-indigo-400 dark:border-indigo-900/60'
  } else if (isShortcut) {
    titleText = 'Delete shortcut?'
    warningText = `"${file.name}" shortcut will be deleted from your Drive. The original file will not be affected.`
    confirmButtonText = 'Delete shortcut'
    confirmButtonStyle =
      'bg-slate-900 text-white hover:bg-slate-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-white'
    IconComponent = LinkIcon
    iconContainerStyle =
      'bg-sky-50 text-sky-600 border-sky-200 dark:bg-sky-950/40 dark:text-sky-400 dark:border-sky-900/60'
  }

  const handleConfirm = async () => {
    if (!file) return
    setLoading(true)
    setError(null)
    try {
      let endpoint = `/files/${file.id}`
      if (isPermanent) {
        endpoint = `/files/${file.id}/permanent`
      } else if (isShared) {
        endpoint = `/shares/shared-with-me/${file.id}`
      }

      await apiClient.delete(endpoint)
      onDeleted(file.id)
      onClose()
    } catch (err) {
      setError(extractError(err, 'Failed to complete action. Please try again.'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      label={titleText}
      maxWidthClass="max-w-md"
      locked={loading}
    >
      <div className="flex items-start gap-3.5 mb-4">
        <span
          className={`flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl border ${iconContainerStyle}`}
        >
          <IconComponent size={18} />
        </span>
        <div className="min-w-0 flex-1 pt-0.5">
          <h2 className="text-base font-semibold text-slate-900 dark:text-zinc-100">
            {titleText}
          </h2>
          <p className="mt-1 text-xs leading-relaxed text-slate-600 dark:text-zinc-400">
            {warningText}
          </p>
        </div>
      </div>

      {/* Item preview chip */}
      <div className="mb-5 flex items-center gap-2.5 rounded-lg border border-slate-200/80 bg-slate-50/80 px-3 py-2 text-xs text-slate-700 dark:border-zinc-800 dark:bg-zinc-900/50 dark:text-zinc-300">
        <FileIcon variant={getFileIcon(file.name, file.is_directory)} size={18} />
        <span className="truncate font-medium" title={file.name}>
          {file.name}
        </span>
        {isShortcut && (
          <span className="ml-auto rounded bg-sky-100 px-1.5 py-0.5 text-[10px] font-medium text-sky-700 dark:bg-sky-950/60 dark:text-sky-300">
            Shortcut
          </span>
        )}
      </div>

      {error && (
        <div className="mb-4">
          <Alert variant="error">{error}</Alert>
        </div>
      )}

      <div className="flex items-center justify-end gap-2.5 pt-1">
        <Button type="button" variant="secondary" onClick={onClose} disabled={loading}>
          Cancel
        </Button>
        <button
          type="button"
          onClick={handleConfirm}
          disabled={loading}
          className={`inline-flex items-center justify-center gap-1.5 rounded-lg px-4 py-2 text-xs font-semibold shadow-sm transition-all duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 dark:focus-visible:ring-offset-zinc-900 disabled:pointer-events-none disabled:opacity-50 ${confirmButtonStyle}`}
        >
          {loading && (
            <svg
              className="animate-spin"
              width={14}
              height={14}
              viewBox="0 0 24 24"
              fill="none"
              aria-hidden="true"
            >
              <circle
                className="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                strokeWidth="4"
              />
              <path
                className="opacity-90"
                fill="currentColor"
                d="M4 12a8 8 0 0 1 8-8V0C5.373 0 0 5.373 0 12h4z"
              />
            </svg>
          )}
          {confirmButtonText}
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
    if (status === 404) return 'Item not found — it may already have been removed.'
    if (status === 401) return 'Session expired. Please sign in again.'
    if (status === 403) return 'You do not have permission to perform this action.'
    return data?.error || data?.message || fallback
  }
  return 'An unexpected error occurred.'
}
