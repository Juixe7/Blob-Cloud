import { useState } from 'react'
import { cn } from '../lib/format'
import { FileIcon } from './FileIcon'
import type { IconVariant } from '../lib/format'

interface FileThumbProps {
  /** Optional thumbnail URL (set over WebSocket via THUMBNAIL_READY). */
  thumbnailUrl?: string
  /** Fallback icon variant when no thumbnail is available. */
  iconVariant: IconVariant
  /** Pixel size of the icon/fallback. The thumbnail fills its container. */
  iconSize?: number
  /** Layout style: 'grid' (fills a square area) or 'row' (inline). */
  layout?: 'grid' | 'row'
  className?: string
  isShortcut?: boolean
}

/**
 * Renders a file's thumbnail image when available, falling back to the generic
 * type-based icon. This is the visual swap target for THUMBNAIL_READY events:
 * when the SQS worker finishes, the backend pushes the thumbnail URL over WS,
 * the Dashboard merges it into local state, and this component re-renders with
 * the image — no full directory refresh needed.
 */
export function FileThumb({
  thumbnailUrl,
  iconVariant,
  iconSize = 20,
  layout = 'row',
  className,
  isShortcut = false,
}: FileThumbProps) {
  const [errored, setErrored] = useState(false)
  const showImage = thumbnailUrl && !errored

  if (showImage) {
    if (layout === 'grid') {
      return (
        <div className={cn('h-16 w-full overflow-hidden rounded-lg bg-zinc-900', className)}>
          <img
            src={thumbnailUrl}
            alt=""
            loading="lazy"
            onError={() => setErrored(true)}
            className="h-full w-full object-cover"
          />
        </div>
      )
    }
    return (
      <img
        src={thumbnailUrl}
        alt=""
        loading="lazy"
        onError={() => setErrored(true)}
        className={cn('rounded', className)}
        width={iconSize}
        height={iconSize}
      />
    )
  }

  // Fallback: generic type-based icon.
  if (layout === 'grid') {
    return (
      <div className={cn('flex h-16 w-full items-center justify-center rounded-lg bg-zinc-900', className)}>
        <FileIcon variant={iconVariant} size={iconSize} isShortcut={isShortcut} />
      </div>
    )
  }
  return <FileIcon variant={iconVariant} size={iconSize} className={className} isShortcut={isShortcut} />
}

export default FileThumb
