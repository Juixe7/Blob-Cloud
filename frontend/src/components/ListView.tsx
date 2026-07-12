import type { FileItem } from '../types/file'
import { formatFileSize, formatDate, getFileIcon, cn } from '../lib/format'
import { FileIcon } from './FileIcon'

interface ListViewProps {
  items: FileItem[]
  /** Called when a directory row is double-clicked. */
  onOpenFolder: (item: FileItem) => void
}

/**
 * Table-style file listing with columns: Name, Date Modified, Size.
 * Double-clicking a folder row navigates into it.
 */
export function ListView({ items, onOpenFolder }: ListViewProps) {
  return (
    <div className="flex-1 overflow-auto">
      {items.length === 0 ? (
        <EmptyState />
      ) : (
        <table className="w-full text-sm" role="grid">
          <thead>
            <tr className="border-b border-zinc-800 text-left text-xs font-medium uppercase tracking-wider text-zinc-500">
              <th className="px-4 py-3 w-[60%]">Name</th>
              <th className="px-4 py-3 w-[25%]">Date Modified</th>
              <th className="px-4 py-3 w-[15%]">Size</th>
            </tr>
          </thead>
          <tbody>
            {items.map((item) => (
              <Row key={item.id} item={item} onOpenFolder={onOpenFolder} />
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

function Row({ item, onOpenFolder }: { item: FileItem; onOpenFolder: (f: FileItem) => void }) {
  const icon = getFileIcon(item.name, item.is_directory)

  return (
    <tr
      className={cn(
        'group cursor-default border-b border-zinc-800/50 transition-colors duration-150',
        'hover:bg-zinc-900/60',
      )}
      onDoubleClick={() => {
        if (item.is_directory) onOpenFolder(item)
      }}
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'Enter' && item.is_directory) onOpenFolder(item)
      }}
      role="row"
      aria-label={item.name}
    >
      {/* Name */}
      <td className="px-4 py-2.5">
        <div className="flex items-center gap-3">
          <FileIcon variant={icon} size={18} />
          <span className="truncate text-zinc-200 group-hover:text-zinc-50">{item.name}</span>
          {item.is_directory && (
            <span className="text-[10px] font-medium uppercase tracking-wider text-zinc-600">
              Folder
            </span>
          )}
        </div>
      </td>

      {/* Date Modified */}
      <td className="px-4 py-2.5 text-zinc-500">
        {formatDate(item.updated_at)}
      </td>

      {/* Size */}
      <td className="px-4 py-2.5 text-zinc-500">
        {item.is_directory ? '—' : formatFileSize(item.size_bytes)}
      </td>
    </tr>
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

export default ListView
