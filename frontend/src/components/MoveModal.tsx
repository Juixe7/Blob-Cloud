import { useCallback, useEffect, useState } from 'react'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Alert } from './ui/Alert'
import { Spinner } from './ui/Spinner'
import { FolderIcon, ChevronRightIcon, ChevronLeftIcon, HomeIcon } from './icons'
import { getFileIcon, cn } from '../lib/format'
import { FileIcon } from './FileIcon'
import type { FileItem, BreadcrumbNode } from '../types/file'

interface MoveItem {
  id: string
  name: string
  parent_id: string | null
}

interface MoveModalProps {
  open: boolean
  onClose: () => void
  /** The single item being moved (or null). */
  file?: MoveItem | null
  /** Bulk items being moved (or null). */
  files?: MoveItem[] | null
  /** Called after a successful move. */
  onMoved: (itemIds: string[], newParentId: string | null) => void
}

/**
 * "Move To" navigation modal.
 * Supports both single-item and bulk-item move operations.
 */
export function MoveModal({ open, onClose, file, files, onMoved }: MoveModalProps) {
  const itemsToMove: MoveItem[] = files && files.length > 0 ? files : file ? [file] : []
  const firstItem = itemsToMove[0] ?? null

  // Navigation state local to the modal.
  const [path, setPath] = useState<BreadcrumbNode[]>([{ id: null, name: 'My Drive' }])
  const [folders, setFolders] = useState<FileItem[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [moving, setMoving] = useState(false)
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
      // Guard: never move into the item being moved (would create an orphan
      // cycle if it's a directory).
      if (folder.id === file?.id) return
      setPath((prev) => [...prev, { id: folder.id, name: folder.name }])
      void loadFolders(folder.id)
    },
    [file?.id, loadFolders],
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

  /** Commit the move: PATCH or POST bulk move to selected destination. */
  const handleMove = useCallback(async () => {
    if (itemsToMove.length === 0) return
    const targetId = currentFolderId

    setMoving(true)
    setError(null)
    try {
      if (itemsToMove.length === 1) {
        await apiClient.patch(`/files/${itemsToMove[0].id}`, { parent_id: targetId })
      } else {
        await apiClient.post('/files/bulk/move', {
          ids: itemsToMove.map((it) => it.id),
          parent_id: targetId,
        })
      }
      onMoved(
        itemsToMove.map((it) => it.id),
        targetId,
      )
      setSuccess(true)
      setTimeout(onClose, 700)
    } catch (err) {
      setError(extractError(err, 'Failed to move item(s). Please try again.'))
    } finally {
      setMoving(false)
    }
  }, [itemsToMove, currentFolderId, onMoved, onClose])

  if (itemsToMove.length === 0) return null

  const isSameLocation = firstItem ? firstItem.parent_id === currentFolderId : false
  const destinationLabel = path[path.length - 1].name
  const headerLabel =
    itemsToMove.length === 1 ? `Move “${firstItem?.name}”` : `Move ${itemsToMove.length} items`

  return (
    <Modal
      open={open}
      onClose={onClose}
      label={headerLabel}
      maxWidthClass="max-w-lg"
      position="top"
      locked={moving}
    >
      {/* Header */}
      <div className="mb-4">
        <h2 className="text-lg font-semibold text-zinc-50">{headerLabel}</h2>
        <p className="mt-0.5 text-sm text-zinc-500">
          Choose a destination folder.
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

      {/* Folder list (directories only) */}
      <div className="min-h-[200px] max-h-[320px] overflow-y-auto rounded-lg border border-zinc-800 bg-zinc-950/30">
        {loading ? (
          <div className="flex h-[200px] items-center justify-center">
            <Spinner size={18} className="text-violet-500" />
          </div>
        ) : folders.length === 0 ? (
          <div className="flex h-[200px] flex-col items-center justify-center gap-2 px-4 text-center">
            <FolderIcon size={28} className="text-zinc-700" />
            <p className="text-sm text-zinc-500">No subfolders here</p>
            <p className="text-xs text-zinc-600">
              You can move the item into “{destinationLabel}”.
            </p>
          </div>
        ) : (
          <ul className="p-1.5">
            {folders.map((f) => {
              // Don't list items being moved as candidate destinations.
              if (itemsToMove.some((it) => it.id === f.id)) return null
              return (
                <li key={f.id}>
                  <button
                    type="button"
                    onClick={() => enterFolder(f)}
                    disabled={moving}
                    className="flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors hover:bg-zinc-800/60 focus-visible:bg-zinc-800/60 focus:outline-none"
                  >
                    <FileIcon variant={getFileIcon(f.name, true)} size={18} />
                    <span className="flex-1 truncate text-sm text-zinc-200">{f.name}</span>
                    <ChevronRightIcon size={14} className="text-zinc-600" />
                  </button>
                </li>
              )
            })}
          </ul>
        )}
      </div>

      {/* Status / errors */}
      {error && (
        <div className="mt-3">
          <Alert variant="error">{error}</Alert>
        </div>
      )}
      {success && (
        <div className="mt-3">
          <Alert variant="success">Moved to “{destinationLabel}”.</Alert>
        </div>
      )}

      {/* Footer actions */}
      <div className="mt-5 flex items-center justify-between gap-3">
        <p className="text-xs text-zinc-600">
          {isSameLocation ? 'Currently in this location' : `Destination: ${destinationLabel}`}
        </p>
        <div className="flex items-center gap-3">
          <Button variant="secondary" onClick={onClose} disabled={moving}>
            Cancel
          </Button>
          <Button onClick={handleMove} loading={moving}>
            Move Here
          </Button>
        </div>
      </div>
    </Modal>
  )
}

export default MoveModal

function extractError(err: unknown, fallback: string): string {
  if (axios.isAxiosError(err)) {
    const status = err.response?.status
    const data = err.response?.data as { error?: string; message?: string } | undefined
    if (status === 404) return 'Item or destination not found.'
    if (status === 401) return 'Session expired. Please sign in again.'
    return data?.error || data?.message || fallback
  }
  return 'An unexpected error occurred.'
}
