import { Modal } from './ui/Modal'
import { Button } from './ui/Button'
import { FileIcon } from './FileIcon'

interface UploadConflictModalProps {
  open: boolean
  onClose: () => void
  fileName: string
  onUpdateVersion: () => void
  onKeepBoth: () => void
}

export function UploadConflictModal({
  open,
  onClose,
  fileName,
  onUpdateVersion,
  onKeepBoth,
}: UploadConflictModalProps) {
  if (!open) return null

  // Calculate preview of renamed file
  const dotIndex = fileName.lastIndexOf('.')
  const renamedPreview = dotIndex !== -1
    ? `${fileName.slice(0, dotIndex)} (1)${fileName.slice(dotIndex)}`
    : `${fileName} (1)`

  return (
    <Modal open={open} onClose={onClose} label="File Already Exists" maxWidthClass="max-w-md">
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-amber-500/10 text-amber-500 dark:bg-amber-500/20">
            <FileIcon filename={fileName} isDirectory={false} className="h-6 w-6" />
          </div>
          <div>
            <h2 className="text-lg font-bold text-slate-900 dark:text-zinc-50">File Already Exists</h2>
            <p className="text-xs text-slate-500 dark:text-zinc-400">
              An item named <span className="font-semibold text-slate-800 dark:text-zinc-200">"{fileName}"</span> already exists in this folder.
            </p>
          </div>
        </div>

        <div className="rounded-xl border border-slate-200 bg-slate-50 p-3 text-xs text-slate-600 dark:border-zinc-800 dark:bg-zinc-900/60 dark:text-zinc-400 space-y-2">
          <p>Choose how you would like to handle this file:</p>
          <ul className="list-disc pl-4 space-y-1 text-[11px]">
            <li><strong>Update Version:</strong> Archives the existing file as a previous version and updates to the latest version. Keeps all active share links intact.</li>
            <li><strong>Keep Both:</strong> Saves the file as <span className="font-semibold text-slate-700 dark:text-zinc-300">"{renamedPreview}"</span>.</li>
          </ul>
        </div>

        <div className="flex flex-col gap-2 pt-2">
          <Button
            variant="primary"
            onClick={() => {
              onUpdateVersion()
              onClose()
            }}
          >
            Update Existing File (New Version)
          </Button>

          <Button
            variant="secondary"
            onClick={() => {
              onKeepBoth()
              onClose()
            }}
          >
            Keep Both (Separate Copy)
          </Button>

          <Button
            variant="ghost"
            className="text-xs text-slate-500 hover:text-slate-700 dark:text-zinc-400"
            onClick={onClose}
          >
            Cancel Upload
          </Button>
        </div>
      </div>
    </Modal>
  )
}
export default UploadConflictModal
