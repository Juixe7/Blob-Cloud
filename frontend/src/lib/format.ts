import { format, formatDistanceToNow } from 'date-fns'

/**
 * Format a byte count into a human-readable string.
 * Uses binary-style thresholds (1 KB = 1024 B) for consistency with
 * most file explorers.
 */
export function formatFileSize(bytes: number): string {
  if (bytes === 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB'] as const
  let size = bytes
  let unitIndex = 0
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex++
  }
  return `${size.toFixed(unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`
}

/**
 * Format an ISO date string for the "Date Modified" column.
 * Shows relative time for recent items (e.g. "3 hours ago"), absolute date
 * for older items.
 */
export function formatDate(iso: string): string {
  try {
    const date = new Date(iso)
    const now = Date.now()
    const diffMs = now - date.getTime()
    const sevenDays = 7 * 24 * 60 * 60 * 1000

    if (diffMs < sevenDays && diffMs >= 0) {
      return formatDistanceToNow(date, { addSuffix: true })
    }
    return format(date, 'MMM d, yyyy')
  } catch {
    return '—'
  }
}

/** All possible file icon variants understood by the <FileIcon> component. */
export type IconVariant =
  | 'folder' | 'file' | 'img' | 'pdf' | 'doc' | 'xls' | 'csv' | 'ppt'
  | 'video' | 'audio' | 'archive' | 'code' | 'txt' | 'md'

/**
 * Determine an icon variant for a file based on its extension.
 * Directories always return 'folder'.
 */
export function getFileIcon(name: string, isDirectory: boolean): IconVariant {
  if (isDirectory) return 'folder'
  const ext = name.split('.').pop()?.toLowerCase() ?? ''
  const map: Record<string, IconVariant> = {
    pdf: 'pdf',
    doc: 'doc', docx: 'doc',
    xls: 'xls', xlsx: 'xls', csv: 'csv',
    ppt: 'ppt', pptx: 'ppt',
    jpg: 'img', jpeg: 'img', png: 'img', gif: 'img', svg: 'img', webp: 'img',
    mp4: 'video', mov: 'video', avi: 'video', mkv: 'video',
    mp3: 'audio', wav: 'audio', flac: 'audio',
    zip: 'archive', rar: 'archive', '7z': 'archive', tar: 'archive', gz: 'archive',
    js: 'code', ts: 'code', jsx: 'code', tsx: 'code', py: 'code', go: 'code',
    json: 'code', yaml: 'code', yml: 'code', toml: 'code',
    txt: 'txt', md: 'md',
  }
  return map[ext] ?? 'file'
}

/**
 * Combine clsx + tailwind-merge into a single `cn()` helper for conditional
 * Tailwind classes (avoids string concatenation footguns).
 */
import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}
