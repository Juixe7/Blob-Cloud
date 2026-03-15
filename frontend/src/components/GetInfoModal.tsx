import { Modal } from './ui/Modal'
import type { FileItem } from '../types/file'
import { formatFileSize } from '../lib/format'

interface GetInfoModalProps {
  item: FileItem
  onClose: () => void
}

export function GetInfoModal({ item, onClose }: GetInfoModalProps) {
  const isImage = item.mime_type?.startsWith('image/')
  const isDoc = item.name.endsWith('.txt') || item.name.endsWith('.md') || item.name.endsWith('.pdf')
  
  return (
    <Modal open={true} label={`Info: ${item.name}`} onClose={onClose}>
      <h2 className="text-lg font-semibold text-white mb-4">Info: {item.name}</h2>
      <div className="space-y-4 text-sm text-zinc-300">
        <div className="grid grid-cols-[100px_1fr] gap-2">
          <span className="text-zinc-500">Kind:</span>
          <span>{item.is_directory ? 'Folder' : (item.mime_type || 'Unknown')}</span>

          <span className="text-zinc-500">Size:</span>
          <span>{formatFileSize(item.size_bytes)}</span>

          <span className="text-zinc-500">Where:</span>
          <span>{item.original_location || 'My Drive'}</span>
          
          <span className="text-zinc-500">Created:</span>
          <span>{new Date(item.created_at).toLocaleString()}</span>
          
          <span className="text-zinc-500">Modified:</span>
          <span>{new Date(item.updated_at).toLocaleString()}</span>
        </div>

        <div className="my-4 h-px bg-arch-border" />
        
        <div className="space-y-3">
          <h4 className="font-medium text-white flex items-center gap-2">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M12 2v4m0 12v4M4.93 4.93l2.83 2.83m8.48 8.48l2.83 2.83M2 12h4m12 0h4M4.93 19.07l2.83-2.83m8.48-8.48l2.83-2.83" />
            </svg>
            AI Insights
          </h4>
          
          {(isImage || isDoc) ? (
            <>
              {item.tags ? (
                <div>
                  <span className="text-zinc-500 block mb-1">Detected Tags:</span>
                  <div className="flex flex-wrap gap-1.5">
                    {item.tags.split(',').map((t) => (
                      <span key={t} className="px-2 py-0.5 rounded-full bg-arch-850 border border-arch-border text-xs">
                        {t.trim()}
                      </span>
                    ))}
                  </div>
                </div>
              ) : (
                <p className="text-xs text-zinc-500 italic">No tags generated yet.</p>
              )}

              {isDoc && (
                <div className="mt-3">
                  <span className="text-zinc-500 block mb-1">AI Summary:</span>
                  {item.summary ? (
                    <div className="p-3 bg-arch-900 border border-arch-border rounded text-sm text-zinc-300 leading-relaxed">
                      {item.summary}
                    </div>
                  ) : (
                    <p className="text-xs text-zinc-500 italic">No summary generated yet.</p>
                  )}
                </div>
              )}
            </>
          ) : (
            <p className="text-xs text-zinc-500">AI insights are not available for this file type.</p>
          )}
        </div>

        <div className="flex justify-end gap-2 mt-6">
          <button
            onClick={onClose}
            className="px-4 py-2 text-sm font-medium text-white rounded bg-arch-800 hover:bg-arch-700 transition-colors"
          >
            Close
          </button>
        </div>
      </div>
    </Modal>
  )
}
