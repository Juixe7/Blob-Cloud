import { decryptChunkPayload, deriveKeyPBKDF2 } from './crypto'
import { apiClient } from './api'
import { getAccessToken } from './token'

const CHUNK_SIZE = 4 * 1024 * 1024
const ENCRYPTED_CHUNK_OVERHEAD = 44 // 12 (IV) + 16 (Salt) + 16 (Auth tag)
const ENCRYPTED_CHUNK_SIZE = CHUNK_SIZE + ENCRYPTED_CHUNK_OVERHEAD

export async function downloadEncryptedFile(fileId: string, filename: string, passphrase: string) {
  const base = apiClient.defaults.baseURL ?? '/api'
  const token = getAccessToken() ?? ''
  const url = `${base}/files/${fileId}/download?token=${encodeURIComponent(token)}`

  const response = await fetch(url)
  if (!response.ok) {
    throw new Error('Failed to download encrypted file')
  }

  const encryptedBuffer = await response.arrayBuffer()
  const decryptedChunks: ArrayBuffer[] = []

  const view = new DataView(encryptedBuffer)
  let offset = 0
  let cachedKey: CryptoKey | undefined

  // Check if buffer uses self-describing length-prefix framing
  const isFramed =
    encryptedBuffer.byteLength >= 4 &&
    (() => {
      const firstLen = view.getUint32(0, false)
      return firstLen >= 28 && firstLen + 4 <= encryptedBuffer.byteLength
    })()

  if (isFramed) {
    while (offset + 4 <= encryptedBuffer.byteLength) {
      const chunkLen = view.getUint32(offset, false)
      offset += 4
      if (offset + chunkLen > encryptedBuffer.byteLength) {
        throw new Error('Corrupted encrypted stream: chunk length exceeds stream boundaries')
      }
      const chunk = encryptedBuffer.slice(offset, offset + chunkLen)

      // Derive and cache CryptoKey once from the first chunk's salt
      if (!cachedKey && chunk.byteLength >= 28) {
        const saltBytes = new Uint8Array(chunk, 12, 16)
        const saltHex = Array.from(saltBytes)
          .map((b) => b.toString(16).padStart(2, '0'))
          .join('')
        const derived = await deriveKeyPBKDF2(passphrase, saltHex)
        cachedKey = derived.key
      }

      const plaintext = await decryptChunkPayload(chunk, cachedKey || passphrase)
      decryptedChunks.push(plaintext)
      offset += chunkLen
    }
  } else {
    // Fallback for legacy un-framed downloads
    while (offset < encryptedBuffer.byteLength) {
      const nextOffset = Math.min(offset + ENCRYPTED_CHUNK_SIZE, encryptedBuffer.byteLength)
      const chunk = encryptedBuffer.slice(offset, nextOffset)
      const plaintext = await decryptChunkPayload(chunk, passphrase)
      decryptedChunks.push(plaintext)
      offset = nextOffset
    }
  }

  const blob = new Blob(decryptedChunks, { type: 'application/octet-stream' })
  const objectUrl = URL.createObjectURL(blob)
  
  const a = document.createElement('a')
  a.href = objectUrl
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  
  setTimeout(() => URL.revokeObjectURL(objectUrl), 10000)
}
