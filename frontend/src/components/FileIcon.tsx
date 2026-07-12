import type { SVGAttributes } from 'react'
import type { IconVariant } from '../lib/format'

interface FileIconProps extends SVGAttributes<SVGSVGElement> {
  variant: IconVariant
  /** Pixel size (default 20). */
  size?: number
}

/**
 * Minimal inline SVG icons for file types. No external icon library dependency.
 * Each icon uses the zinc palette and is sized consistently.
 */
export function FileIcon({ variant, size = 20, className = '', ...rest }: FileIconProps) {
  const colorMap: Record<IconVariant, string> = {
    folder: 'text-violet-400',
    file: 'text-zinc-500',
    img: 'text-blue-400',
    pdf: 'text-red-400',
    doc: 'text-blue-400',
    xls: 'text-green-400',
    csv: 'text-green-400',
    ppt: 'text-orange-400',
    video: 'text-purple-400',
    audio: 'text-pink-400',
    archive: 'text-yellow-400',
    code: 'text-emerald-400',
    txt: 'text-zinc-400',
    md: 'text-zinc-400',
  }

  const color = colorMap[variant] ?? colorMap.file

  if (variant === 'folder') {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" fill="none" className={`${color} ${className}`} aria-hidden="true" {...rest}>
        <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" fill="currentColor" fillOpacity="0.2" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />
      </svg>
    )
  }

  // Generic file shape (all non-folder variants)
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" className={`${color} ${className}`} aria-hidden="true" {...rest}>
      <path d="M6 2a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8l-6-6H6z" fill="currentColor" fillOpacity="0.1" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />
      <path d="M14 2v6h6" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />
    </svg>
  )
}

export default FileIcon
