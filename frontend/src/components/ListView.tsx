import React, { type MouseEvent, useRef, useState } from 'react'
import { List } from 'react-window'
import { getAccessToken } from '../lib/token'
import { apiClient } from '../lib/api'
import type { FileItem } from '../types/file'
import { formatFileSize, formatDate, cn } from '../lib/format'
import { FileIcon } from './FileIcon'
import { useResizeObserver } from '../hooks/useResizeObserver'

interface ListViewProps {
  items: FileItem[]
  selectedIds?: Set<string>
  isTrash?: boolean
  isShared?: boolean
  onToggleSelect?: (id: string) => void
  onSingleSelect?: (id: string) => void
  onSelectRange?: (id: string) => void
  onSelectAll?: () => void
  onOpenFolder: (item: FileItem) => void
  onOpenFile?: (item: FileItem) => void
  onContextMenu: (item: FileItem, e: MouseEvent) => void
}


type RowPropsType = {
  items: FileItem[]
  selectedIds?: Set<string>
  isTrash?: boolean
  isShared?: boolean
  onToggleSelect?: (id: string) => void
  onSingleSelect?: (id: string) => void
  onSelectRange?: (id: string) => void
  onOpenFolder: (folder: FileItem) => void
  onOpenFile?: (file: FileItem) => void
  onContextMenu: (item: FileItem, e: MouseEvent) => void
}

type RowComponentProps = {
  index: number
  style: React.CSSProperties
} & RowPropsType

const Row = React.memo(({ index, style, items, selectedIds, isTrash, isShared, onToggleSelect, onSingleSelect, onSelectRange, onOpenFolder, onOpenFile, onContextMenu }: RowComponentProps) => {
  const item = items[index]
  const isSelected = selectedIds?.has(item.id) ?? false
  const isShortcut = item.mime_type === 'application/vnd.google-apps.shortcut'
  const isBrokenShortcut = isShortcut && item.shortcut_target_id === null
  const [imgError, setImgError] = useState(false)
  
  const token = getAccessToken() ?? ''
  const base = apiClient.defaults.baseURL ?? '/api'
  const thumbUrl = item.thumbnail_url || `${base}/files/${item.id}/thumbnail?token=${encodeURIComponent(token)}`
  const isImage = item.mime_type?.startsWith('image/') || false
  const showThumbnail = isImage && !imgError
  
  return (
    <div
      style={style}
      onClick={(e) => {
        e.stopPropagation()
        if (e.detail === 2) {
          if (isBrokenShortcut) return
          if (item.is_directory) {
            onOpenFolder(item)
            return
          } else if (onOpenFile) {
            onOpenFile(item)
            return
          }
        }
        if (e.shiftKey && onSelectRange) {
          onSelectRange(item.id)
        } else if ((e.ctrlKey || e.metaKey) && onToggleSelect) {
          onToggleSelect(item.id)
        } else if (onSingleSelect) {
          onSingleSelect(item.id)
        }
      }}
      onDoubleClick={() => {
        if (isBrokenShortcut) return
        if (item.is_directory) {
          onOpenFolder(item)
        } else if (onOpenFile) {
          onOpenFile(item)
        }
      }}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          if (isBrokenShortcut) return
          if (item.is_directory) onOpenFolder(item)
          else if (onOpenFile) onOpenFile(item)
        }
      }}
      onContextMenu={(e) => {
        if (onToggleSelect && !isSelected) {
          onToggleSelect(item.id)
        }
        onContextMenu(item, e)
      }}
      tabIndex={0}
      className={cn(
        'flex items-center text-[11px] group cursor-default transition-colors duration-150 border-b border-arch-border/50 focus:outline-none focus:bg-amber-500/5',
        isSelected ? 'bg-amber-500/10 border-l-2 border-l-amber-500 border-b-transparent' : 'hover:bg-arch-850/60',
        item.status === 'QUARANTINED' && 'bg-[repeating-linear-gradient(45deg,transparent,transparent_10px,rgba(220,38,38,0.1)_10px,rgba(220,38,38,0.1)_20px)]'
      )}
      title={item.status === 'QUARANTINED' ? 'Malware detected. This file is locked in quarantine.' : undefined}
    >
      <div className="pl-4 pr-1 py-2 w-10 shrink-0 flex items-center" onClick={(e) => e.stopPropagation()}>
        <input
          type="checkbox"
          checked={isSelected}
          onChange={() => onToggleSelect?.(item.id)}
          className="form-checkbox h-3.5 w-3.5 rounded-xs border-arch-700 bg-arch-950 text-amber-500 focus:ring-amber-500/40 cursor-pointer"
        />
      </div>
      
      <div className="flex-1 min-w-0 pr-4 py-1.5 flex items-center gap-3">
        <div
          className={cn(
            "shrink-0 flex items-center relative",
            item.is_directory && !isTrash && "cursor-pointer"
          )}
          onClick={(e) => {
            if (item.is_directory && !isTrash) {
              e.stopPropagation()
              onOpenFolder(item)
            }
          }}
        >
          {showThumbnail ? (
            <img 
              src={thumbUrl} 
              alt={item.name} 
              className={cn("h-6 w-6 md:h-4 md:w-4 object-cover rounded-[2px]", item.status === 'QUARANTINED' && 'grayscale opacity-50')} 
              onError={() => setImgError(true)}
            />
          ) : (
            <div className={cn(item.status === 'QUARANTINED' && 'grayscale opacity-50')}>
              <FileIcon filename={item.name} isDirectory={item.is_directory} size={20} isShortcut={isShortcut} isBrokenShortcut={isBrokenShortcut} />
            </div>
          )}
          {item.status === 'QUARANTINED' && (
            <div className="absolute -top-1 -right-1 bg-red-500 rounded-full w-3 h-3 flex items-center justify-center shadow-[0_0_5px_rgba(239,68,68,0.5)]">
              <svg xmlns="http://www.w3.org/2000/svg" width="8" height="8" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                <path d="M12 2v20M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" />
              </svg>
            </div>
          )}
        </div>
        <div className="flex flex-col min-w-0">
          <span
            className={cn(
              'truncate font-medium text-xs md:text-[11px]',
              isTrash ? 'text-zinc-500 line-through' : 'text-zinc-200',
              item.is_directory && !isTrash && 'hover:text-amber-400 hover:underline cursor-pointer'
            )}
            onClick={(e) => {
              if (item.is_directory && !isTrash) {
                e.stopPropagation()
                onOpenFolder(item)
              }
            }}
          >
            {item.name}
          </span>
          <span className="md:hidden truncate text-zinc-500 font-mono text-[9px] mt-0.5">
            {isTrash ? formatDate(item.deleted_at || '') : isShared ? 'Unknown' : formatDate(item.updated_at || item.created_at)}
            {!item.is_directory && ` • ${formatFileSize(item.size_bytes)}`}
            {isTrash && item.is_directory && ` • ${item.item_count ?? 0} items`}
          </span>
        </div>
      </div>

      {isTrash && (
        <div className="hidden md:block w-[20%] px-4 py-2 truncate text-zinc-400">
          {item.original_location || 'My Drive'}
        </div>
      )}
      
      <div className="hidden md:block w-[20%] px-4 py-2 truncate text-zinc-400 font-mono text-[10px]">
        {isTrash ? formatDate(item.deleted_at || '') : isShared ? 'Unknown' : formatDate(item.updated_at || item.created_at)}
      </div>
      
      {isTrash && (
        <div className="hidden lg:block w-[10%] px-4 py-2 truncate text-zinc-400">
          {item.is_directory ? `${item.item_count ?? 0} items` : '--'}
        </div>
      )}
      
      <div className="hidden sm:block w-[15%] px-4 py-2 truncate text-zinc-400 font-mono text-[10px]">
        {!item.is_directory ? formatFileSize(item.size_bytes) : isTrash ? formatFileSize(item.aggregate_size ?? 0) : '--'}
      </div>
    </div>
  )
})

