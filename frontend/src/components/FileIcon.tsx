import type { ReactElement } from 'react'
import { cn } from '../lib/format'
import type { IconVariant } from '../lib/format'

interface FileIconProps {
  filename?: string
  isDirectory?: boolean
  variant?: IconVariant
  className?: string
  size?: number
  isShortcut?: boolean
  isBrokenShortcut?: boolean
}

/**
 * Returns extension-aware, color-coded SVG file icons matching modern design standards.
 * Categories: Folder (Gold), PDF (Red), Image (Purple), Doc (Blue), Sheet (Green),
 * Audio (Pink), Video (Orange), Code/Archive (Cyan), Fallback File (Zinc).
 */
export function FileIcon({
  filename = '',
  isDirectory = false,
  variant,
  className = '',
  size = 20,
  isShortcut = false,
  isBrokenShortcut = false,
}: FileIconProps): ReactElement {
  // Deduce effective variant from props
  let effectiveVariant: IconVariant = 'file'

  if (variant) {
    effectiveVariant = variant
  } else if (isDirectory) {
    effectiveVariant = 'folder'
  } else if (filename) {
    const ext = filename.split('.').pop()?.toLowerCase() ?? ''
    const map: Record<string, IconVariant> = {
      pdf: 'pdf',
      doc: 'doc', docx: 'doc',
      xls: 'xls', xlsx: 'xls', csv: 'csv',
      ppt: 'ppt', pptx: 'ppt',
      jpg: 'img', jpeg: 'img', png: 'img', gif: 'img', svg: 'img', webp: 'img',
      mp4: 'video', mov: 'video', avi: 'video', mkv: 'video', webm: 'video',
      mp3: 'audio', wav: 'audio', flac: 'audio', m4a: 'audio',
      zip: 'archive', rar: 'archive', '7z': 'archive', tar: 'archive', gz: 'archive',
      js: 'code', ts: 'code', jsx: 'code', tsx: 'code', py: 'code', go: 'code',
      json: 'code', yaml: 'code', yml: 'code', html: 'code', css: 'code',
      txt: 'txt', md: 'md',
    }
    effectiveVariant = map[ext] ?? 'file'
  }

  // Render Gold Folder
  if (effectiveVariant === 'folder') {
    return (
      <div className={cn("relative inline-flex items-center justify-center", isBrokenShortcut && "grayscale opacity-50")} title={isBrokenShortcut ? "Shortcut target was deleted." : undefined}>
        <svg
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          className={cn('text-amber-400 shrink-0', className)}
          aria-label="Folder icon"
        >
          <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" fill="currentColor" fillOpacity="0.18" />
        </svg>
        {isShortcut && <ShortcutOverlay isBroken={isBrokenShortcut} />}
      </div>
    )
  }

  // Render Crimson Red PDF
  if (effectiveVariant === 'pdf') {
    return (
      <div className={cn("relative inline-flex items-center justify-center", isBrokenShortcut && "grayscale opacity-50")} title={isBrokenShortcut ? "Shortcut target was deleted." : undefined}>
        <svg
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          className={cn('text-rose-500 shrink-0', className)}
          aria-label="PDF file"
        >
          <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" fill="currentColor" fillOpacity="0.15" />
          <polyline points="14 2 14 8 20 8" />
          <path d="M9 13h2a1.5 1.5 0 000-3H9v6" />
          <path d="M14 13h1a1.5 1.5 0 000-3h-1v6" />
        </svg>
        {isShortcut && <ShortcutOverlay isBroken={isBrokenShortcut} />}
      </div>
    )
  }

  // Render Purple Image
  if (effectiveVariant === 'img') {
    return (
      <div className={cn("relative inline-flex items-center justify-center", isBrokenShortcut && "grayscale opacity-50")} title={isBrokenShortcut ? "Shortcut target was deleted." : undefined}>
        <svg
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          className={cn('text-purple-400 shrink-0', className)}
          aria-label="Image file"
        >
          <rect x="3" y="3" width="18" height="18" rx="2" ry="2" fill="currentColor" fillOpacity="0.12" />
          <circle cx="8.5" cy="8.5" r="1.5" />
          <polyline points="21 15 16 10 5 21" />
        </svg>
        {isShortcut && <ShortcutOverlay isBroken={isBrokenShortcut} />}
      </div>
    )
  }

  // Render Royal Blue Document
  if (effectiveVariant === 'doc' || effectiveVariant === 'txt' || effectiveVariant === 'md') {
    return (
      <div className={cn("relative inline-flex items-center justify-center", isBrokenShortcut && "grayscale opacity-50")} title={isBrokenShortcut ? "Shortcut target was deleted." : undefined}>
        <svg
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          className={cn('text-blue-400 shrink-0', className)}
          aria-label="Document file"
        >
          <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" fill="currentColor" fillOpacity="0.12" />
          <polyline points="14 2 14 8 20 8" />
          <line x1="16" y1="13" x2="8" y2="13" />
          <line x1="16" y1="17" x2="8" y2="17" />
          <polyline points="10 9 9 9 8 9" />
        </svg>
        {isShortcut && <ShortcutOverlay isBroken={isBrokenShortcut} />}
      </div>
    )
  }

  // Render Emerald Green Spreadsheet
  if (effectiveVariant === 'xls' || effectiveVariant === 'csv') {
    return (
      <div className={cn("relative inline-flex items-center justify-center", isBrokenShortcut && "grayscale opacity-50")} title={isBrokenShortcut ? "Shortcut target was deleted." : undefined}>
        <svg
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          className={cn('text-emerald-400 shrink-0', className)}
          aria-label="Spreadsheet file"
        >
          <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" fill="currentColor" fillOpacity="0.12" />
          <polyline points="14 2 14 8 20 8" />
          <path d="M8 13h8M8 17h8M12 10v10" />
        </svg>
        {isShortcut && <ShortcutOverlay isBroken={isBrokenShortcut} />}
      </div>
    )
  }

  // Render Fuchsia Audio
  if (effectiveVariant === 'audio') {
    return (
      <div className={cn("relative inline-flex items-center justify-center", isBrokenShortcut && "grayscale opacity-50")} title={isBrokenShortcut ? "Shortcut target was deleted." : undefined}>
        <svg
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          className={cn('text-fuchsia-400 shrink-0', className)}
          aria-label="Audio file"
        >
          <path d="M9 18V5l12-2v13" />
          <circle cx="6" cy="18" r="3" fill="currentColor" fillOpacity="0.25" />
          <circle cx="18" cy="16" r="3" fill="currentColor" fillOpacity="0.25" />
        </svg>
        {isShortcut && <ShortcutOverlay isBroken={isBrokenShortcut} />}
      </div>
    )
  }

  // Render Amber Video
  if (effectiveVariant === 'video') {
    return (
      <div className={cn("relative inline-flex items-center justify-center", isBrokenShortcut && "grayscale opacity-50")} title={isBrokenShortcut ? "Shortcut target was deleted." : undefined}>
        <svg
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          className={cn('text-orange-400 shrink-0', className)}
          aria-label="Video file"
        >
          <rect x="2" y="4" width="20" height="16" rx="2" fill="currentColor" fillOpacity="0.12" />
          <polygon points="10 8 16 12 10 16 10 8" fill="currentColor" />
        </svg>
        {isShortcut && <ShortcutOverlay isBroken={isBrokenShortcut} />}
      </div>
    )
  }

  // Render Cyan Code
  if (effectiveVariant === 'code') {
    return (
      <div className={cn("relative inline-flex items-center justify-center", isBrokenShortcut && "grayscale opacity-50")} title={isBrokenShortcut ? "Shortcut target was deleted." : undefined}>
        <svg
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          className={cn('text-cyan-400 shrink-0', className)}
          aria-label="Code file"
        >
          <polyline points="16 18 22 12 16 6" />
          <polyline points="8 6 2 12 8 18" />
        </svg>
        {isShortcut && <ShortcutOverlay isBroken={isBrokenShortcut} />}
      </div>
    )
  }

  // Render Teal Archive
  if (effectiveVariant === 'archive') {
    return (
      <div className={cn("relative inline-flex items-center justify-center", isBrokenShortcut && "grayscale opacity-50")} title={isBrokenShortcut ? "Shortcut target was deleted." : undefined}>
        <svg
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          className={cn('text-teal-400 shrink-0', className)}
          aria-label="Archive file"
        >
          <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" fill="currentColor" fillOpacity="0.12" />
          <polyline points="14 2 14 8 20 8" />
          <circle cx="12" cy="14" r="2" />
          <path d="M12 10v2" />
        </svg>
        {isShortcut && <ShortcutOverlay isBroken={isBrokenShortcut} />}
      </div>
    )
  }

  // Fallback Generic File
  return (
    <div className={cn("relative inline-flex items-center justify-center", isBrokenShortcut && "grayscale opacity-50")} title={isBrokenShortcut ? "Shortcut target was deleted." : undefined}>
      <svg
        width={size}
        height={size}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.75"
        strokeLinecap="round"
        strokeLinejoin="round"
        className={cn('text-zinc-400 shrink-0', className)}
        aria-label="File icon"
      >
        <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" fill="currentColor" fillOpacity="0.1" />
        <polyline points="14 2 14 8 20 8" />
      </svg>
      {isShortcut && <ShortcutOverlay isBroken={isBrokenShortcut} />}
    </div>
  )
}

function ShortcutOverlay({ isBroken = false }: { isBroken?: boolean }) {
  if (isBroken) {
    return (
      <div className="absolute -bottom-1 -left-1 rounded-full bg-arch-900 p-[1px] text-red-500 shadow-[0_0_8px_rgba(255,0,0,0.4)]" title="Shortcut target was deleted.">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" stroke="none">
          <path d="M12 2L1 21h22L12 2zm0 3.5l7.5 13h-15L12 5.5zm-1 5v4h2v-4h-2zm0 5v2h2v-2h-2z" />
        </svg>
      </div>
    )
  }
  return (
    <div className="absolute -bottom-1 -left-1 rounded-full bg-arch-900 p-[1px] text-zinc-100 shadow-[0_0_8px_rgba(255,255,255,0.4)]">
      <svg
        width="10"
        height="10"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="3"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
      </svg>
    </div>
  )
}

export default FileIcon
