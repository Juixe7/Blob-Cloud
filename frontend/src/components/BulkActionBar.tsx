import { useState } from 'react'
import {
  FolderIcon,
  TrashIcon,
  DownloadIcon,
  RotateCcwIcon,
  XIcon,
} from './icons'
import { Spinner } from './ui/Spinner'

export interface BulkActionBarProps {
  selectedCount: number
  isTrash?: boolean
  disableWriteActions?: boolean
  onMove?: () => void
  onDelete?: () => void
  onRestore?: () => void
  onDeletePermanent?: () => void
  onDownload?: () => void
  onDeselect: () => void
}

/**
 * Desktop-class floating bulk action toolbar, anchored at bottom-center.
 * Automatically adapts options based on whether the user is in My Drive or Trash.
 */
export function BulkActionBar({
  selectedCount,
  isTrash = false,
  disableWriteActions = false,
  onMove,
  onDelete,
  onRestore,
  onDeletePermanent,
  onDownload,
  onDeselect,
}: BulkActionBarProps) {
  const [loadingAction, setLoadingAction] = useState<string | null>(null)

  if (selectedCount === 0) return null

  const handleAction = async (name: string, fn?: () => Promise<void> | void) => {
    if (!fn) return
    setLoadingAction(name)
    try {
      await fn()
    } finally {
      setLoadingAction(null)
    }
  }

  return (
    <div
      role="toolbar"
      aria-label="Bulk actions"
      className="fixed bottom-24 md:bottom-6 left-1/2 -translate-x-1/2 z-50 flex items-center gap-2.5 rounded border border-arch-border bg-arch-900/95 backdrop-blur-md px-4 py-2 shadow-sharp text-xs font-medium text-zinc-100 animate-fade-in select-none max-w-[calc(100vw-2rem)] overflow-x-auto scrollbar-hide"
    >
      {/* Selected badge */}
      <span className="font-mono text-[11px] font-semibold bg-amber-500/10 text-amber-400 border border-amber-500/30 rounded px-2.5 py-0.5">
        {selectedCount} selected
      </span>

      <div className="h-4 w-px bg-arch-border" />

      {!isTrash ? (
        <>
          {/* Move To */}
          {!disableWriteActions && onMove && (
            <button
              type="button"
              onClick={() => handleAction('move', onMove)}
              disabled={Boolean(loadingAction)}
              className="flex items-center gap-1.5 rounded px-2.5 py-1 transition-colors hover:bg-arch-850 hover:text-white text-zinc-300 disabled:opacity-50"
            >
              <FolderIcon size={14} />
              <span>Move</span>
            </button>
          )}

          {/* Download */}
          {onDownload && (
            <button
              type="button"
              onClick={() => handleAction('download', onDownload)}
              disabled={Boolean(loadingAction)}
              className="flex items-center gap-1.5 rounded px-2.5 py-1 transition-colors hover:bg-arch-850 hover:text-white text-zinc-300 disabled:opacity-50"
            >
              {loadingAction === 'download' ? (
                <Spinner size={14} className="text-amber-400" />
              ) : (
                <DownloadIcon size={14} />
              )}
              <span>Download</span>
            </button>
          )}

          {/* Delete (Soft) */}
          {!disableWriteActions && onDelete && (
            <button
              type="button"
              onClick={() => handleAction('delete', onDelete)}
              disabled={Boolean(loadingAction)}
              className="flex items-center gap-1.5 rounded px-2.5 py-1 text-rose-400 transition-colors hover:bg-rose-950/40 hover:text-rose-300 disabled:opacity-50"
            >
              {loadingAction === 'delete' ? (
                <Spinner size={14} className="text-rose-400" />
              ) : (
                <TrashIcon size={14} />
              )}
              <span>Delete</span>
            </button>
          )}
        </>
      ) : (
        <>
          {/* Restore */}
          {onRestore && (
            <button
              type="button"
              onClick={() => handleAction('restore', onRestore)}
              disabled={Boolean(loadingAction)}
              className="flex items-center gap-1.5 rounded px-2.5 py-1 transition-colors hover:bg-arch-850 hover:text-white text-zinc-300 disabled:opacity-50"
            >
              {loadingAction === 'restore' ? (
                <Spinner size={14} className="text-amber-400" />
              ) : (
                <RotateCcwIcon size={14} />
              )}
              <span>Restore</span>
            </button>
          )}

          {/* Delete Permanently */}
          {onDeletePermanent && (
            <button
              type="button"
              onClick={() => handleAction('deletePermanent', onDeletePermanent)}
              disabled={Boolean(loadingAction)}
              className="flex items-center gap-1.5 rounded px-2.5 py-1 text-rose-400 transition-colors hover:bg-rose-950/40 hover:text-rose-300 disabled:opacity-50"
            >
              {loadingAction === 'deletePermanent' ? (
                <Spinner size={14} className="text-rose-400" />
              ) : (
                <TrashIcon size={14} />
              )}
              <span>Delete Permanently</span>
            </button>
          )}
        </>
      )}

      <div className="h-4 w-px bg-arch-border" />

      {/* Deselect */}
      <button
        type="button"
        onClick={onDeselect}
        title="Clear selection"
        className="rounded p-1 text-zinc-400 transition-colors hover:bg-arch-850 hover:text-zinc-200"
      >
        <XIcon size={14} />
      </button>
    </div>
  )
}

export default BulkActionBar
