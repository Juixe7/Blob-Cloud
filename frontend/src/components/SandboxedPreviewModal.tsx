import { useState } from 'react'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Spinner } from './ui/Spinner'
import { formatFileSize } from '../lib/format'
import { getAccessToken } from '../lib/token'
import { apiClient } from '../lib/api'
import type { ShareInvitation } from '../types/file'
import { FileIcon } from './FileIcon'

interface SandboxedPreviewModalProps {
  open: boolean
  onClose: () => void
  invitation: ShareInvitation | null
  onAccept: (invitation: ShareInvitation) => Promise<void>
  onDecline: (invitation: ShareInvitation) => Promise<void>
}

export function SandboxedPreviewModal({
  open,
  onClose,
  invitation,
  onAccept,
  onDecline,
}: SandboxedPreviewModalProps) {
  const [loadingAction, setLoadingAction] = useState<'accept' | 'decline' | null>(null)
  const [iframeLoading, setIframeLoading] = useState(true)

  if (!invitation) return null

  const token = getAccessToken() ?? ''
  const base = apiClient.defaults.baseURL ?? '/api'
  const previewUrl = `${base}/shares/invitations/${invitation.id}/preview?token=${encodeURIComponent(token)}`

  const isImage = /\.(jpg|jpeg|png|webp|gif|svg)$/i.test(invitation.file_name) || invitation.mime_type?.startsWith('image/')
  const isPdf = /\.pdf$/i.test(invitation.file_name) || invitation.mime_type === 'application/pdf'
  const isAudio = /\.(mp3|wav|ogg|m4a)$/i.test(invitation.file_name) || invitation.mime_type?.startsWith('audio/')
  const isVideo = /\.(mp4|webm|mov)$/i.test(invitation.file_name) || invitation.mime_type?.startsWith('video/')

  const handleAccept = async () => {
    setLoadingAction('accept')
    try {
      await onAccept(invitation)
      onClose()
    } finally {
      setLoadingAction(null)
    }
  }

  const handleDecline = async () => {
    setLoadingAction('decline')
    try {
      await onDecline(invitation)
      onClose()
    } finally {
      setLoadingAction(null)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      label="Protected Preview (Unaccepted File)"
      maxWidthClass="max-w-3xl"
    >
      <div className="space-y-4">
        {/* Header */}
        <div className="flex items-center justify-between pb-3 border-b border-zinc-800">
          <h2 className="text-sm font-semibold text-zinc-100">Protected Preview (Unaccepted File)</h2>
          <button
            type="button"
            onClick={onClose}
            className="rounded p-1 text-zinc-400 hover:text-white transition-colors"
          >
            ✕
          </button>
        </div>

        {/* Safety Warning Banner */}
        <div className="flex items-start gap-3 rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-xs text-amber-200">
          <svg className="h-5 w-5 flex-shrink-0 text-amber-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
          </svg>
          <div>
            <p className="font-semibold text-amber-300">Sandboxed Environment</p>
            <p className="mt-0.5 text-amber-200/80">
              Shared by <span className="font-medium text-white">{invitation.sender_email}</span>. Direct downloads and execution permissions are blocked until you accept this invitation.
            </p>
          </div>
        </div>

        {/* File Meta Summary */}
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-zinc-800 bg-zinc-900/60 px-3.5 py-2.5 text-xs text-zinc-300">
          <div className="flex items-center gap-2.5 min-w-0">
            <FileIcon filename={invitation.file_name} isDirectory={invitation.is_directory} size={18} />
            <span className="font-medium text-white truncate max-w-[240px] sm:max-w-md" title={invitation.file_name}>
              {invitation.file_name}
            </span>
            <span className="text-zinc-500">({formatFileSize(invitation.size_bytes)})</span>
          </div>
          <span className="rounded border border-indigo-500/30 bg-indigo-500/10 px-2 py-0.5 text-[11px] font-semibold text-indigo-300 uppercase tracking-wider">
            {invitation.role} Role
          </span>
        </div>

        {/* Sender Note */}
        {invitation.message && (
          <div className="rounded-lg border border-zinc-800/80 bg-zinc-900/30 p-3 text-xs">
            <span className="font-medium text-zinc-400">Note from sender:</span>
            <p className="mt-1 text-zinc-200 italic">"{invitation.message}"</p>
          </div>
        )}

        {/* AI Summary if present */}
        {invitation.summary && (
          <div className="rounded-lg border border-purple-500/20 bg-purple-500/5 p-3 text-xs">
            <div className="flex items-center gap-1.5 font-medium text-purple-300">
              <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M12 2v4m0 12v4M4.93 4.93l2.83 2.83m8.48 8.48l2.83 2.83M2 12h4m12 0h4M4.93 19.07l2.83-2.83m8.48-8.48l2.83-2.83" />
              </svg>
              <span>AI Content Summary</span>
            </div>
            <p className="mt-1 leading-relaxed text-zinc-300">{invitation.summary}</p>
          </div>
        )}

        {/* Sandboxed Viewer Container */}
        <div className="relative min-h-[350px] max-h-[500px] w-full overflow-hidden rounded-lg border border-zinc-800 bg-zinc-950 flex flex-col items-center justify-center">
          {iframeLoading && (
            <div className="absolute inset-0 flex items-center justify-center bg-zinc-950/80 z-10">
              <Spinner size={24} className="text-indigo-500" />
            </div>
          )}

          {isImage ? (
            <img
              src={previewUrl}
              alt={invitation.file_name}
              onLoad={() => setIframeLoading(false)}
              onError={() => setIframeLoading(false)}
              className="max-h-[480px] w-auto max-w-full object-contain p-2 select-none"
            />
          ) : isPdf ? (
            <iframe
              src={previewUrl}
              title={invitation.file_name}
              sandbox="allow-scripts"
              onLoad={() => setIframeLoading(false)}
              className="h-[480px] w-full border-none"
            />
          ) : isAudio ? (
            <div className="p-8 w-full max-w-md text-center" onLoadedData={() => setIframeLoading(false)}>
              <audio controls src={previewUrl} className="w-full" onCanPlay={() => setIframeLoading(false)}>
                Your browser does not support audio preview.
              </audio>
            </div>
          ) : isVideo ? (
            <video
              controls
              src={previewUrl}
              className="max-h-[480px] w-full"
              onCanPlay={() => setIframeLoading(false)}
            >
              Your browser does not support video preview.
            </video>
          ) : (
            <iframe
              src={previewUrl}
              title={invitation.file_name}
              sandbox="allow-same-origin"
              onLoad={() => setIframeLoading(false)}
              className="h-[450px] w-full border-none p-4 text-zinc-300 font-mono text-xs bg-zinc-950"
            />
          )}

          {/* Watermark */}
          <div className="pointer-events-none absolute bottom-3 right-3 rounded bg-zinc-900/90 px-2 py-1 text-[10px] font-mono tracking-wider text-zinc-500 border border-zinc-800">
            PROTECTED SANDBOX
          </div>
        </div>

        {/* Footer Actions */}
        <div className="mt-6 flex flex-wrap items-center justify-between gap-3 pt-2 border-t border-zinc-800">
          <Button variant="ghost" onClick={onClose} disabled={loadingAction !== null}>
            Cancel
          </Button>

          <div className="flex items-center gap-2.5">
            <Button
              variant="danger"
              onClick={handleDecline}
              loading={loadingAction === 'decline'}
              disabled={loadingAction !== null}
            >
              Decline Share
            </Button>
            <Button
              onClick={handleAccept}
              loading={loadingAction === 'accept'}
              disabled={loadingAction !== null}
              className="bg-indigo-600 hover:bg-indigo-500 text-white"
            >
              Accept & Add to Drive
            </Button>
          </div>
        </div>
      </div>
    </Modal>
  )
}
