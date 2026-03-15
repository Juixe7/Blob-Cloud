import { useCallback, useEffect, useState } from 'react'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Alert } from './ui/Alert'
import { Spinner } from './ui/Spinner'
import { FolderIcon, ChevronRightIcon, ChevronLeftIcon, HomeIcon } from './icons'
import { cn } from '../lib/format'
import type { FileItem, BreadcrumbNode } from '../types/file'

interface ShortcutModalProps {
  open: boolean
  onClose: () => void
  /** The single item being shortcutted. */
  file?: FileItem | null
  /** Called after a successful shortcut creation. */
  onCreated: () => void
}

/**
 * "Create Shortcut" navigation modal.
 * Lets the user pick any folder destination in their Drive to insert a shortcut reference.
 */
export function ShortcutModal({ open, onClose, file, onCreated }: ShortcutModalProps) {
  // Navigation state local to the modal.
  const [path, setPath] = useState<BreadcrumbNode[]>([{ id: null, name: 'My Drive' }])
  const [folders, setFolders] = useState<FileItem[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [success, setSuccess] = useState(false)

  const currentFolderId = path[path.length - 1].id

  /** Fetch only the directories under a given parent. */
  const loadFolders = useCallback(async (parentId: string | null) => {
    setLoading(true)
    setError(null)
    try {
      const url = parentId ? `/files?parent_id=${parentId}` : '/files'
      const res = await apiClient.get<FileItem[]>(url)
      const dirs = (res.data ?? []).filter((f) => f.is_directory)
      // Alphabetical, deterministic order.
      dirs.sort((a, b) => a.name.localeCompare(b.name))
      setFolders(dirs)
    } catch (err) {
      setError(extractError(err, 'Failed to load folders.'))
      setFolders([])
    } finally {
      setLoading(false)
    }
  }, [])

  // Load root whenever the modal (re)opens; reset state on close.
  useEffect(() => {
    if (open) {
      setPath([{ id: null, name: 'My Drive' }])
      setError(null)
      setSuccess(false)
      void loadFolders(null)
    } else {
      // Tear down between sessions.
      setFolders([])
      setError(null)
      setSuccess(false)
    }
  }, [open, loadFolders])

  /** Navigate into a subfolder (updates the breadcrumb path + loads contents). */
  const enterFolder = useCallback(
    (folder: FileItem) => {
      setPath((prev) => [...prev, { id: folder.id, name: folder.name }])
      void loadFolders(folder.id)
    },
    [loadFolders],
  )

  /** Navigate to a breadcrumb at the given index. */
  const navigateTo = useCallback(
    (index: number) => {
      setPath((prev) => prev.slice(0, index + 1))
      void loadFolders(path[index].id)
    },
    [loadFolders, path],
  )

  /** Go one level up. */
  const goUp = useCallback(() => {
    if (path.length <= 1) return
    const next = path.slice(0, -1)
    setPath(next)
    void loadFolders(next[next.length - 1].id)
  }, [loadFolders, path])

  /** Commit the shortcut creation. */
  const handleCreateShortcut = useCallback(async () => {
    if (!file) return
    const targetId = currentFolderId

    setCreating(true)
    setError(null)
    try {
      await apiClient.post('/files/shortcut', {
        file_id: file.id,
        parent_id: targetId,
      })
      onCreated()
      setSuccess(true)
      setTimeout(onClose, 700)
    } catch (err) {
      setError(extractError(err, 'Failed to create shortcut. Please try again.'))
    } finally {
      setCreating(false)
    }
  }, [file, currentFolderId, onCreated, onClose])

  if (!file) return null

  const destinationLabel = path[path.length - 1].name
  const headerLabel = `Create Shortcut for “${file.name}”`

  return (
    <Modal
      open={open}
      onClose={onClose}
      label={headerLabel}
      maxWidthClass="max-w-lg"
      position="top"
      locked={creating}
    >
      {/* Header */}
      <div className="mb-4">
        <h2 className="text-lg font-semibold text-zinc-50">{headerLabel}</h2>
        <p className="mt-0.5 text-sm text-zinc-500">
          Choose a destination folder for this shortcut.
        </p>
      </div>

      {/* In-modal breadcrumb / location bar */}
      <div className="mb-3 flex items-center gap-1 overflow-x-auto rounded-lg border border-zinc-800 bg-zinc-950/40 px-2 py-1.5 text-sm">
        <button
          type="button"
          onClick={goUp}
          disabled={path.length <= 1 || loading}
          aria-label="Go up one folder"
          className="flex-shrink-0 rounded p-1 text-zinc-400 transition-colors hover:bg-zinc-800 hover:text-zinc-200 disabled:opacity-40 disabled:hover:bg-transparent"
        >
          <ChevronLeftIcon size={16} />
        </button>
        <span className="flex-shrink-0 text-zinc-600">|</span>
        <nav aria-label="Destination path" className="flex min-w-0 items-center gap-0.5">
          {path.map((node, i) => {
            const isLast = i === path.length - 1
            return (
              <span key={node.id ?? 'root'} className="flex flex-shrink-0 items-center">
                {i === 0 && (
                  <HomeIcon size={14} className="mr-1 text-zinc-500" />
                )}
                <button
                  type="button"
                  onClick={() => navigateTo(i)}
                  disabled={isLast || loading}
                  className={cn(
                    'max-w-[160px] truncate rounded px-1.5 py-0.5 transition-colors',
                    isLast
                      ? 'font-medium text-zinc-100'
                      : 'text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200',
                  )}
                  title={node.name}
                >
                  {node.name}
                </button>
                {!isLast && <ChevronRightIcon size={14} className="mx-0.5 text-zinc-600" />}
              </span>
            )
          })}
        </nav>
      </div>

      {/* Folders List container */}
      <div className="relative min-h-[160px] max-h-[240px] overflow-y-auto rounded-xl border border-zinc-800 bg-zinc-950/20 px-1 py-1">
        {loading ? (
          <div className="absolute inset-0 flex items-center justify-center">
            <Spinner size={24} className="text-violet-500" />
          </div>
        ) : error ? (
          <div className="absolute inset-0 flex items-center justify-center p-4">
            <Alert variant="error">
              {error}
            </Alert>
          </div>
        ) : folders.length === 0 ? (
          <div className="absolute inset-0 flex flex-col items-center justify-center text-center p-4 text-zinc-500">
            <FolderIcon size={28} className="text-zinc-700 mb-1" />
            <p className="text-xs font-medium">Empty Folder</p>
          </div>
        ) : (
          <div className="space-y-0.5">
            {folders.map((f) => (
              <button
                key={f.id}
                type="button"
                onClick={() => enterFolder(f)}
                className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm text-zinc-300 transition-colors hover:bg-zinc-900/60 hover:text-zinc-50"
              >
                <FolderIcon size={16} className="text-violet-500/80 flex-shrink-0" />
                <span className="truncate">{f.name}</span>
                <ChevronRightIcon size={14} className="ml-auto text-zinc-600 flex-shrink-0" />
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Footer controls */}
      <div className="mt-5 flex items-center justify-between gap-4 border-t border-zinc-800/80 pt-3">
        <span className="text-xs text-zinc-500 truncate max-w-[200px]" title={destinationLabel}>
          Adding shortcut to: <strong className="text-zinc-300 font-semibold">{destinationLabel}</strong>
        </span>

        <div className="flex gap-2.5">
          <Button variant="secondary" onClick={onClose} disabled={creating || success}>
            Cancel
          </Button>
          <Button
            variant="primary"
            onClick={() => void handleCreateShortcut()}
            loading={creating}
            disabled={success || loading}
          >
            Create Shortcut
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function extractError(err: unknown, fallback: string): string {
  if (axios.isAxiosError(err)) {
    const data = err.response?.data as Record<string, unknown> | undefined
    if (data && typeof data.error === 'string') {
      return data.error
    }
  }
  return fallback
}

export default ShortcutModal
