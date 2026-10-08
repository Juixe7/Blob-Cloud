import { useState, useEffect } from 'react'
import { cn, formatFileSize } from '../lib/format'
import type { FileItem } from '../types/file'
import { FileIcon } from './FileIcon'
import { apiClient } from '../lib/api'
import { getAccessToken } from '../lib/token'
import { ContactCardPopover } from './ContactCardPopover'

interface DetailPanelProps {
  item: FileItem | null
  isOpen: boolean
  onClose: () => void
  onItemUpdated?: (updated: FileItem) => void
  onManageAccess?: (item: FileItem) => void
  isSharedView?: boolean
  currentLocationPath?: string
  currentUserEmail?: string
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

export function DetailPanel({
  item,
  isOpen,
  onClose,
  onItemUpdated,
  onManageAccess,
  isSharedView = false,
  currentLocationPath,
  currentUserEmail,
}: DetailPanelProps) {
  const [isGenerating, setIsGenerating] = useState(false)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)
  const [imgError, setImgError] = useState(false)

  const token = getAccessToken() ?? ''
  const base = apiClient.defaults.baseURL ?? '/api'

  const effectiveId = item?.target_id || item?.id

  const isImage = item?.mime_type?.startsWith('image/') || (item?.name ? /\.(jpg|jpeg|png|webp|gif)$/i.test(item.name) : false)
  const isVideo = item?.mime_type?.startsWith('video/') || (item?.name ? /\.(mp4|webm|mov|mkv)$/i.test(item.name) : false)
  const isPdf = item?.mime_type === 'application/pdf' || (item?.name ? item.name.toLowerCase().endsWith('.pdf') : false)
  const isDoc = item?.name ? /\.(txt|md|pdf|doc|docx|csv|xlsx)$/i.test(item.name) : false
  const canGenerateAI = Boolean(item && !item.is_directory && (isImage || isDoc))

  const canHaveThumbnail = Boolean(item && !item.is_directory && (isImage || isVideo || isPdf || item.thumbnail_url))
  const thumbUrl = item?.thumbnail_url || (effectiveId ? `${base}/files/${effectiveId}/thumbnail?token=${encodeURIComponent(token)}` : '')

  useEffect(() => {
    setImgError(false)
  }, [item?.id])

  const isItemShared = Boolean(
    isSharedView ||
    item?.shared_by_email ||
    item?.shared_at ||
    (item?.role && item.role !== 'OWNER')
  )

  const resolvedLocation = item?.original_location
    ? item.original_location
    : isItemShared
    ? 'Shared with me'
    : currentLocationPath || 'My Drive'

  useEffect(() => {
    if (isOpen && item && !item.is_directory && (isDoc || isImage) && !item.summary && effectiveId) {
      apiClient.get<FileItem>(`/files/${effectiveId}`).then((res) => {
        if (res.data && (res.data.summary || res.data.tags || res.data.owner_email || res.data.role)) {
          onItemUpdated?.({ ...item, ...res.data, id: item.id })
        }
      }).catch(() => {})
    }
  }, [isOpen, item?.id, effectiveId, isDoc, isImage, item?.summary, onItemUpdated])