export function ListView({
  items,
  selectedIds,
  isTrash = false,
  isShared = false,
  onToggleSelect,
  onSingleSelect,
  onSelectRange,
  onSelectAll,
  onOpenFolder,
  onOpenFile,
  onContextMenu,
}: ListViewProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const { height } = useResizeObserver(containerRef)
  
  const allSelected = items.length > 0 && items.every((it) => selectedIds?.has(it.id))

  if (items.length === 0) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center text-zinc-500 h-full">
        <p>No items in this view.</p>
      </div>
    )
  }

  return (
    <div className="flex-1 flex flex-col min-h-0 select-none">
      {/* Header Row */}
      <div className="hidden md:flex items-center text-left font-mono text-[9px] font-semibold uppercase tracking-[0.15em] text-zinc-500 border-b border-arch-border bg-arch-950 pr-[14px]">
        <div className="pl-4 pr-1 py-2.5 w-10 shrink-0 flex items-center">
          {(selectedIds?.size ?? 0) > 0 && (
            <input
              type="checkbox"
              checked={allSelected}
              onChange={onSelectAll}
              className="form-checkbox h-3.5 w-3.5 rounded-xs border-arch-700 bg-arch-950 text-amber-500 focus:ring-amber-500/40 cursor-pointer"
            />
          )}
        </div>
        <div className="flex-1 min-w-0 pr-4 py-2.5">NAME</div>
        {isTrash && <div className="hidden md:block w-[20%] px-4 py-2.5">ORIGINAL LOCATION</div>}
        <div className="hidden md:block w-[20%] px-4 py-2.5">{isTrash ? 'DATE DELETED' : isShared ? 'DATE SHARED' : 'DATE MODIFIED'}</div>
        {isTrash && <div className="hidden lg:block w-[10%] px-4 py-2.5">ITEMS</div>}
        <div className="hidden sm:block w-[15%] px-4 py-2.5">SIZE</div>
      </div>
      
      {/* Virtualized List */}
      <div className="flex-1 overflow-hidden" ref={containerRef}>
        {height > 0 && (() => {
          const VirtualList = List as any
          return (
            <VirtualList
              height={height}
              rowCount={items.length}
              rowHeight={typeof window !== 'undefined' && window.innerWidth < 768 ? 56 : 40}
              width="100%"
              rowProps={{
                items,
                selectedIds,
                isTrash,
                isShared,
                onToggleSelect,
                onSingleSelect,
                onSelectRange,
                onOpenFolder,
                onOpenFile,
                onContextMenu,
              }}
              rowComponent={Row as any}
            />
          )
        })()}
      </div>
    </div>
  )
}
