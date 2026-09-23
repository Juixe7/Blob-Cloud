import { useState, useEffect, useMemo } from 'react'
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

  const effectiveTopLevelJobs = useMemo(() => {
    const topLevel = jobs.filter((j) => !j.folder_job_id)
    return topLevel.map((job) => {
      if (job.is_folder) {
        const children = jobs.filter((j) => j.folder_job_id === job.id)
        if (children.length > 0) {
          const totalProgress = children.reduce((acc, c) => acc + c.progress, 0)
          const avgProgress = totalProgress / children.length
          const isFailed = children.some((c) => c.status === 'FAILED')
          const isCompleted = children.every((c) => c.status === 'COMPLETED' || c.status === 'FAILED')
          const aggStatus: UploadStatus = isFailed ? 'FAILED' : isCompleted ? 'COMPLETED' : 'UPLOADING'
          return {
            ...job,
            progress: avgProgress,
            status: aggStatus,
            error: isFailed ? 'Some files failed to upload' : undefined,
          }
        }
      }
      return job
    })
  }, [jobs])

  const activeCount = effectiveTopLevelJobs.filter((j) => !isTerminal(j.status)).length
  const allTerminal = effectiveTopLevelJobs.length > 0 && activeCount === 0

  useEffect(() => {
    if (allTerminal) {
      const timer = setTimeout(() => {
        clearCompleted()
      }, 2500) // Disappear fast
      return () => clearTimeout(timer)
    }
  }, [allTerminal, clearCompleted])

  if (jobs.length === 0) return null

  return (
    <div
      className="fixed top-20 right-6 z-50 w-72 animate-fade-in font-sans select-none"
      role="region"
      aria-label="Upload queue"
    >
      <div className="overflow-hidden rounded border border-arch-border bg-arch-900 shadow-sharp">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-arch-border px-3.5 py-2.5 bg-arch-950">
          <div className="flex items-center gap-2">
            {activeCount > 0 && <Spinner size={12} className="text-amber-400" />}
            <h2 className="font-mono text-xs font-semibold text-zinc-100 uppercase tracking-wider">
              {activeCount > 0 ? `UPLOADING (${activeCount})` : 'UPLOAD QUEUE'}
            </h2>
          </div>
          <div className="flex items-center gap-1">
            {effectiveTopLevelJobs.some((j) => isTerminal(j.status)) && (
              <button
                onClick={clearCompleted}
                className="rounded px-2 py-0.5 font-mono text-[10px] text-zinc-400 transition-colors hover:bg-arch-850 hover:text-zinc-200"
                aria-label="Clear completed uploads"
              >
                CLEAR
              </button>
            )}
            <button
              onClick={() => setCollapsed((c) => !c)}
              className="rounded p-1 text-zinc-400 transition-colors hover:bg-arch-850 hover:text-zinc-200"
              aria-label={collapsed ? 'Expand upload queue' : 'Collapse upload queue'}
              aria-expanded={!collapsed}
            >
              <svg
                width="12"
                height="12"
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
          <ul className="max-h-72 divide-y divide-arch-border/50 overflow-y-auto">
            {effectiveTopLevelJobs.map((job) => (
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
    <li className="px-3.5 py-2.5">
      <div className="flex items-start gap-2.5">
        {/* Status icon */}
        <StatusIcon status={status} />

        {/* Body */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-xs font-medium text-zinc-200" title={filename}>
              {filename} {job.is_folder && job.file_count && `(${job.file_count} files)`}
            </span>
            <span className="shrink-0 font-mono text-[10px] text-zinc-500">
              {formatFileSize(totalSize)}
            </span>
          </div>

          {/* Status text */}
          <p className="mt-0.5 font-mono text-[10px] text-zinc-400">
            {statusText(status, progress, error)}
          </p>

          {/* Progress bar */}
          <div className="mt-1.5 h-1 w-full overflow-hidden rounded-xs bg-arch-950 border border-arch-border">
            <div
              className={cn(
                'h-full transition-all duration-200',
                status === 'FAILED' ? 'bg-rose-500' : 'bg-amber-500',
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
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="mt-0.5 shrink-0 text-zinc-400"
        aria-hidden="true"
      >
        <polyline points="20,6 9,17 4,12" />
      </svg>
    )
  }
  if (status === 'FAILED') {
    return (
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="mt-0.5 shrink-0 text-rose-400"
        aria-hidden="true"
      >
        <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
        <line x1="12" y1="9" x2="12" y2="13" />
        <line x1="12" y1="17" x2="12.01" y2="17" />
      </svg>
    )
  }
  return <Spinner size={14} className="mt-0.5 shrink-0 text-amber-400" />
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
