import { useState } from 'react'
import { useUpload } from '../context/UploadContext'
import { formatFileSize, cn } from '../lib/format'
import { Spinner } from './ui/Spinner'
import type { UploadJob, UploadStatus } from '../types/file'

/**
 * Floating upload-queue overlay, fixed at the bottom-right of the screen.
 * Shows one row per active/recent upload with aggregate progress and status.
 * Collapsible; auto-hides when the queue is empty.
 */
export function UploadQueue() {
  const { jobs, clearCompleted } = useUpload()
  const [collapsed, setCollapsed] = useState(false)

  // Don't render anything when there are no jobs.
  if (jobs.length === 0) return null

  const activeCount = jobs.filter((j) => !isTerminal(j.status)).length

  return (
    <div
      className="fixed bottom-4 right-4 z-50 w-80 animate-fade-in"
      role="region"
      aria-label="Upload queue"
    >
      <div className="overflow-hidden rounded-lg border border-zinc-800 bg-zinc-900 shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-zinc-800 px-4 py-3">
          <div className="flex items-center gap-2">
            {activeCount > 0 && <Spinner size={14} className="text-violet-400" />}
            <h2 className="text-sm font-semibold text-zinc-50">
              {activeCount > 0 ? `Uploading ${activeCount} file${activeCount > 1 ? 's' : ''}` : 'Uploads'}
            </h2>
          </div>
          <div className="flex items-center gap-1">
            {jobs.some((j) => isTerminal(j.status)) && (
              <button
                onClick={clearCompleted}
                className="rounded px-2 py-1 text-xs text-zinc-500 transition-colors hover:bg-zinc-800 hover:text-zinc-300"
                aria-label="Clear completed uploads"
              >
                Clear
              </button>
            )}
            <button
              onClick={() => setCollapsed((c) => !c)}
              className="rounded p-1 text-zinc-500 transition-colors hover:bg-zinc-800 hover:text-zinc-300"
              aria-label={collapsed ? 'Expand upload queue' : 'Collapse upload queue'}
              aria-expanded={!collapsed}
            >
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                className={cn('transition-transform duration-200', collapsed && 'rotate-180')}
                aria-hidden="true"
              >
                <polyline points="6,9 12,15 18,9" />
              </svg>
            </button>
          </div>
        </div>

        {/* Rows */}
        {!collapsed && (
          <ul className="max-h-80 divide-y divide-zinc-800/60 overflow-y-auto">
            {jobs.map((job) => (
              <JobRow key={job.id} job={job} />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

/** A single upload row with status text, progress bar, and indicator icon. */
function JobRow({ job }: { job: UploadJob }) {
  const { filename, totalSize, status, progress, error } = job

  return (
    <li className="px-4 py-3">
      <div className="flex items-start gap-3">
        {/* Status icon */}
        <StatusIcon status={status} />

        {/* Body */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-sm text-zinc-200" title={filename}>
              {filename}
            </span>
            <span className="shrink-0 text-xs text-zinc-500">
              {formatFileSize(totalSize)}
            </span>
          </div>

          {/* Status text */}
          <p className="mt-0.5 text-xs text-zinc-500">
            {statusText(status, progress, error)}
          </p>

          {/* Progress bar */}
          <div className="mt-2 h-1 w-full overflow-hidden rounded-full bg-zinc-800">
            <div
              className={cn(
                'h-full rounded-full transition-all duration-300',
                status === 'FAILED' ? 'bg-red-500' : 'bg-violet-500',
              )}
              style={{ width: `${status === 'FAILED' ? 100 : Math.max(2, progress)}%` }}
              role="progressbar"
              aria-valuenow={Math.round(progress)}
              aria-valuemin={0}
              aria-valuemax={100}
            />
          </div>
        </div>
      </div>
    </li>
  )
}

/** Inline status icon: spinner for active, check for done, warning for failed. */
function StatusIcon({ status }: { status: UploadStatus }) {
  if (status === 'COMPLETED') {
    return (
      <svg
        width="16"
        height="16"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="mt-0.5 shrink-0 text-green-400"
        aria-hidden="true"
      >
        <polyline points="20,6 9,17 4,12" />
      </svg>
    )
  }
  if (status === 'FAILED') {
    return (
      <svg
        width="16"
        height="16"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="mt-0.5 shrink-0 text-red-400"
        aria-hidden="true"
      >
        <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
        <line x1="12" y1="9" x2="12" y2="13" />
        <line x1="12" y1="17" x2="12.01" y2="17" />
      </svg>
    )
  }
  return <Spinner size={16} className="mt-0.5 shrink-0 text-violet-400" />
}

/** Human-readable status line per phase. */
function statusText(status: UploadStatus, progress: number, error?: string): string {
  switch (status) {
    case 'HASHING':
      return 'Hashing…'
    case 'INITIATING':
      return 'Preparing upload…'
    case 'UPLOADING':
      return `Uploading ${Math.round(progress)}%`
    case 'COMPLETING':
      return 'Finalizing…'
    case 'COMPLETED':
      return 'Completed'
    case 'FAILED':
      return error ?? 'Failed'
    default:
      return ''
  }
}

/** True for states that no longer change (completed or failed). */
function isTerminal(status: UploadStatus): boolean {
  return status === 'COMPLETED' || status === 'FAILED'
}

export default UploadQueue
