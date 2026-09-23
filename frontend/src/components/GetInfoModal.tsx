import { useState } from 'react'
import { Modal } from './ui/Modal'
import type { FileItem } from '../types/file'
import { formatFileSize } from '../lib/format'
import { apiClient } from '../lib/api'

interface GetInfoModalProps {
  item: FileItem
  onClose: () => void
  onItemUpdated?: (updated: FileItem) => void
}

function formatMimeType(mime?: string, isDir?: boolean): string {
  if (isDir) return 'Folder'
  if (!mime) return 'Unknown'
  const map: Record<string, string> = {
    'application/pdf': 'PDF Document (.pdf)',
    'application/vnd.openxmlformats-officedocument.wordprocessingml.document': 'Word Document (.docx)',
    'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': 'Excel Spreadsheet (.xlsx)',
    'application/vnd.openxmlformats-officedocument.presentationml.presentation': 'PowerPoint (.pptx)',
    'text/plain': 'Plain Text (.txt)',
    'text/markdown': 'Markdown Document (.md)',
    'text/csv': 'CSV Spreadsheet (.csv)',
    'application/json': 'JSON File (.json)',
    'image/png': 'PNG Image (.png)',
    'image/jpeg': 'JPEG Image (.jpg)',
    'image/webp': 'WebP Image (.webp)',
    'image/gif': 'GIF Image (.gif)',
    'image/svg+xml': 'SVG Vector (.svg)',
    'application/zip': 'ZIP Archive (.zip)',
  }
  return map[mime] || mime
}

export function GetInfoModal({ item, onClose, onItemUpdated }: GetInfoModalProps) {
  const [currentItem, setCurrentItem] = useState<FileItem>(item)
  const [isGenerating, setIsGenerating] = useState(false)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  const isImage = currentItem.mime_type?.startsWith('image/') || /\.(jpg|jpeg|png|webp|gif)$/i.test(currentItem.name)
  const isDoc = /\.(txt|md|pdf)$/i.test(currentItem.name)
  const canGenerateAI = (isImage || isDoc) && !currentItem.is_directory

  const handleGenerateAI = async () => {
    setIsGenerating(true)
    setErrorMsg(null)
    try {
      const res = await apiClient.post<{ file_id: string; tags: string | null; summary: string | null }>(
        `/files/${currentItem.id}/ai-insights`
      )
      const updated: FileItem = {
        ...currentItem,
        tags: res.data.tags ?? currentItem.tags,
        summary: res.data.summary ?? currentItem.summary,
      }
      setCurrentItem(updated)
      onItemUpdated?.(updated)
    } catch (err: unknown) {
      const msg = err && typeof err === 'object' && 'response' in err
        ? (err as { response?: { data?: { error?: string } } }).response?.data?.error
        : 'Failed to generate AI insights'
      setErrorMsg(msg || 'Failed to generate AI insights')
    } finally {
      setIsGenerating(false)
    }
  }

  return (
    <Modal open={true} label={`Info: ${currentItem.name}`} onClose={onClose} maxWidthClass="max-w-lg">
      <div className="flex items-start justify-between gap-3 mb-4">
        <div className="min-w-0 flex-1">
          <span className="text-[11px] font-semibold uppercase tracking-wider text-amber-500 block mb-1">
            File Details
          </span>
          <h2
            className="text-base md:text-lg font-semibold text-white break-words [overflow-wrap:anywhere] leading-snug"
            title={currentItem.name}
          >
            {currentItem.name}
          </h2>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="text-zinc-400 hover:text-white transition-colors p-1.5 -mr-1.5 -mt-1.5 rounded-lg hover:bg-arch-800"
          aria-label="Close dialog"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      <div className="space-y-4 text-sm text-zinc-300">
        <div className="grid grid-cols-[90px_1fr] gap-y-2 gap-x-3 text-xs md:text-sm">
          <span className="text-zinc-500">Kind:</span>
          <span className="min-w-0 break-words [overflow-wrap:anywhere] text-zinc-200">
            {formatMimeType(currentItem.mime_type, currentItem.is_directory)}
          </span>

          <span className="text-zinc-500">Size:</span>
          <span className="min-w-0 text-zinc-200">{formatFileSize(currentItem.size_bytes)}</span>

          <span className="text-zinc-500">Where:</span>
          <span className="min-w-0 break-words [overflow-wrap:anywhere] text-zinc-200">{currentItem.original_location || 'My Drive'}</span>
          
          <span className="text-zinc-500">Created:</span>
          <span className="min-w-0 text-zinc-200">{new Date(currentItem.created_at).toLocaleString()}</span>
          
          <span className="text-zinc-500">Modified:</span>
          <span className="min-w-0 text-zinc-200">{new Date(currentItem.updated_at).toLocaleString()}</span>
        </div>

        <div className="my-4 h-px bg-arch-border" />
        
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h4 className="font-medium text-white flex items-center gap-2 text-sm">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-amber-500">
                <path d="M12 2v4m0 12v4M4.93 4.93l2.83 2.83m8.48 8.48l2.83 2.83M2 12h4m12 0h4M4.93 19.07l2.83-2.83m8.48-8.48l2.83-2.83" />
              </svg>
              AI Insights
            </h4>

            {canGenerateAI && (
              <button
                type="button"
                onClick={handleGenerateAI}
                disabled={isGenerating}
                className="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium text-amber-300 bg-amber-950/40 hover:bg-amber-900/60 border border-amber-700/50 rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {isGenerating ? (
                  <>
                    <svg className="animate-spin h-3.5 w-3.5 text-amber-400" viewBox="0 0 24 24" fill="none">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                    </svg>
                    Analyzing...
                  </>
                ) : (
                  <>
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" />
                    </svg>
                    {currentItem.summary || currentItem.tags ? 'Re-analyze' : 'Generate Summary'}
                  </>
                )}
              </button>
            )}
          </div>

          {errorMsg && (
            <div className="p-2 text-xs text-rose-400 bg-rose-950/40 border border-rose-800/40 rounded">
              {errorMsg}
            </div>
          )}
          
          {canGenerateAI ? (
            <>
              {currentItem.tags ? (
                <div>
                  <span className="text-zinc-500 block mb-1 text-xs">Detected Tags:</span>
                  <div className="flex flex-wrap gap-1.5">
                    {currentItem.tags.split(',').map((t) => (
                      <span key={t} className="px-2 py-0.5 rounded-full bg-arch-850 border border-arch-border text-xs text-zinc-200">
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
                  <span className="text-zinc-500 block mb-1 text-xs">AI Summary:</span>
                  {currentItem.summary ? (
                    <div className="p-3 bg-arch-900 border border-arch-border rounded text-sm text-zinc-300 leading-relaxed max-h-48 overflow-y-auto">
                      {currentItem.summary}
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
            type="button"
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