  const handleGenerateAI = async () => {
    if (!item || !effectiveId) return
    setIsGenerating(true)
    setErrorMsg(null)
    try {
      const res = await apiClient.post<{ file_id: string; tags: string | null; summary: string | null }>(
        `/files/${effectiveId}/ai-insights`
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
    <>
      {/* Mobile backdrop */}
      {isOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/60 backdrop-blur-xs md:hidden"
          onClick={onClose}
          aria-hidden="true"
        />
      )}

      <div
        className={cn(
          'h-full flex-shrink-0 bg-zinc-950 border-l border-zinc-900 transition-all duration-300 ease-in-out z-40',
          'w-80 md:w-88',
          'max-md:fixed max-md:inset-y-0 max-md:right-0 max-md:shadow-2xl max-md:max-w-xs',
          isOpen
            ? 'mr-0 opacity-100 translate-x-0'
            : '-mr-80 md:-mr-88 max-md:translate-x-full opacity-0 pointer-events-none'
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
              className="flex-shrink-0 rounded p-1 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100 transition-colors cursor-pointer"
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
                {/* Preview Window (Rich Image / PDF / Video / Icon Preview) */}
                <div className="flex aspect-[4/3] w-full items-center justify-center rounded-lg bg-zinc-900 overflow-hidden border border-zinc-800 shadow-inner">
                  {canHaveThumbnail && !imgError && thumbUrl ? (
                    <img
                      src={thumbUrl}
                      alt={item.name}
                      className="h-full w-full object-contain bg-zinc-950/60"
                      onError={() => setImgError(true)}
                    />
                  ) : (
                    <FileIcon filename={item.name} isDirectory={item.is_directory} size={64} />
                  )}
                </div>

                {/* AI Actions */}
                {canGenerateAI && (
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-semibold text-zinc-400 uppercase tracking-wider flex items-center gap-1.5">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-zinc-400">
                        <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
                      </svg>
                      AI Insights
                    </span>
                    <button
                      type="button"
                      onClick={handleGenerateAI}
                      disabled={isGenerating}
                      className="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium text-zinc-200 hover:text-white bg-zinc-800 hover:bg-zinc-700 border border-zinc-700 hover:border-zinc-600 rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer shadow-xs"
                    >
                      {isGenerating ? (
                        <>
                          <svg className="animate-spin h-3 w-3 text-zinc-300" viewBox="0 0 24 24" fill="none">
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
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-zinc-400">
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
                            className="px-2.5 py-0.5 text-[11px] font-medium rounded-md bg-zinc-800/90 text-zinc-300 border border-zinc-700/70 hover:border-zinc-500 hover:text-white transition-colors"
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
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-zinc-400">
                        <path d="M12 2v4m0 12v4M4.93 4.93l2.83 2.83m8.48 8.48l2.83 2.83M2 12h4m12 0h4M4.93 19.07l2.83-2.83m8.48-8.48l2.83-2.83"></path>
                      </svg>
                      AI Conceptual Summary
                    </h4>
                    <div className="p-3 bg-zinc-900 border border-zinc-800 rounded-lg text-xs text-zinc-300 leading-relaxed shadow-xs max-h-48 overflow-y-auto">
                      {item.summary}
                    </div>
                  </div>
                )}

                <div className="h-px bg-zinc-900" />

                {/* Who Has Access Section (Google Drive Standard) */}
                <div className="space-y-3">
                  <div className="flex items-center justify-between">
                    <h4 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider flex items-center gap-1.5">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="text-zinc-400">
                        <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" />
                        <circle cx="9" cy="7" r="4" />
                        <path d="M22 21v-2a4 4 0 0 0-3-3.87" />
                        <path d="M16 3.13a4 4 0 0 1 0 7.75" />
                      </svg>
                      Who Has Access
                    </h4>
                    {onManageAccess && (
                      <button
                        type="button"
                        onClick={() => onManageAccess(item)}
                        className="text-[11px] font-medium text-zinc-400 hover:text-zinc-200 hover:underline transition-colors cursor-pointer"
                      >
                        Manage access
                      </button>
                    )}
                  </div>

                  <div className="space-y-2 rounded-lg border border-zinc-800/80 bg-zinc-900/60 p-2.5">
                    {/* Owner Entry */}
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex items-center gap-2 min-w-0">
                        <ContactCardPopover
                          email={item.owner_email || currentUserEmail || 'Owner'}
                          isOwner={true}
                          showEmailText={true}
                          avatarSize="sm"
                        />
                      </div>
                      <span className="shrink-0 px-2 py-0.5 rounded text-[10px] font-medium bg-zinc-800 text-zinc-300 border border-zinc-700">
                        Owner
                      </span>
                    </div>

                    {/* Shared By Entry (if shared with me) */}
                    {item.shared_by_email && item.shared_by_email !== item.owner_email && (
                      <div className="flex items-center justify-between gap-2 pt-1.5 border-t border-zinc-800/60">
                        <div className="flex items-center gap-2 min-w-0">
                          <ContactCardPopover
                            email={item.shared_by_email}
                            sharedAt={item.shared_at}
                            showEmailText={true}
                            avatarSize="sm"
                          />
                        </div>
                        <span className="shrink-0 text-[10px] text-zinc-500 font-mono">
                          Shared by
                        </span>
                      </div>
                    )}

                    {/* Your Role Badge */}
                    <div className="flex items-center justify-between gap-2 pt-1.5 border-t border-zinc-800/60 text-xs">
                      <span className="text-zinc-500 text-[11px]">Your permission</span>
                      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold bg-zinc-800 text-zinc-200 border border-zinc-700">
                        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" className="text-zinc-400">
                          <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
                        </svg>
                        {item.role === 'OWNER' ? 'Owner (Full Access)' : item.role === 'EDITOR' ? 'Editor (Can Edit)' : 'Viewer (Read Only)'}
                      </span>
                    </div>
                  </div>
                </div>

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
                    <span className="text-zinc-200 truncate" title={resolvedLocation}>{resolvedLocation}</span>

                    <span className="text-zinc-500">Owner</span>
                    <span className="text-zinc-200 truncate" title={item.owner_email || (item.user_id ? 'You' : 'Unknown')}>
                      {item.owner_email || 'You'}
                    </span>

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
    </>
  )
}
