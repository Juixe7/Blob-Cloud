/// <reference lib="webworker" />
import { deriveKeyPBKDF2, encryptChunkPayload } from '../lib/crypto'

/**
 * Web Worker: Bounded concurrency (limit = 3) file slicing + SHA-256 & MD5 hashing.
 *
 * Runs entirely on a background thread so the React main thread never drops
 * frames while processing large files (e.g. 500MB+ to 10GB+).
 *
 * Bounded Concurrency Semaphore:
 *   Limits peak memory footprint by processing at most 3 chunk slices in memory
 *   concurrently, preventing browser RAM exhaustion.
 */

/** Exact chunk size: 4,194,304 bytes (4 MiB). */
const CHUNK_SIZE = 4 * 1024 * 1024

/** Strict concurrency semaphore ceiling. */
const CONCURRENCY_LIMIT = 3

/* ---- Typed message contracts (shared with UploadContext) ---- */

export interface ChunkHashResult {
  sha256: string
  md5: string // Hex-encoded MD5 hash of the chunk
  size_bytes: number
}

export type HashWorkerRequest = { 
  type: 'hash'
  file: File
  passphrase?: string
}

export type HashWorkerResponse =
  | { type: 'progress'; progress: number }
  | { type: 'complete'; chunks: ChunkHashResult[]; encryption_salt?: string }
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
 * Pure TypeScript MD5 implementation for ArrayBuffer.
 */
function md5(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  const len = bytes.length

  const bitLen = len * 8
  const paddingLen = (len % 64 < 56) ? (56 - (len % 64)) : (120 - (len % 64))
  const padded = new Uint8Array(len + paddingLen + 8)
  padded.set(bytes)
  padded[len] = 0x80

  const view = new DataView(padded.buffer)
  view.setUint32(padded.length - 8, bitLen >>> 0, true)
  view.setUint32(padded.length - 4, Math.floor(bitLen / 0x100000000), true)

  let a0 = 0x67452301
  let b0 = 0xefcdab89
  let c0 = 0x98badcfe
  let d0 = 0x10325476

  const s = [
    7, 12, 17, 22,  7, 12, 17, 22,  7, 12, 17, 22,  7, 12, 17, 22,
    5,  9, 14, 20,  5,  9, 14, 20,  5,  9, 14, 20,  5,  9, 14, 20,
    4, 11, 16, 23,  4, 11, 16, 23,  4, 11, 16, 23,  4, 11, 16, 23,
    6, 10, 15, 21,  6, 10, 15, 21,  6, 10, 15, 21,  6, 10, 15, 21
  ]

  const K = new Uint32Array(64)
  for (let i = 0; i < 64; i++) {
    K[i] = Math.floor(Math.abs(Math.sin(i + 1)) * 4294967296) >>> 0
  }

  for (let i = 0; i < padded.length; i += 64) {
    let A = a0, B = b0, C = c0, D = d0
    const M = new Uint32Array(16)
    for (let j = 0; j < 16; j++) {
      M[j] = view.getUint32(i + j * 4, true)
    }

    for (let j = 0; j < 64; j++) {
      let F = 0
      let g = 0
      if (j < 16) {
        F = (B & C) | ((~B) & D)
        g = j
      } else if (j < 32) {
        F = (D & B) | ((~D) & C)
        g = (5 * j + 1) % 16
      } else if (j < 48) {
        F = B ^ C ^ D
        g = (3 * j + 5) % 16
      } else {
        F = C ^ (B | (~D))
        g = (7 * j) % 16
      }

      const temp = D
      D = C
      C = B
      const sum = (A + F + K[j] + M[g]) >>> 0
      const rot = (sum << s[j]) | (sum >>> (32 - s[j]))
      B = (B + rot) >>> 0
      A = temp
    }

    a0 = (a0 + A) >>> 0
    b0 = (b0 + B) >>> 0
    c0 = (c0 + C) >>> 0
    d0 = (d0 + D) >>> 0
  }

  const hex32 = (n: number) => {
    let str = ''
    for (let j = 0; j < 4; j++) {
      str += ((n >> (j * 8)) & 0xff).toString(16).padStart(2, '0')
    }
    return str
  }

  return hex32(a0) + hex32(b0) + hex32(c0) + hex32(d0)
}

/**
 * Slice a File into fixed-size chunks and compute SHA-256 + MD5 hashes using
 * a Bounded Concurrency Semaphore (max 3 concurrent chunk operations).
 */
async function hashFileBounded(file: File, passphrase?: string): Promise<{ chunks: ChunkHashResult[], salt?: string }> {
  const totalChunks = Math.max(1, Math.ceil(file.size / CHUNK_SIZE))
  const results: ChunkHashResult[] = new Array(totalChunks)

  let nextIndex = 0
  let completedCount = 0

  let cryptoKey: CryptoKey | undefined
  let encryptionSalt: string | undefined

  if (passphrase) {
    const derived = await deriveKeyPBKDF2(passphrase)
    cryptoKey = derived.key
    encryptionSalt = derived.salt
  }

  return new Promise<{ chunks: ChunkHashResult[], salt?: string }>((resolve, reject) => {
    const processNext = async () => {
      if (completedCount === totalChunks) {
        resolve({ chunks: results, salt: encryptionSalt })
        return
      }

      while (nextIndex < totalChunks) {
        const index = nextIndex++
        const offset = index * CHUNK_SIZE

        try {
          const slice = file.slice(offset, Math.min(file.size, offset + CHUNK_SIZE))
          let arrayBuffer = await slice.arrayBuffer()

          if (passphrase && cryptoKey && encryptionSalt) {
            arrayBuffer = await encryptChunkPayload(arrayBuffer, cryptoKey, encryptionSalt, index)
          }

          const sha256Digest = await crypto.subtle.digest('SHA-256', arrayBuffer)
          const sha256Hex = bufferToHex(sha256Digest)
          const md5Hex = md5(arrayBuffer)

          results[index] = {
            sha256: sha256Hex,
            md5: md5Hex,
            size_bytes: arrayBuffer.byteLength,
          }

          completedCount++
          const progress = Math.round((completedCount / totalChunks) * 100)
          const msg: HashWorkerResponse = { type: 'progress', progress }
          ;(self as unknown as Worker).postMessage(msg)

          if (completedCount === totalChunks) {
            resolve({ chunks: results, salt: encryptionSalt })
            return
          }
        } catch (err) {
          reject(err)
          return
        }
      }
    }

    // Launch up to CONCURRENCY_LIMIT (3) parallel task runners
    const initialRunners = Math.min(CONCURRENCY_LIMIT, totalChunks)
    for (let i = 0; i < initialRunners; i++) {
      void processNext()
    }
  })
}

/* ---- Worker entry point ---- */

self.onmessage = async (e: MessageEvent<HashWorkerRequest>) => {
  const { type, file, passphrase } = e.data

  if (type !== 'hash' || !file) {
    const msg: HashWorkerResponse = { type: 'error', error: 'Invalid worker message.' }
    ;(self as unknown as Worker).postMessage(msg)
    return
  }

  try {
    const { chunks, salt } = await hashFileBounded(file, passphrase)
    const msg: HashWorkerResponse = { type: 'complete', chunks, encryption_salt: salt }
    ;(self as unknown as Worker).postMessage(msg)
  } catch (err) {
    const msg: HashWorkerResponse = { type: 'error', error: (err as Error).message }
    ;(self as unknown as Worker).postMessage(msg)
  }
}


export {}
