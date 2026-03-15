import { useEffect, useState } from 'react'
import { apiClient } from '../lib/api'
import { getAccessToken } from '../lib/token'
import type { FileItem } from '../types/file'
import { formatFileSize, formatDate } from '../lib/format'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Spinner } from './ui/Spinner'
import { FileIcon } from './FileIcon'

interface FilePreviewModalProps {
  open: boolean
  onClose: () => void
  file: FileItem | null
  onDownload?: (file: FileItem) => void
  publicToken?: string
}

/**
 * Full-screen Google Drive style in-browser file preview modal.
 * Supports inline rendering for Images, PDFs, Audio, Video, and Code/Text files.
 */
export function FilePreviewModal({ open, onClose, file, onDownload, publicToken }: FilePreviewModalProps) {
  const [blobUrl, setBlobUrl] = useState<string | null>(null)
  const [textContent, setTextContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  const ext = file?.name.split('.').pop()?.toLowerCase() ?? ''
  const isImage = ['png', 'jpg', 'jpeg', 'webp', 'gif', 'svg', 'bmp'].includes(ext)
  const isPdf = ext === 'pdf'
  const isAudio = ['mp3', 'wav', 'aac', 'flac', 'm4a', 'ogg'].includes(ext)
  const isVideo = ['mp4', 'webm', 'mov', 'mkv', 'avi'].includes(ext)
  const isTextCode = ['txt', 'md', 'json', 'js', 'ts', 'jsx', 'tsx', 'go', 'py', 'html', 'css', 'sh', 'sql', 'yaml', 'yml'].includes(ext)

  useEffect(() => {
    if (!open || !file || file.is_directory) {
      setBlobUrl(null)
      setTextContent(null)
      setLoading(false)
      setError(null)
      return
    }

    let active = true
    let currentBlobUrl: string | null = null

    // Track view
    if (!publicToken) {
      apiClient.post(`/files/${file.id}/view`).catch(err => console.error("failed to track view", err))
    }

    async function loadPreview() {
      setLoading(true)
      setError(null)
      setBlobUrl(null)
      setTextContent(null)

      try {
        const token = getAccessToken()
        const url = publicToken ? `/public/shares/${publicToken}/download?inline=true` : `/files/${file!.id}/download?inline=true`
        const headers: Record<string, string> = {}
        if (!publicToken && token) {
          headers['Authorization'] = `Bearer ${token}`
        }

        const res = await apiClient.get(url, {
          responseType: isTextCode ? 'text' : 'blob',
          headers,
        })

        if (!active) return

        if (isTextCode) {
          setTextContent(typeof res.data === 'string' ? res.data : JSON.stringify(res.data, null, 2))
        } else {
          const contentType = (res.headers['content-type'] as string) || 'application/octet-stream'
          const blob = new Blob([res.data], { type: contentType })
          currentBlobUrl = URL.createObjectURL(blob)
          setBlobUrl(currentBlobUrl)
        }
      } catch {
        if (!active) return
        setError('Unable to stream file preview.')
      } finally {
        if (active) setLoading(false)
      }
    }

    void loadPreview()

    return () => {
      active = false
      if (currentBlobUrl) URL.revokeObjectURL(currentBlobUrl)
    }
  }, [open, file, isTextCode])

  if (!file) return null

  const handleCopyText = () => {
    if (!textContent) return
    void navigator.clipboard.writeText(textContent)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      label={`Preview ${file.name}`}
      maxWidthClass="max-w-4xl w-[92vw] h-[85vh]"
    >
      {/* Header bar */}
      <div className="flex items-center justify-between border-b border-slate-200 pb-3 dark:border-zinc-800">
        <div className="flex items-center gap-3 min-w-0 pr-4">
          <FileIcon filename={file.name} isDirectory={file.is_directory} size={22} />
          <div className="min-w-0">
            <h2 className="truncate text-base font-semibold text-slate-900 dark:text-zinc-50">{file.name}</h2>
            <p className="text-xs text-slate-500 dark:text-zinc-400">
              {formatFileSize(file.size_bytes)} • Modified {formatDate(file.updated_at)}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2 shrink-0">
          {isTextCode && textContent && (
            <Button variant="secondary" className="py-1 px-3 text-xs" onClick={handleCopyText}>
              {copied ? 'Copied!' : 'Copy Code'}
            </Button>
          )}
          {onDownload && (
            <Button variant="primary" className="py-1 px-3 text-xs" onClick={() => onDownload(file)}>
              Download
            </Button>
          )}
          <button
            onClick={onClose}
            className="rounded-lg p-1.5 text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
            aria-label="Close preview"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M18 6L6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
      </div>

      {/* Main Preview Container */}
      <div className="relative mt-4 flex h-[calc(85vh-100px)] w-full items-center justify-center overflow-hidden rounded-xl border border-slate-200 bg-slate-100/50 dark:border-zinc-800/80 dark:bg-zinc-950/80">
        {loading && (
          <div className="flex flex-col items-center gap-3 text-zinc-400">
            <Spinner size={24} />
            <span className="text-xs font-medium">Loading preview stream...</span>
          </div>
        )}

        {!loading && error && (
          <div className="flex flex-col items-center gap-3 text-center px-4">
            <div className="rounded-full bg-rose-500/10 p-3 text-rose-400">
              <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <circle cx="12" cy="12" r="10" />
                <line x1="12" y1="8" x2="12" y2="12" />
                <line x1="12" y1="16" x2="12.01" y2="16" />
              </svg>
            </div>
            <p className="text-sm text-zinc-300">{error}</p>
            {onDownload && (
              <Button variant="secondary" className="py-1 px-3 text-xs" onClick={() => onDownload(file)}>
                Download File Instead
              </Button>
            )}
          </div>
        )}

        {!loading && !error && (
          <>
            {/* Image Preview */}
            {isImage && blobUrl && (
              <div className="flex h-full w-full items-center justify-center p-4">
                <img
                  src={blobUrl}
                  alt={file.name}
                  className="max-h-full max-w-full object-contain rounded-lg shadow-2xl"
                />
              </div>
            )}

            {/* PDF Preview */}
            {isPdf && blobUrl && (
              <iframe
                src={blobUrl}
                title={file.name}
                className="h-full w-full border-0 bg-zinc-900 rounded-lg"
              />
            )}

            {/* Audio Preview */}
            {isAudio && blobUrl && (
              <div className="flex flex-col items-center gap-6 p-8 text-center">
                <div className="flex h-24 w-24 items-center justify-center rounded-full bg-fuchsia-500/10 text-fuchsia-400 shadow-xl">
                  <FileIcon filename={file.name} size={48} />
                </div>
                <audio controls src={blobUrl} className="w-80 max-w-full" autoPlay />
              </div>
            )}

            {/* Video Preview */}
            {isVideo && blobUrl && (
              <div className="flex h-full w-full items-center justify-center p-2">
                <video controls src={blobUrl} className="max-h-full max-w-full rounded-lg shadow-2xl" autoPlay />
              </div>
            )}

            {/* Text & Code Preview */}
            {isTextCode && textContent !== null && (
              <div className="h-full w-full overflow-auto p-4 text-left font-mono text-xs text-zinc-200 bg-zinc-900/90 leading-relaxed selection:bg-violet-500 selection:text-white">
                <pre className="whitespace-pre-wrap break-words">{textContent}</pre>
              </div>
            )}

            {/* Unsupported / Binary File Fallback */}
            {!isImage && !isPdf && !isAudio && !isVideo && !isTextCode && (
              <div className="flex flex-col items-center gap-4 text-center px-6">
                <div className="flex h-20 w-20 items-center justify-center rounded-2xl bg-zinc-900 border border-zinc-800 shadow-lg">
                  <FileIcon filename={file.name} size={40} />
                </div>
                <div>
                  <h3 className="text-base font-medium text-zinc-100">{file.name}</h3>
                  <p className="mt-1 text-xs text-zinc-500">
                    No inline preview available for this file type ({ext.toUpperCase() || 'Binary'}).
                  </p>
                </div>
                {onDownload && (
                  <Button variant="primary" onClick={() => onDownload(file)}>
                    Download File ({formatFileSize(file.size_bytes)})
                  </Button>
                )}
              </div>
            )}
          </>
        )}
      </div>
    </Modal>
  )
}

export default FilePreviewModal
