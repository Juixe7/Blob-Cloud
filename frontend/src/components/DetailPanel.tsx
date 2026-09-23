import { useState } from 'react'
import { cn, formatFileSize } from '../lib/format'
import type { FileItem } from '../types/file'
import { FileIcon } from './FileIcon'
import { apiClient } from '../lib/api'

interface DetailPanelProps {
  item: FileItem | null
  isOpen: boolean
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

export function DetailPanel({ item, isOpen, onClose, onItemUpdated }: DetailPanelProps) {
  const [isGenerating, setIsGenerating] = useState(false)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  const isImage = item?.mime_type?.startsWith('image/') || (item?.name ? /\.(jpg|jpeg|png|webp|gif)$/i.test(item.name) : false)
  const isDoc = item?.name ? /\.(txt|md|pdf)$/i.test(item.name) : false
  const canGenerateAI = Boolean(item && !item.is_directory && (isImage || isDoc))

  const handleGenerateAI = async () => {
    if (!item) return
    setIsGenerating(true)
    setErrorMsg(null)
    try {
      const res = await apiClient.post<{ file_id: string; tags: string | null; summary: string | null }>(
        `/files/${item.id}/ai-insights`
      )
      const updated: FileItem = {
        ...item,
        tags: res.data.tags ?? item.tags,
        summary: res.data.summary ?? item.summary,
      }
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
    <div
      className={cn(
        'h-full w-72 md:w-80 flex-shrink-0 bg-zinc-950 border-l border-zinc-900 transition-all duration-300 ease-in-out z-30',
        isOpen ? 'mr-0 opacity-100' : '-mr-72 md:-mr-80 opacity-0 pointer-events-none'
      )}
    >
      <div className="flex h-full flex-col">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-zinc-900 px-4 py-3 shrink-0">
          <div className="flex items-center gap-3 overflow-hidden">
            {item && (
              <div className="flex-shrink-0">
                <FileIcon filename={item.name} isDirectory={item.is_directory} size={20} />
              </div>
            )}
            <h2 className="truncate text-sm font-semibold text-zinc-100" title={item?.name || 'Details'}>
              {item ? item.name : 'Details'}
            </h2>
          </div>
          <button
            onClick={onClose}
            className="flex-shrink-0 rounded p-1 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100 transition-colors"
            aria-label="Close details panel"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="18" y1="6" x2="6" y2="18"></line>
              <line x1="6" y1="6" x2="18" y2="18"></line>
            </svg>
          </button>
        </div>

        {/* Content Area */}
        <div className="flex-1 overflow-y-auto scrollbar-hide p-4">
          {!item ? (
            <div className="flex h-full flex-col items-center justify-center text-center">
              <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-zinc-900 border border-zinc-800">
                <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" className="text-zinc-500">
                  <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"></path>
                  <polyline points="13 2 13 9 20 9"></polyline>
                </svg>
              </div>
              <h3 className="text-sm font-medium text-zinc-300">No item selected</h3>
              <p className="mt-1 text-xs text-zinc-500">Select a file or folder to view its details.</p>
            </div>
          ) : (
            <div className="space-y-6">
              {/* Preview Window */}
              <div className="flex aspect-[4/3] w-full items-center justify-center rounded-lg bg-zinc-900 overflow-hidden border border-zinc-800 shadow-inner">
                {item.thumbnail_url ? (
                  <img
                    src={item.thumbnail_url}
                    alt={item.name}
                    className="h-full w-full object-cover"
                  />
                ) : (
                  <FileIcon filename={item.name} isDirectory={item.is_directory} size={64} />
                )}
              </div>

              {/* AI Actions */}
              {canGenerateAI && (
                <div className="flex items-center justify-between">
                  <span className="text-xs font-semibold text-zinc-400 uppercase tracking-wider flex items-center gap-1.5">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-amber-500">
                      <path d="M12 2v4m0 12v4M4.93 4.93l2.83 2.83m8.48 8.48l2.83 2.83M2 12h4m12 0h4M4.93 19.07l2.83-2.83m8.48-8.48l2.83-2.83"></path>
                    </svg>
                    AI Insights
                  </span>
                  <button
                    type="button"
                    onClick={handleGenerateAI}
                    disabled={isGenerating}
                    className="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium text-amber-300 bg-amber-950/40 hover:bg-amber-900/60 border border-amber-700/50 rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    {isGenerating ? (
                      <>
                        <svg className="animate-spin h-3 w-3 text-amber-400" viewBox="0 0 24 24" fill="none">
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
                        {item.summary || item.tags ? 'Re-analyze' : 'Generate'}
                      </>
                    )}
                  </button>
                </div>
              )}

              {errorMsg && (
                <div className="p-2 text-xs text-rose-400 bg-rose-950/40 border border-rose-800/40 rounded">
                  {errorMsg}
                </div>
              )}

              {/* AI Auto-Tags Section */}
              {item.tags && (
                <div className="space-y-2">
                  <h4 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider flex items-center gap-1.5">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-violet-500">
                      <path d="M20.59 13.41l-7.17 7.17a2 2 0 0 1-2.83 0L2 12V2h10l8.59 8.59a2 2 0 0 1 0 2.82z"></path>
                      <line x1="7" y1="7" x2="7.01" y2="7"></line>
                    </svg>
                    Auto-Tags
                  </h4>
                  <div className="flex flex-wrap gap-1.5">
                    {item.tags.split(',').map((tag) => {
                      const t = tag.trim()
                      if (!t) return null
                      return (
                        <span
                          key={t}
                          className="px-2 py-0.5 text-[11px] font-medium rounded-full bg-violet-950/30 text-violet-400 border border-violet-800/30"
                        >
                          {t}
                        </span>
                      )
                    })}
                  </div>
                </div>
              )}

              {/* AI Conceptual Summary Section */}
              {item.summary && (
                <div className="space-y-2">
                  <h4 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider flex items-center gap-1.5">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-amber-500">
                      <path d="M12 2v4m0 12v4M4.93 4.93l2.83 2.83m8.48 8.48l2.83 2.83M2 12h4m12 0h4M4.93 19.07l2.83-2.83m8.48-8.48l2.83-2.83"></path>
                    </svg>
                    AI Conceptual Summary
                  </h4>
                  <div className="p-3 bg-zinc-900 border border-zinc-800 rounded-md text-xs text-zinc-300 leading-relaxed shadow-sm max-h-48 overflow-y-auto">
                    {item.summary}
                  </div>
                </div>
              )}

              <div className="h-px bg-zinc-900" />

              {/* File Properties */}
              <div className="space-y-3">
                <h4 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider">Properties</h4>
                <div className="grid grid-cols-[100px_1fr] gap-y-3 text-xs">
                  <span className="text-zinc-500">Type</span>
                  <span className="text-zinc-200 truncate">{formatMimeType(item.mime_type, item.is_directory)}</span>

                  <span className="text-zinc-500">Size</span>
                  <span className="text-zinc-200">{item.is_directory ? '--' : formatFileSize(item.size_bytes)}</span>

                  <span className="text-zinc-500">Location</span>
                  <span className="text-zinc-200 truncate" title={item.original_location || 'My Drive'}>{item.original_location || 'My Drive'}</span>

                  <span className="text-zinc-500">Created</span>
                  <span className="text-zinc-200">{new Date(item.created_at).toLocaleString()}</span>

                  <span className="text-zinc-500">Modified</span>
                  <span className="text-zinc-200">{new Date(item.updated_at).toLocaleString()}</span>
                </div>
              </div>

            </div>
          )}
        </div>
      </div>
    </div>
  )
}
