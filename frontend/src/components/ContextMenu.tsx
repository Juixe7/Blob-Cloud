import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { cn } from '../lib/format'
import {
  ShareIcon,
  PencilIcon,
  FolderInputIcon,
  DownloadIcon,
  TrashIcon,
} from './icons'
import type { FileItem } from '../types/file'

/**
 * The set of actions a context menu can surface. Each is wired by the parent
 * (Dashboard) to the corresponding modal / handler.
 */
export interface ContextMenuActions {
  onPreview?: (item: FileItem) => void
  onShare: (item: FileItem) => void
  onRename: (item: FileItem) => void
  onMove: (item: FileItem) => void
  onDownload: (item: FileItem) => void
  onDelete: (item: FileItem) => void
  onRestore?: (item: FileItem) => void
  onPermanentDelete?: (item: FileItem) => void
  onCreateShortcut?: (item: FileItem) => void
  onSharePublic?: (item: FileItem) => void
  onVersionHistory?: (item: FileItem) => void
  onGetInfo?: (item: FileItem) => void
}

interface ContextMenuProps {
  /** The item the menu was opened against. null = closed. */
  item: FileItem | null
  /** Anchor coordinates (clientX / clientY from the contextmenu event). */
  position: { x: number; y: number } | null
  /** Close handler — invoked on outside click, Escape, or item selection. */
  onClose: () => void
  /** Action callbacks. */
  actions: ContextMenuActions
  /** Whether menu is rendered inside Trash view. */
  isTrash?: boolean
  /** Resolved role of the current user on this item. */
  role?: 'OWNER' | 'EDITOR' | 'VIEWER'
}

/**
 * Lightweight, accessible right-click context menu.
 */
