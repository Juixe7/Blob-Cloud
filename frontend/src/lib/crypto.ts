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

// Encrypt a chunk and prepend IV + Salt to it.
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
  
  // Payload: [IV (12 bytes)] + [Salt (16 bytes)] + [Ciphertext]
  const payload = new Uint8Array(iv.length + saltBuffer.length + ciphertext.byteLength)
  payload.set(iv, 0)
  payload.set(saltBuffer, iv.length)
  payload.set(new Uint8Array(ciphertext), iv.length + saltBuffer.length)

  return payload.buffer
}

// Decrypt a chunk by extracting IV + Salt from the payload.
export async function decryptChunkPayload(
  payload: ArrayBuffer,
  passphrase: string
): Promise<ArrayBuffer> {
  const payloadBytes = new Uint8Array(payload)
  if (payloadBytes.length < 28) { // 12 (IV) + 16 (Salt)
    throw new Error('Invalid encrypted payload: too small')
  }

  const iv = payloadBytes.slice(0, 12)
  const saltBuffer = payloadBytes.slice(12, 28)
  const ciphertext = payloadBytes.slice(28)

  const saltHex = bufferToHex(saltBuffer)
  const { key } = await deriveKeyPBKDF2(passphrase, saltHex)

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
