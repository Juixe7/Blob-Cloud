/**
 * Zero-Knowledge E2EE using the Web Crypto API.
 * Uses AES-GCM for authenticated encryption and PBKDF2 for key derivation.
 */

// Derive a 256-bit AES-GCM key from a user passphrase and a given salt.
// If salt is not provided, a random 16-byte salt is generated.
export async function deriveKeyPBKDF2(
  passphrase: string,
  saltHex?: string
): Promise<{ key: CryptoKey; salt: string }> {
  const enc = new TextEncoder()
  const keyMaterial = await crypto.subtle.importKey(
    'raw',
    enc.encode(passphrase),
    { name: 'PBKDF2' },
    false,
    ['deriveBits', 'deriveKey']
  )

  let saltBuffer: Uint8Array
  if (saltHex) {
    saltBuffer = new Uint8Array(saltHex.match(/.{1,2}/g)!.map((byte) => parseInt(byte, 16)))
  } else {
    saltBuffer = crypto.getRandomValues(new Uint8Array(16))
  }

  const key = await crypto.subtle.deriveKey(
    {
      name: 'PBKDF2',
      salt: saltBuffer as BufferSource,
      iterations: 100000,
      hash: 'SHA-256',
    },
    keyMaterial,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt', 'decrypt']
  )

  return { key, salt: bufferToHex(saltBuffer) }
}

// Deterministic IV generation based on the chunk sequence number and the file salt.
export async function generateDeterministicIV(saltHex: string, sequenceNumber: number): Promise<Uint8Array> {
  const enc = new TextEncoder()
  const data = enc.encode(`${saltHex}:${sequenceNumber}`)
  const hash = await crypto.subtle.digest('SHA-256', data)
  // AES-GCM uses a 12-byte IV
  return new Uint8Array(hash).slice(0, 12)
}

// In-memory cache for derived PBKDF2 keys to eliminate redundant derivations across chunks.
const keyDerivationCache = new Map<string, CryptoKey>()

export async function getOrDeriveKey(passphrase: string, saltHex: string): Promise<CryptoKey> {
  const cacheKey = `${passphrase}:${saltHex}`
  const cached = keyDerivationCache.get(cacheKey)
  if (cached) return cached
  const { key } = await deriveKeyPBKDF2(passphrase, saltHex)
  keyDerivationCache.set(cacheKey, key)
  return key
}

// Encrypt a chunk and prepend 4-byte length prefix + IV + Salt.
// Frame format: [Length: uint32 BE (4 bytes)] + [IV (12 bytes)] + [Salt (16 bytes)] + [Ciphertext + Auth Tag]
// Length is the size of the payload following the 4-byte header: 12 + 16 + ciphertext.byteLength
export async function encryptChunkPayload(
  chunk: ArrayBuffer,
  key: CryptoKey,
  saltHex: string,
  sequenceNumber: number
): Promise<ArrayBuffer> {
  const iv = await generateDeterministicIV(saltHex, sequenceNumber)
  
  const ciphertext = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv: iv as BufferSource },
    key,
    chunk as BufferSource
  )

  const saltBuffer = new Uint8Array(saltHex.match(/.{1,2}/g)!.map((byte) => parseInt(byte, 16)))
  const contentLen = iv.length + saltBuffer.length + ciphertext.byteLength
  
  // Total payload = 4 bytes (header length) + contentLen
  const payload = new Uint8Array(4 + contentLen)
  const view = new DataView(payload.buffer)
  view.setUint32(0, contentLen, false) // Big-endian uint32
  payload.set(iv, 4)
  payload.set(saltBuffer, 4 + iv.length)
  payload.set(new Uint8Array(ciphertext), 4 + iv.length + saltBuffer.length)

  return payload.buffer
}

// Decrypt a chunk by extracting IV + Salt from the payload.
// Supports framed payloads (with 4-byte length prefix) and unframed payloads.
// Accepts either a passphrase string or a pre-derived CryptoKey.
export async function decryptChunkPayload(
  payload: ArrayBuffer,
  passphraseOrKey: string | CryptoKey
): Promise<ArrayBuffer> {
  let payloadBytes = new Uint8Array(payload)

  // If payload contains 4-byte length prefix matching remaining length, strip it
  if (payloadBytes.length >= 4) {
    const view = new DataView(payloadBytes.buffer, payloadBytes.byteOffset, payloadBytes.byteLength)
    const possibleLen = view.getUint32(0, false)
    if (possibleLen === payloadBytes.length - 4) {
      payloadBytes = payloadBytes.subarray(4)
    }
  }

  if (payloadBytes.length < 28) { // 12 (IV) + 16 (Salt)
    throw new Error('Invalid encrypted payload: too small')
  }

  const iv = payloadBytes.slice(0, 12)
  const saltBuffer = payloadBytes.slice(12, 28)
  const ciphertext = payloadBytes.slice(28)

  let key: CryptoKey
  if (typeof passphraseOrKey === 'string') {
    const saltHex = bufferToHex(saltBuffer)
    key = await getOrDeriveKey(passphraseOrKey, saltHex)
  } else {
    key = passphraseOrKey
  }

  const plaintext = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: iv as BufferSource },
    key,
    ciphertext as BufferSource
  )

  return plaintext
}

function bufferToHex(buffer: ArrayBuffer | Uint8Array): string {
  const bytes = new Uint8Array(buffer)
  let hex = ''
  for (let i = 0; i < bytes.length; i++) {
    hex += bytes[i].toString(16).padStart(2, '0')
  }
  return hex
}