export function ContextMenu({ item, position, onClose, actions, isTrash = false, role = 'OWNER' }: ContextMenuProps) {
  const menuRef = useRef<HTMLDivElement>(null)
  // Measured size used to clamp position within the viewport after first paint.
  const [size, setSize] = useState<{ w: number; h: number }>({ w: 0, h: 0 })

  // Measure the menu once it's mounted so we can clamp its position.
  useLayoutEffect(() => {
    if (!item || !position) return
    const el = menuRef.current
    if (!el) return
    setSize({ w: el.offsetWidth, h: el.offsetHeight })
  }, [item, position])

  // Global dismiss handlers: outside click + Escape + viewport changes.
  useEffect(() => {
    if (!item || !position) return

    function onPointerDown(e: MouseEvent) {
      if (menuRef.current?.contains(e.target as Node)) return
      onClose()
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    function onViewportChange() {
      onClose()
    }

    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKey)
    window.addEventListener('resize', onViewportChange)
    window.addEventListener('scroll', onViewportChange, true)

    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('resize', onViewportChange)
      window.removeEventListener('scroll', onViewportChange, true)
    }
  }, [item, position, onClose])

  if (!item || !position) return null

  // Clamp within the viewport, leaving an 8px margin from each edge.
  const MARGIN = 8
  const x = Math.min(position.x, window.innerWidth - size.w - MARGIN)
  const y = Math.min(position.y, window.innerHeight - size.h - MARGIN)
  const left = Math.max(MARGIN, x)
  const top = Math.max(MARGIN, y)

  const isDirectory = item.is_directory

  const run = (fn?: (i: FileItem) => void) => () => {
    if (fn) fn(item)
    onClose()
  }

  if (isTrash) {
    return (
      <div
        ref={menuRef}
        role="menu"
        aria-label={`Trash actions for ${item.name}`}
        className={cn(
          'fixed z-50 w-48 rounded border border-arch-border bg-arch-950 p-1 shadow-sharp text-xs select-none',
          'animate-[menu-in_100ms_ease-out]',
        )}
        style={{ left, top }}
      >
        <MenuItem
          icon={
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="1 4 1 10 7 10" />
              <path d="M3.51 15a9 9 0 102.13-9.36L1 10" />
            </svg>
          }
          label="Restore"
          onClick={run(actions.onRestore)}
        />
        <div className="my-1 h-px bg-arch-border" role="separator" />
        <MenuItem
          icon={<TrashIcon size={14} />}
          label="Delete Permanently"
          onClick={run(actions.onPermanentDelete || actions.onDelete)}
          danger
        />
      </div>
    )
  }

  const isOwnerOrEditor = role === 'OWNER' || role === 'EDITOR'
  const isQuarantined = item.status === 'QUARANTINED'

  return (
    <div
      ref={menuRef}
      role="menu"
      aria-label={`Actions for ${item.name}`}
      className={cn(
        'fixed z-50 w-48 rounded border border-arch-border bg-arch-950 p-1 shadow-sharp text-xs select-none',
        'animate-[menu-in_100ms_ease-out]',
      )}
      style={{ left, top }}
    >
      {!isDirectory && actions.onPreview && !isQuarantined && (
        <MenuItem
          icon={
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75">
              <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
              <circle cx="12" cy="12" r="3" />
            </svg>
          }
          label="Preview"
          onClick={run(actions.onPreview)}
        />
      )}
      {isOwnerOrEditor && !isQuarantined && (
        <MenuItem icon={<ShareIcon size={14} />} label="Share" onClick={run(actions.onShare)} />
      )}
      {isOwnerOrEditor && actions.onSharePublic && !isQuarantined && (
        <MenuItem
          icon={
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75">
              <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
              <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
            </svg>
          }
          label="Public Link"
          onClick={run(actions.onSharePublic)}
        />
      )}
      {actions.onGetInfo && !isQuarantined && (
        <MenuItem
          icon={
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75">
              <circle cx="12" cy="12" r="10" />
              <line x1="12" y1="16" x2="12" y2="12" />
              <line x1="12" y1="8" x2="12.01" y2="8" />
            </svg>
          }
          label="Get Info (AI)"
          onClick={run(actions.onGetInfo)}
        />
      )}
      {isOwnerOrEditor && !isQuarantined && (
        <MenuItem icon={<PencilIcon size={14} />} label="Rename" onClick={run(actions.onRename)} />
      )}
      {isOwnerOrEditor && !isQuarantined && (
        <MenuItem icon={<FolderInputIcon size={14} />} label="Move To" onClick={run(actions.onMove)} />
      )}
      {!isDirectory && actions.onVersionHistory && !isQuarantined && (
        <MenuItem
          icon={
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75">
              <path d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          }
          label="Version History"
          onClick={run(actions.onVersionHistory)}
        />
      )}
      {!isQuarantined && <MenuItem icon={<DownloadIcon size={14} />} label="Download" onClick={run(actions.onDownload)} />}
      {actions.onCreateShortcut && !isQuarantined && (
        <MenuItem
          icon={
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
              <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
              <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
            </svg>
          }
          label="Create Shortcut"
          onClick={run(actions.onCreateShortcut)}
        />
      )}
      {isOwnerOrEditor && (
        <>
          <div className="my-1 h-px bg-arch-border" role="separator" />
          <MenuItem
            icon={<TrashIcon size={14} />}
            label="Delete"
            onClick={run(actions.onDelete)}
            danger
          />
        </>
      )}
    </div>
  )
}

interface MenuItemProps {
  icon: ReactNode
  label: string
  onClick: () => void
  danger?: boolean
}

function MenuItem({ icon, label, onClick, danger = false }: MenuItemProps) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onClick}
      className={cn(
        'flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left font-sans text-xs font-medium transition-colors',
        'focus:outline-none focus-visible:bg-arch-850',
        danger
          ? 'text-rose-400 hover:bg-rose-950/40 hover:text-rose-300'
          : 'text-zinc-300 hover:bg-arch-850 hover:text-white',
      )}
    >
      <span className={cn('flex-shrink-0', danger ? 'text-rose-400' : 'text-zinc-400')}>{icon}</span>
      <span>{label}</span>
    </button>
  )
}

export default ContextMenu
