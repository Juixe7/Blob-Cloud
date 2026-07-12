import type { FileItem } from '../types/file'
import { getFileIcon, cn } from '../lib/format'
import { FileIcon } from './FileIcon'

interface GridViewProps {
  items: FileItem[]
  onOpenFolder: (item: FileItem) => void
}

/**
 * Responsive card grid for file browsing. Cards show an icon preview area
 * and a truncated name label. Double-clicking a folder card navigates in.
 */
export function GridView({ items, onOpenFolder }: GridViewProps) {
  return (
    <div className="flex-1 overflow-auto p-4">
      {items.length === 0 ? (
        <EmptyState />
      ) : (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4 lg:grid-cols-6">
          {items.map((item) => (
            <Card key={item.id} item={item} onOpenFolder={onOpenFolder} />
          ))}
        </div>
      )}
    </div>
  )
}

function Card({ item, onOpenFolder }: { item: FileItem; onOpenFolder: (f: FileItem) => void }) {
  const icon = getFileIcon(item.name, item.is_directory)

  return (
    <button
      type="button"
      className={cn(
        'group flex flex-col items-center gap-3 rounded-xl border border-zinc-800 bg-zinc-900/40 p-4 transition-all duration-200',
        'hover:border-zinc-700 hover:shadow-glow hover:bg-zinc-900/80',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-500/60 focus-visible:ring-offset-2 focus-visible:ring-offset-zinc-950',
      )}
      onDoubleClick={() => {
        if (item.is_directory) onOpenFolder(item)
      }}
      tabIndex={0}
      aria-label={`${item.name}${item.is_directory ? ' (folder)' : ''}`}
    >
      {/* Icon / preview area */}
      <div className="flex h-16 w-full items-center justify-center rounded-lg bg-zinc-900">
        <FileIcon variant={icon} size={item.is_directory ? 32 : 28} />
      </div>

      {/* Name label */}
      <span className="w-full truncate text-center text-xs text-zinc-300 group-hover:text-zinc-50">
        {item.name}
      </span>
    </button>
  )
}

function EmptyState() {
  return (
    <div className="flex h-full min-h-[300px] flex-col items-center justify-center gap-4">
      <div className="flex h-20 w-20 items-center justify-center rounded-2xl border border-dashed border-zinc-800">
        <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round" className="text-zinc-700" aria-hidden="true">
          <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
        </svg>
      </div>
      <p className="text-sm text-zinc-500">This folder is empty</p>
      <p className="text-xs text-zinc-600">Upload files or create a new folder to get started.</p>
    </div>
  )
}

export default GridView
