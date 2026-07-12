/// <reference lib="webworker" />

/**
 * Web Worker: file slicing + SHA-256 hashing.
 *
 * Runs entirely on a background thread so the React main thread never drops
 * frames while processing large files (e.g. 500MB+).
 *
 * Protocol:
 *   main → worker : { type: 'hash', file: File }
 *   worker → main : { type: 'progress', progress: number }   (0..100)
 *                   { type: 'complete', chunks: [{sha256, size_bytes}] }
 *                   { type: 'error', error: string }
 *
 * The sliced Blobs are NOT transferred back (they would be copied, doubling
 * memory). Instead the main thread re-slices from the original File when it
 * needs the raw bytes for the S3 PUT — cheap because Blob.slice is lazy.
 */

/** Exact chunk size: 4,194,304 bytes (4 MiB). */
const CHUNK_SIZE = 4 * 1024 * 1024

/* ---- Typed message contracts (shared with UploadContext) ---- */

export type HashWorkerRequest = { type: 'hash'; file: File }

export type HashWorkerResponse =
  | { type: 'progress'; progress: number }
  | { type: 'complete'; chunks: { sha256: string; size_bytes: number }[] }
  | { type: 'error'; error: string }

/* ---- Helpers ---- */

/**
 * Convert an ArrayBuffer (raw SHA-256 digest bytes) to a lowercase hex string.
 */
function bufferToHex(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let hex = ''
  for (let i = 0; i < bytes.length; i++) {
    hex += bytes[i].toString(16).padStart(2, '0')
  }
  return hex
}

/**
 * Slice a File into fixed-size chunks and compute the SHA-256 of each using
 * the native Web Crypto API. Reports progress after every chunk.
 */
async function hashFile(file: File): Promise<{ sha256: string; size_bytes: number }[]> {
  const totalChunks = Math.max(1, Math.ceil(file.size / CHUNK_SIZE))
  const chunks: { sha256: string; size_bytes: number }[] = []

  for (let offset = 0, index = 0; offset < file.size || index === 0; offset += CHUNK_SIZE, index++) {
    const slice = file.slice(offset, offset + CHUNK_SIZE)
    const arrayBuffer = await slice.arrayBuffer()
    const digest = await crypto.subtle.digest('SHA-256', arrayBuffer)

    chunks.push({
      sha256: bufferToHex(digest),
      size_bytes: arrayBuffer.byteLength,
    })

    // Report progress as fraction of total chunks hashed.
    const progress = Math.round(((index + 1) / totalChunks) * 100)
    const msg: HashWorkerResponse = { type: 'progress', progress }
    ;(self as unknown as Worker).postMessage(msg)
  }

  return chunks
}

/* ---- Worker entry point ---- */

self.onmessage = async (e: MessageEvent<HashWorkerRequest>) => {
  const { type, file } = e.data

  if (type !== 'hash' || !file) {
    const msg: HashWorkerResponse = { type: 'error', error: 'Invalid worker message.' }
    ;(self as unknown as Worker).postMessage(msg)
    return
  }

  try {
    const chunks = await hashFile(file)
    const msg: HashWorkerResponse = { type: 'complete', chunks }
    ;(self as unknown as Worker).postMessage(msg)
  } catch (err) {
    const message = err instanceof Error ? err.message : 'Unknown hashing error.'
    const msg: HashWorkerResponse = { type: 'error', error: message }
    ;(self as unknown as Worker).postMessage(msg)
  }
}

export {}
