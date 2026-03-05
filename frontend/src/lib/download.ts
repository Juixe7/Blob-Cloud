import { decryptChunkPayload } from './crypto'
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
  
  let offset = 0
  while (offset < encryptedBuffer.byteLength) {
    const nextOffset = Math.min(offset + ENCRYPTED_CHUNK_SIZE, encryptedBuffer.byteLength)
    const chunk = encryptedBuffer.slice(offset, nextOffset)
    const plaintext = await decryptChunkPayload(chunk, passphrase)
    decryptedChunks.push(plaintext)
    offset = nextOffset
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
