/**
 * Client-Side Media Thumbnail Extractor
 *
 * Extracts visual previews for videos and PDFs directly in the browser:
 * - Videos: Extracts a high-resolution frame at 1s (or midpoint) using HTML5 <video> + <canvas>
 * - PDFs: Renders Page 1 using Mozilla's pdfjs-dist onto an offscreen <canvas>
 *
 * Both output standard 200-320px PNG blobs suitable for direct upload to PUT /api/files/{id}/thumbnail.
 */

import * as pdfjsLib from 'pdfjs-dist'
import pdfWorkerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url'

// Configure PDF.js worker using Vite's static asset URL resolver
pdfjsLib.GlobalWorkerOptions.workerSrc = pdfWorkerUrl

const THUMBNAIL_MAX_DIMENSION = 320

/**
 * Capture a frame from an HTML5-compatible video file (MP4, WebM, MOV).
 */
export async function extractVideoThumbnail(file: File): Promise<Blob | null> {
  return new Promise((resolve) => {
    // Only attempt video frame extraction in a browser DOM environment
    if (typeof document === 'undefined') {
      resolve(null)
      return
    }

    const video = document.createElement('video')
    video.preload = 'metadata'
    video.muted = true
    video.playsInline = true

    const objectUrl = URL.createObjectURL(file)
    video.src = objectUrl

    let hasCleanedUp = false
    const cleanup = () => {
      if (hasCleanedUp) return
      hasCleanedUp = true
      URL.revokeObjectURL(objectUrl)
      video.removeAttribute('src')
      video.load()
    }

    // Safety timeout in case video decoding hangs or codec is unsupported
    const timeout = setTimeout(() => {
      cleanup()
      resolve(null)
    }, 8000)

    video.onloadedmetadata = () => {
      // Seek to 1s mark, or midpoint if video is shorter than 1s
      const seekTime = video.duration > 1 ? 1 : Math.max(0.1, video.duration / 2)
      video.currentTime = seekTime
    }

    video.onseeked = () => {
      clearTimeout(timeout)
      try {
        const width = video.videoWidth || 320
        const height = video.videoHeight || 240

        const scale = Math.min(
          THUMBNAIL_MAX_DIMENSION / width,
          THUMBNAIL_MAX_DIMENSION / height,
          1,
        )
        const targetWidth = Math.max(1, Math.round(width * scale))
        const targetHeight = Math.max(1, Math.round(height * scale))

        const canvas = document.createElement('canvas')
        canvas.width = targetWidth
        canvas.height = targetHeight

        const ctx = canvas.getContext('2d')
        if (!ctx) {
          cleanup()
          resolve(null)
          return
        }

        ctx.drawImage(video, 0, 0, targetWidth, targetHeight)
        canvas.toBlob((blob) => {
          cleanup()
          resolve(blob)
        }, 'image/png')
      } catch {
        cleanup()
        resolve(null)
      }
    }

    video.onerror = () => {
      clearTimeout(timeout)
      cleanup()
      resolve(null)
    }
  })
}

/**
 * Render the first page of a PDF document to a PNG thumbnail Blob.
 */
export async function extractPdfThumbnail(file: File): Promise<Blob | null> {
  try {
    const arrayBuffer = await file.arrayBuffer()
    const loadingTask = pdfjsLib.getDocument({ data: arrayBuffer })
    const pdf = await loadingTask.promise

    if (pdf.numPages < 1) {
      return null
    }

    const page = await pdf.getPage(1)
    const originalViewport = page.getViewport({ scale: 1.0 })

    const scale = Math.min(
      THUMBNAIL_MAX_DIMENSION / originalViewport.width,
      THUMBNAIL_MAX_DIMENSION / originalViewport.height,
      1.5,
    )
    const viewport = page.getViewport({ scale })

    const canvas = document.createElement('canvas')
    canvas.width = Math.round(viewport.width)
    canvas.height = Math.round(viewport.height)

    const ctx = canvas.getContext('2d')
    if (!ctx) return null

    const renderContext = {
      canvasContext: ctx,
      viewport: viewport,
    }

    await page.render(renderContext).promise

    return new Promise<Blob | null>((resolve) => {
      canvas.toBlob((blob) => {
        resolve(blob)
      }, 'image/png')
    })
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn('[thumbnailExtractor] failed to render PDF page 1:', err)
    return null
  }
}

/**
 * Extract thumbnail blob for any supported media file (video or PDF).
 */
export async function extractMediaThumbnail(file: File): Promise<Blob | null> {
  const mime = file.type?.toLowerCase() || ''
  const name = file.name.toLowerCase()

  if (mime.startsWith('video/') || /\.(mp4|webm|mov|mkv)$/i.test(name)) {
    return extractVideoThumbnail(file)
  }

  if (mime === 'application/pdf' || name.endsWith('.pdf')) {
    return extractPdfThumbnail(file)
  }

  return null
}
