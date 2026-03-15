import React, { type MouseEvent, useRef, useMemo, useState } from 'react'
import { List } from 'react-window'
import { getAccessToken } from '../lib/token'
import { apiClient } from '../lib/api'
import type { FileItem } from '../types/file'
import { cn } from '../lib/format'
import { FileIcon } from './FileIcon'
import { useResizeObserver } from '../hooks/useResizeObserver'

interface GridViewProps {
  items: FileItem[]
  selectedIds?: Set<string>
  onToggleSelect?: (id: string) => void
  onSingleSelect?: (id: string) => void
  onSelectRange?: (id: string) => void
  onOpenFolder: (item: FileItem) => void
  onOpenFile?: (item: FileItem) => void
  onContextMenu: (item: FileItem, e: MouseEvent) => void
}

function chunkArray<T>(arr: T[], size: number): T[][] {
  const result: T[][] = []
  for (let i = 0; i < arr.length; i += size) {
    result.push(arr.slice(i, i + size))
  }
  return result
}

export function GridView({
  items,
  selectedIds,
  onToggleSelect,
  onSingleSelect,
  onSelectRange,
  onOpenFolder,
  onOpenFile,
  onContextMenu,
}: GridViewProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const { width, height } = useResizeObserver(containerRef)

  const columnCount = useMemo(() => {
    if (width === 0) return 6
    if (width < 768) return 2
    if (width < 1024) return 4
    return 6
  }, [width])

  const chunkedRows = useMemo(() => chunkArray(items, columnCount), [items, columnCount])

  if (items.length === 0) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center text-zinc-500 h-full">
        <p>No items in this view.</p>
      </div>
    )
  }

  const Row = ({ index, style }: { index: number; style: React.CSSProperties }) => {
    const rowItems = chunkedRows[index]

    return (
      <div style={style} className="flex gap-4 w-full px-4 pt-4">
        {rowItems.map((item) => (
          <div key={item.id} style={{ width: `calc((100% - ${(columnCount - 1) * 16}px) / ${columnCount})` }}>
            <Card
              item={item}
              isSelected={selectedIds?.has(item.id) ?? false}
              onToggleSelect={onToggleSelect}
              onSingleSelect={onSingleSelect}
              onSelectRange={onSelectRange}
              onOpenFolder={onOpenFolder}
              onOpenFile={onOpenFile}
              onContextMenu={onContextMenu}
            />
          </div>
        ))}
      </div>
    )
  }

  return (
    <div className="flex-1 overflow-hidden select-none" ref={containerRef}>
      {height > 0 && width > 0 && (() => {
        const VirtualList = List as any
        return (
          <VirtualList
            height={height}
            rowCount={chunkedRows.length}
            rowHeight={220} // Fixed height for cards + padding
            width="100%"
            rowProps={{ chunkedRows, selectedIds }}
            rowComponent={Row as any}
          />
        )
      })()}
    </div>
  )
}

interface CardProps {
  item: FileItem
  isSelected: boolean
  onToggleSelect?: (id: string) => void
  onSingleSelect?: (id: string) => void
  onSelectRange?: (id: string) => void
  onOpenFolder: (f: FileItem) => void
  onOpenFile?: (f: FileItem) => void
  onContextMenu: (item: FileItem, e: MouseEvent) => void
}

function Card({
  item,
  isSelected,
  onToggleSelect,
  onSingleSelect,
  onSelectRange,
  onOpenFolder,
  onOpenFile,
  onContextMenu,
}: CardProps) {
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
      onClick={(e) => {
        e.stopPropagation()
        if (e.shiftKey && onSelectRange) {
          onSelectRange(item.id)
        } else if ((e.ctrlKey || e.metaKey) && onToggleSelect) {
          onToggleSelect(item.id)
        } else if (onSingleSelect) {
          onSingleSelect(item.id)
        }
      }}
      onDoubleClick={() => {
        if (isBrokenShortcut || item.status === 'QUARANTINED') return
        if (item.is_directory) {
          onOpenFolder(item)
        } else if (onOpenFile) {
          onOpenFile(item)
        }
      }}
      onContextMenu={(e) => {
        if (onToggleSelect && !isSelected) {
          onToggleSelect(item.id)
        }
        onContextMenu(item, e)
      }}
      tabIndex={0}
      role="button"
      className={cn(
        'group relative flex h-full flex-col overflow-hidden rounded-xl border border-arch-border/50 bg-arch-900/50 p-1 shadow-sm transition-all duration-200 focus:outline-none focus:ring-2 focus:ring-amber-500/50',
        isSelected ? 'bg-amber-500/10 border-amber-500/50' : 'hover:border-arch-border hover:bg-arch-850/60',
        item.status === 'QUARANTINED' && 'bg-[repeating-linear-gradient(45deg,transparent,transparent_10px,rgba(220,38,38,0.1)_10px,rgba(220,38,38,0.1)_20px)] border-red-500/50 hover:bg-[repeating-linear-gradient(45deg,transparent,transparent_10px,rgba(220,38,38,0.1)_10px,rgba(220,38,38,0.1)_20px)] hover:border-red-500/50'
      )}
      title={item.status === 'QUARANTINED' ? 'Malware detected. This file is locked in quarantine.' : undefined}
    >
      <div className="absolute left-3 top-3 z-10" onClick={(e) => e.stopPropagation()}>
        <input
          type="checkbox"
          checked={isSelected}
          onChange={() => onToggleSelect?.(item.id)}
          className={cn(
            'form-checkbox h-4 w-4 rounded-sm border-arch-700 bg-arch-950/80 text-amber-500 focus:ring-amber-500/40 cursor-pointer transition-opacity duration-200',
            isSelected ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'
          )}
        />
      </div>

      <div className="relative flex aspect-[4/3] w-full items-center justify-center overflow-hidden rounded-lg bg-arch-950/50">
        {showThumbnail ? (
          <img 
            src={thumbUrl} 
            alt={item.name} 
            className={cn("h-full w-full object-cover", item.status === 'QUARANTINED' && 'grayscale opacity-50')} 
            onError={() => setImgError(true)}
          />
        ) : (
          <div className={cn(item.status === 'QUARANTINED' && 'grayscale opacity-50')}>
            <FileIcon filename={item.name} isDirectory={item.is_directory} size={32} isShortcut={isShortcut} isBrokenShortcut={isBrokenShortcut} />
          </div>
        )}
        {item.status === 'QUARANTINED' && (
          <div className="absolute top-2 right-2 bg-red-500 rounded-full w-6 h-6 flex items-center justify-center shadow-[0_0_10px_rgba(239,68,68,0.5)]">
            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 2v20M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" />
            </svg>
          </div>
        )}
      </div>

      <div className="flex flex-1 flex-col justify-center px-3 py-3">
        <h3 className="truncate text-[13px] font-medium text-zinc-200" title={item.name}>
          {item.name}
        </h3>
      </div>
    </div>
  )
}
