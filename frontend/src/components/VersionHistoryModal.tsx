import { useState, useEffect } from 'react'
import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { Spinner } from './ui/Spinner'
import { formatDate, formatFileSize } from '../lib/format'
import { apiClient } from '../lib/api'
import { useToast } from './Toast'
import type { FileItem } from '../types/file'

export interface FileVersion {
  id: string
  file_id: string
  version_number: number
  size_bytes: number
  created_at: string
}

interface VersionHistoryModalProps {
  open: boolean
  onClose: () => void
  file: FileItem | null
  onRestoreComplete: () => void
}

export function VersionHistoryModal({
  open,
  onClose,
  file,
  onRestoreComplete,
}: VersionHistoryModalProps) {
  const [versions, setVersions] = useState<FileVersion[]>([])
  const [loading, setLoading] = useState(false)
  const [restoring, setRestoring] = useState<string | null>(null)
  const { push: pushToast } = useToast()

  useEffect(() => {
    if (open && file) {
      setLoading(true)
      apiClient
        .get<{ versions: FileVersion[] }>(`/files/${file.id}/versions`)
        .then((res) => setVersions(res.data.versions || []))
        .catch(() => pushToast({ message: 'Failed to load version history.', variant: 'error' }))
        .finally(() => setLoading(false))
    } else {
      setVersions([])
    }
  }, [open, file])

  const handleRestore = async (versionId: string) => {
    if (!file) return
    setRestoring(versionId)
    try {
      await apiClient.post(`/files/${file.id}/versions/${versionId}/restore`)
      pushToast({ message: 'Version restored successfully.', variant: 'success' })
      onRestoreComplete()
      onClose()
    } catch (err: any) {
      const msg = err.response?.data?.error || 'Failed to restore version.'
      pushToast({ message: msg, variant: 'error' })
    } finally {
      setRestoring(null)
    }
  }

  if (!open || !file) return null

  return (
    <Modal open={open} onClose={onClose} label="Version History" maxWidthClass="max-w-xl">
      <div className="space-y-5">
        <div>
          <h2 className="text-xl font-bold text-slate-900 dark:text-zinc-50">Version History</h2>
          <p className="text-xs text-slate-500 dark:text-zinc-400 mt-1">
            Restore previous versions of <span className="font-semibold text-slate-700 dark:text-zinc-300">{file.name}</span>.
          </p>
        </div>

        {loading ? (
          <div className="flex h-32 items-center justify-center rounded-xl border border-slate-200 bg-slate-50 dark:border-zinc-800 dark:bg-zinc-900/60">
            <Spinner size={24} />
          </div>
        ) : versions.length === 0 ? (
          <div className="rounded-xl border border-slate-200 bg-slate-50 p-6 text-xs text-slate-500 dark:border-zinc-800 dark:bg-zinc-900/60 text-center">
            No previous versions found for this file.
          </div>
        ) : (
          <div className="max-h-80 overflow-y-auto rounded-xl border border-slate-200 bg-slate-50 p-4 space-y-3 dark:border-zinc-800 dark:bg-zinc-900/60 divide-y divide-slate-200/60 dark:divide-zinc-800/60">
            {versions.map((v) => (
              <div key={v.id} className="flex items-center justify-between pt-3 first:pt-0">
                <div className="flex flex-col">
                  <span className="text-sm font-semibold text-slate-800 dark:text-zinc-200">
                    Version {v.version_number}
                  </span>
                  <div className="mt-1 flex items-center gap-2 text-[11px] text-slate-400 dark:text-zinc-500">
                    <span>{formatFileSize(v.size_bytes)}</span>
                    <span>•</span>
                    <span>{formatDate(v.created_at)}</span>
                  </div>
                </div>
                <div>
                  <Button
                    variant="secondary"
                    className="py-1 px-3 text-xs"
                    onClick={() => handleRestore(v.id)}
                    loading={restoring === v.id}
                    disabled={restoring !== null}
                  >
                    Restore
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}

        <div className="flex justify-end pt-3 border-t border-slate-200 dark:border-zinc-800">
          <Button variant="secondary" onClick={onClose}>
            Close
          </Button>
        </div>
      </div>
    </Modal>
  )
}
