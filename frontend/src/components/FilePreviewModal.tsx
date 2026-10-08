import { useEffect, useState } from 'react'
import { apiClient } from '../lib/api'
import { getAccessToken } from '../lib/token'
import { decryptEncryptedStream } from '../lib/download'
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
  shareSessionToken?: string | null
}

/**
 * Full-screen Google Drive style in-browser file preview modal.
 * Supports inline rendering for Images, PDFs, Audio, Video, and Code/Text files.
 */
export function FilePreviewModal({ open, onClose, file, onDownload, publicToken, shareSessionToken }: FilePreviewModalProps) {
  const [blobUrl, setBlobUrl] = useState<string | null>(null)
  const [textContent, setTextContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  // E2EE decryption state
  const [passphrase, setPassphrase] = useState('')
  const [isDecrypting, setIsDecrypting] = useState(false)
  const [decryptionError, setDecryptionError] = useState<string | null>(null)
  const [needsPassphrase, setNeedsPassphrase] = useState(false)

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
      setPassphrase('')
      setDecryptionError(null)
      setNeedsPassphrase(false)
      return
    }

    if (file.is_encrypted) {
      setBlobUrl(null)
      setTextContent(null)
      setLoading(false)
      setError(null)
      setPassphrase('')
      setDecryptionError(null)
      setNeedsPassphrase(true)
      return
    }

    setNeedsPassphrase(false)
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
        const url = publicToken
          ? `/public/shares/${publicToken}/download?inline=true${shareSessionToken ? `&share_token=${encodeURIComponent(shareSessionToken)}` : ''}`
          : `/files/${file!.id}/download?inline=true`
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
  }, [open, file, isTextCode, publicToken, shareSessionToken])

  const handleDecryptSubmit = async (e?: React.FormEvent) => {
    if (e) e.preventDefault()
    if (!file || !passphrase) return

    setIsDecrypting(true)
    setDecryptionError(null)
    setError(null)

    try {
      const token = getAccessToken()
      const url = publicToken
        ? `/public/shares/${publicToken}/download${shareSessionToken ? `?share_token=${encodeURIComponent(shareSessionToken)}` : ''}`
        : `/files/${file.id}/download`
      const headers: Record<string, string> = {}
      if (!publicToken && token) {
        headers['Authorization'] = `Bearer ${token}`
      }

      const res = await apiClient.get<ArrayBuffer>(url, {
        responseType: 'arraybuffer',
        headers,
      })

      const decryptedChunks = await decryptEncryptedStream(res.data, passphrase)

      if (isTextCode) {
        const decoder = new TextDecoder()
        const text = decryptedChunks.map((c) => decoder.decode(c, { stream: true })).join('') + decoder.decode()
        setTextContent(text)
      } else {
        const mimeType = isImage
          ? `image/${ext === 'svg' ? 'svg+xml' : ext}`
          : isPdf
          ? 'application/pdf'
          : isAudio
          ? `audio/${ext}`
          : isVideo
          ? `video/${ext}`
          : 'application/octet-stream'

        const blob = new Blob(decryptedChunks, { type: mimeType })
        const currentUrl = URL.createObjectURL(blob)
        setBlobUrl(currentUrl)
      }

      setNeedsPassphrase(false)
    } catch (err) {
      console.error('Decryption failed:', err)
      setDecryptionError('Decryption failed. Please check your passphrase.')
    } finally {
      setIsDecrypting(false)
    }
  }

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
            <div className="flex items-center gap-2">
              <h2 className="truncate text-base font-semibold text-slate-900 dark:text-zinc-50">{file.name}</h2>
              {file.is_encrypted && (
                <span className="inline-flex items-center gap-1 rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-medium text-amber-500 border border-amber-500/20 shrink-0">
                  🔒 E2EE Encrypted
                </span>
              )}
            </div>
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
        {needsPassphrase && (
          <div className="flex flex-col items-center justify-center p-6 text-center max-w-md w-full">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-amber-500/10 text-amber-500 border border-amber-500/20 shadow-lg">
              <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
                <path d="M7 11V7a5 5 0 0 1 10 0v4" />
              </svg>
            </div>
            <h3 className="text-base font-semibold text-slate-900 dark:text-zinc-100">
              End-to-End Encrypted File
            </h3>
            <p className="mt-1.5 text-xs text-slate-500 dark:text-zinc-400">
              Enter your passphrase to decrypt and preview this file directly in your browser.
            </p>

            {file.size_bytes > 150 * 1024 * 1024 && (
              <div className="mt-3 rounded-lg border border-amber-500/30 bg-amber-500/5 p-2.5 text-left text-xs text-amber-600 dark:text-amber-400">
                ⚠️ Large file ({formatFileSize(file.size_bytes)}). In-browser preview may use high memory. We recommend downloading and decrypting directly to disk.
              </div>
            )}

            <form onSubmit={handleDecryptSubmit} className="mt-5 w-full space-y-3">
              <input
                type="password"
                value={passphrase}
                onChange={(e) => {
                  setPassphrase(e.target.value)
                  setDecryptionError(null)
                }}
                placeholder="Enter decryption passphrase"
                className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm placeholder:text-slate-400 focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:placeholder:text-zinc-500"
                autoFocus
              />

              {decryptionError && (
                <p className="text-xs text-rose-500 dark:text-rose-400">{decryptionError}</p>
              )}

              <div className="flex items-center gap-2 pt-1">
                <Button
                  type="submit"
                  variant="primary"
                  className="flex-1 py-2 text-xs"
                  disabled={!passphrase || isDecrypting}
                >
                  {isDecrypting ? (
                    <span className="flex items-center justify-center gap-2">
                      <Spinner size={14} /> Decrypting...
                    </span>
                  ) : (
                    'Decrypt & Preview'
                  )}
                </Button>
                {onDownload && (
                  <Button
                    type="button"
                    variant="secondary"
                    className="py-2 text-xs"
                    onClick={() => onDownload(file)}
                  >
                    Download Instead
                  </Button>
                )}
              </div>
            </form>
          </div>
        )}

        {!needsPassphrase && loading && (
          <div className="flex flex-col items-center gap-3 text-zinc-400">
            <Spinner size={24} />
            <span className="text-xs font-medium">Loading preview stream...</span>
          </div>
        )}

        {!needsPassphrase && !loading && error && (
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

        {!needsPassphrase && !loading && !error && (
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
              <div className="h-full w-full overflow-auto p-4 text-left font-mono text-xs text-zinc-200 bg-zinc-900/90 leading-relaxed selection:bg-amber-500/40 selection:text-white">
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
