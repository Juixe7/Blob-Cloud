/**
 * In-Memory Thumbnail Failure Cache
 *
 * Tracks files whose thumbnails returned 404/error during this session.
 * Prevents redundant HTTP 404 network requests, console spam, and visual
 * icon-flickering during virtualized list scrolling or selection re-renders.
 */

const failedThumbnailIds = new Set<string>()

/**
 * Record that a file does not have a server-side thumbnail available.
 */
export function markThumbnailFailed(fileId: string): void {
  if (fileId) {
    failedThumbnailIds.add(fileId)
  }
}

/**
 * Check whether a thumbnail request has previously failed for this file.
 */
export function isThumbnailFailed(fileId: string): boolean {
  if (!fileId) return false
  return failedThumbnailIds.has(fileId)
}

/**
 * Reset the cache (e.g. after fresh batch uploads).
 */
export function clearThumbnailFailureCache(): void {
  failedThumbnailIds.clear()
}
