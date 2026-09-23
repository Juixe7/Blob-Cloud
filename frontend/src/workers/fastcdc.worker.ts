/// <reference lib="webworker" />
import { deriveKeyPBKDF2, encryptChunkPayload } from '../lib/crypto'

/**
 * Web Worker: FastCDC (Fast Content-Defined Chunking) Rolling Gear Hash.
 *
 * Runs entirely on a background thread so the React main thread never drops
 * frames while processing large files (e.g. 500MB+ to 10GB+).
 *
 * Implements shift-resistant content-defined chunking with dual masks to prevent
 * the "byte-shift fallacy" and maximize deduplication rates across file revisions.
 */

/** Mathematical boundaries matching backend/internal/chunker. */
const MIN_SIZE = 1 * 1024 * 1024 // 1 MiB (2^20)
const AVG_SIZE = 4 * 1024 * 1024 // 4 MiB (2^22)
const MAX_SIZE = 8 * 1024 * 1024 // 8 MiB (2^23)

/** FastCDC normalized dual masks for 4 MiB average. */
const MASK_SMALL = 0x7fffffn // 23 bits set (p = 2^-23)
const MASK_LARGE = 0x1fffffn // 21 bits set (p = 2^-21)
const UINT64_MASK = 0xffffffffffffffffn

/**
 * Canonical 256-element 64-bit Gear Table matching backend/internal/chunker/gear_table.go.
 */
export const GEAR_TABLE: BigUint64Array = new BigUint64Array([
  0xa0ac8bcd33a2e774n, 0x2724a8a7b1990037n, 0xd2342aeb33e55f23n, 0xbb6e0d0851e4d23fn,
  0x07f4393663b65fa3n, 0xa5ffdf15ba1207e0n, 0x4e6e6dc63f6404can, 0x546ca82c5ee15ea6n,
  0x08be59b92205561bn, 0xa9fbc185cecb6918n, 0x58f7027c082717a6n, 0xcead8f2d50e8dc1fn,
  0x89e8312b97fe3438n, 0x524b6113e64f77c0n, 0x56a31c5188bf697en, 0x011d889ea7c89f50n,
  0xaaecf7fa56a1b244n, 0xda8e3bf5d4eefd20n, 0x762f01f28b7e2898n, 0x933010b979450c5an,
  0xb70461bb0c73e04en, 0x8a927a42ea7447b9n, 0xf6cf258e72fa0572n, 0x9950e06552a8ebdcn,
  0xbbca7b962383c274n, 0x3d077fb58ea9616en, 0x1a877e68449c2a63n, 0x24a46a6f19582d2cn,
  0x4f181f33f678280fn, 0xbbebb4a64efc9849n, 0xc75f10b2a75877aan, 0xc0bf062925b4cf39n,
  0xbda4fa76c49c71a3n, 0x718f6cbb7dff0c6cn, 0xcb9cfaf71f9cf50cn, 0xa2c6b307ec3c3c12n,
  0xa6d181938b813fdcn, 0xb1d46b0a23351ec8n, 0x90ca5b33100c500an, 0x789211d2938ca23bn,
  0xa39d2551fe668dc4n, 0x4c2b9f76a0863071n, 0x1f0e42a0b3ff635bn, 0x5a549d44fa65b214n,
  0xd342fa790757d23dn, 0x8be831e5f396495bn, 0x09041fa34cffc1efn, 0x77c44e976c67efcan,
  0xcfe6854e4c5b1b4dn, 0xd02a3922e379b324n, 0xa1bfca1483ceb987n, 0xc274e1d510c41ff9n,
  0x82f2dbf77c385db1n, 0x1116c4f0392e22c0n, 0x1ecaa47a06ee0a71n, 0x77a6f200be5c66b8n,
  0xeb06bf8941f71db2n, 0x217d84814ba36130n, 0x5e2bfe12bb673722n, 0xd383f9da8d13264cn,
  0x96bf0f4ebfa88288n, 0x3dff52e04cf46d6dn, 0xa4ba42aa0fcab6d4n, 0xbb0f5fc67d8f4bc1n,
  0x2e86dc7ec24a6fa8n, 0xbfe40d7a0447385dn, 0x48641eb13c1c5ad8n, 0x2ae6dc89ca114948n,
  0x815a5f973be36e1cn, 0xf6bebb26aeecf0d0n, 0x948cebb87df02f10n, 0x7e86e73724c3d4een,
  0x463cbba9676e2552n, 0xbfa933390c2394a1n, 0x546e33a6f1d97746n, 0x91d9d590e8d91f2cn,
  0x221379eb00d0a7a3n, 0xebfa9b77543d31fen, 0x41f8796da99859f5n, 0x40a7b4588523b0a7n,
  0x6854e99ef3bcae63n, 0xdfec2c92a9b343dan, 0x51c518868d44747bn, 0x960b73c4d7d11f84n,
  0x7d287bb2e7e008aan, 0xc059a4c84435882bn, 0x28975a5e33d26ff9n, 0x6e76cf0e3ee7c0cfn,
  0xf1455bb32a58b292n, 0xd31c8fa5a43b2f56n, 0x92f9e421ebdd0ca6n, 0x4b7ffb58532f7036n,
  0x34d5885f80b2a8d4n, 0x2614ff0e68d0bb69n, 0x07f185489f665dc6n, 0xc10f135b91b5c479n,
  0x5f159267ae68e826n, 0xa5b80b7c7b80a2b5n, 0x5824ae655cb3b88bn, 0xd3f8595cb3258c70n,
  0x4d193d58d9241b9en, 0x9cfb34d740eb424fn, 0x0db9ecdae3381a17n, 0x74fc73042a96a014n,
  0x0606f2e8250ba3f4n, 0x1b1bcbe398b958e9n, 0xaa06eef1e97669d6n, 0xb7975ce7c83cbf2bn,
  0x8b8577319e7cfbd2n, 0x3d2740bc433604f3n, 0x55ff6786ff3d8ff4n, 0x66c7dd28646b9762n,
  0x519808d7e00c3b88n, 0x76ea8f0d84570076n, 0x8b5ef1e0f068ecfen, 0x5f61642878bf73d1n,
  0x16ca52a13ee12224n, 0x3e18a80fb65c2df5n, 0x5215c2691f9b33a5n, 0x241dfb33230b7a8dn,
  0xd6f4eeea060f64ben, 0x40317e3f89e414c1n, 0x5c79eeebdbafce76n, 0x49341ef946452296n,
  0xa7ec2b972e6b0147n, 0xb9cb304cf5c8f85fn, 0x2077e64cf121dc49n, 0x98cfcfd799539316n,
  0x103d157642646d65n, 0x5990f11dbf2178dbn, 0x7ef4d5464f1c1072n, 0x9b1b60683a53e5e4n,
  0x1f26cf95faee5ba8n, 0x8a10878e11b333ecn, 0x098d5c3f640243e8n, 0xbb258679f0fcceb6n,
  0x4642cfaf6feee7a1n, 0x86701f5c71bfa1ban, 0xfa64a51e626e2e0en, 0xefdf6433e14c7760n,
  0xd546eb827bf91ef7n, 0xee72462e7aa23a6cn, 0x87a95ca1efd630d7n, 0xbff688e174ec783fn,
  0x803ea415b3c5332fn, 0x56a642ecb75560b2n, 0x7387a6dcfaf3e712n, 0x93ce3ffb5f00cf04n,
  0xfd5cf72834b6e5e9n, 0x52b61ef5aa85b7f1n, 0x5776d5452f3e8271n, 0xc1dfa35b13e9a7e6n,
  0x6854d6faef492d5cn, 0xee85d386dffbc6ecn, 0x2e83fbefb1a43a0en, 0xb1b9efdc1b88e1e7n,
  0x3e18a0cefc1db2a3n, 0x4f128eeb318cbce4n, 0x3d84ca46bfef8204n, 0xa5a7df3b8f107386n,
  0x789b91763ef80c7dn, 0x8f7d0c3e986fe547n, 0xd47ebf970868eb2an, 0x74d5ceee356e7e59n,
  0x0ca6ffba879c5361n, 0x5b36ba2b6e147d34n, 0x1fe2b0d366a7ecf2n, 0x3c990eeef4cfec16n,
  0x67eeaa45b59ea892n, 0x2c4e207b19be55d6n, 0x34ee464ee85d3714n, 0x08796d195feab9e0n,
  0x88ea307df51cb27dn, 0x0548ca7c6f05cb2en, 0x4e6bba84fe67ee97n, 0x310f88a25c11d2e2n,
  0x4614ff7bc1f52b60n, 0x8826d9c63d5045a5n, 0x78be2a8ee0b7ee2en, 0x23ea4e17ef1b3691n,
  0x11fcbca36f047ff1n, 0xb214da839fe53cf1n, 0xa58ff8c01eebe36an, 0x6e1ca80b62e457f9n,
  0x7ef4b82d92eeeb46n, 0x4095bb1b2e1bf36bn, 0x2f8bbba3e7df6528n, 0x3c5520bfdbd1e577n,
  0x8e2b86ea9a738ca2n, 0xa873fefc665ee7a1n, 0x7946cfba1e45758bn, 0x6e5fa34e8f1704ban,
  0xc98d1a1005a63901n, 0xd0ff17ca6143be28n, 0x1ecce3c4155b1eb7n, 0x1fca35a7fc4d7fe9n,
  0xdae20d20ef8b0821n, 0x76541f92e6fb36d0n, 0xcba187f58d2cebc4n, 0x931b2e6dbef468bfn,
  0x2ea5c23e857fe0cfn, 0x3f5c71bf9e472097n, 0xb338bbef7b28a8d9n, 0x489eb12f518e38d7n,
  0xefbcf7aa17cefe26n, 0x6e3557e4bfa02568n, 0x3cbfa59345eeea64n, 0x863ba90ef76eb9a6n,
  0x19dfa628be761c5an, 0x6a066b1a20ffc06fn, 0x8d5cba124e4d6e9bn, 0x85ecba43fecbf8c0n,
  0xbcae03cb5cfefd11n, 0x762cf730a84e62a1n, 0x05bca617c5bfe574n, 0x1034f71a9ee562d9n,
  0xb30cfca75c02bfb7n, 0x118f6ec088ef56dan, 0x51c6ef028e3bcf91n, 0x7ea02ec3b6a95ef0n,
  0x24ecf556b10640f0n, 0xbb3522ba546cefb0n, 0x589efc11a6ef413bn, 0x904aef7245b08c90n,
  0xa77da68b1ee3f7e5n, 0x4c2b9f365ae0f7b0n, 0x72a5a51988efcb02n, 0xd0ebf35be4846430n,
  0x96efba12384fe84fn, 0x3d0bca253cf9bb2bn, 0x140a876ef4ea29d7n, 0xc1ff8a24564c76b9n,
  0x7cba2a6efca1b12bn, 0x8f2d5c19208aef3dn, 0x1ea5be19c72e2cf1n, 0x6e4e3b1aa05f082en,
  0x803dfba2e53efc97n, 0x71bc20bfe05a6ef4n, 0x5cbfeab258ee9d46n, 0x2bb95f3a09eecff6n,
  0x77ba5d198eeeb410n, 0x11ebca2ef490ae68n, 0x6eeebba7404ae6fan, 0x5a18a93e5eb60fc4n,
  0xee20a7b45caeeb71n, 0x11bf4a0dc67215c2n, 0x90f55cfef468e826n, 0x87a56cfecbf17d05n,
  0x06eebd73204de56en, 0x4a1876e4efbe2a81n, 0x1f5cfba38be708e9n, 0xb84ea05ffba7e324n,
  0x8e8ca0fe2456eeb0n, 0x12bbf64efba5a7c2n, 0x3e4ba0ef734cb8f0n, 0xa48feea65e219ba4n,
])

/* ---- Message Contracts ---- */

export interface FastCDCChunkResult {
  sha256: string
  md5: string
  size_bytes: number
  plaintext_size: number
  offset: number
}

export type FastCDCWorkerRequest = {
  type: 'hash'
  file: File
  passphrase?: string
}

export type FastCDCWorkerResponse =
  | { type: 'progress'; progress: number }
  | { type: 'complete'; chunks: FastCDCChunkResult[]; encryption_salt?: string }
  | { type: 'error'; error: string }

/* ---- Crypto Helpers ---- */

function bufferToHex(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let hex = ''
  for (let i = 0; i < bytes.length; i++) {
    hex += bytes[i].toString(16).padStart(2, '0')
  }
  return hex
}

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
 * FastCDC Content-Defined Chunking Engine.
 * Streams through File using slice windows, finding cut boundaries via rolling Gear hash.
 */
async function chunkFileFastCDC(
  file: File,
  passphrase?: string,
): Promise<{ chunks: FastCDCChunkResult[]; salt?: string }> {
  let cryptoKey: CryptoKey | undefined
  let encryptionSalt: string | undefined

  if (passphrase) {
    const derived = await deriveKeyPBKDF2(passphrase)
    cryptoKey = derived.key
    encryptionSalt = derived.salt
  }

  const chunks: FastCDCChunkResult[] = []
  let offset = 0
  let sequenceIndex = 0

  if (file.size === 0) {
    let chunkBuf: ArrayBuffer = new ArrayBuffer(0)
    if (cryptoKey && encryptionSalt) {
      chunkBuf = await encryptChunkPayload(chunkBuf, cryptoKey, encryptionSalt, 0)
    }
    const sha256Digest = await crypto.subtle.digest('SHA-256', chunkBuf)
    return {
      chunks: [
        {
          sha256: bufferToHex(sha256Digest),
          md5: md5(chunkBuf),
          size_bytes: chunkBuf.byteLength,
          plaintext_size: 0,
          offset: 0,
        },
      ],
      salt: encryptionSalt,
    }
  }

  while (offset < file.size) {
    const remaining = file.size - offset

    // If remaining bytes is smaller than or equal to MinSize, emit terminal chunk
    if (remaining <= MIN_SIZE) {
      const slice = file.slice(offset, file.size)
      let buf = await slice.arrayBuffer()
      const plainSize = buf.byteLength
      if (cryptoKey && encryptionSalt) {
        buf = await encryptChunkPayload(buf, cryptoKey, encryptionSalt, sequenceIndex)
      }
      const sha256Digest = await crypto.subtle.digest('SHA-256', buf)
      chunks.push({
        sha256: bufferToHex(sha256Digest),
        md5: md5(buf),
        size_bytes: buf.byteLength,
        plaintext_size: plainSize,
        offset,
      })
      break
    }

    // Read window of up to MAX_SIZE
    const windowSize = Math.min(remaining, MAX_SIZE)
    const windowSlice = file.slice(offset, offset + windowSize)
    const windowBuf = await windowSlice.arrayBuffer()
    const windowBytes = new Uint8Array(windowBuf)

    // Run FastCDC rolling Gear hash with MinSize skipping
    let hash = 0n
    let cut = windowSize
    const midPoint = Math.min(windowSize, AVG_SIZE)
    let found = false

    // Phase 1: Sub-Average Region [MIN_SIZE, AVG_SIZE) -> MaskSmall
    for (let i = MIN_SIZE; i < midPoint; i++) {
      hash = ((hash << 1n) & UINT64_MASK) + GEAR_TABLE[windowBytes[i]]
      if ((hash & MASK_SMALL) === 0n) {
        cut = i + 1
        found = true
        break
      }
    }

    // Phase 2: Post-Average Region [AVG_SIZE, MAX_SIZE) -> MaskLarge
    if (!found) {
      for (let i = midPoint; i < windowSize; i++) {
        hash = ((hash << 1n) & UINT64_MASK) + GEAR_TABLE[windowBytes[i]]
        if ((hash & MASK_LARGE) === 0n) {
          cut = i + 1
          break
        }
      }
    }

    // Chunk payload from window
    let chunkBuf = windowBuf.slice(0, cut)
    const plainSize = chunkBuf.byteLength
    if (cryptoKey && encryptionSalt) {
      chunkBuf = await encryptChunkPayload(chunkBuf, cryptoKey, encryptionSalt, sequenceIndex)
    }

    const sha256Digest = await crypto.subtle.digest('SHA-256', chunkBuf)
    chunks.push({
      sha256: bufferToHex(sha256Digest),
      md5: md5(chunkBuf),
      size_bytes: chunkBuf.byteLength,
      plaintext_size: plainSize,
      offset,
    })

    offset += cut
    sequenceIndex++

    // Post progress
    const progress = Math.min(99, Math.round((offset / file.size) * 100))
    const msg: FastCDCWorkerResponse = { type: 'progress', progress }
    ;(self as unknown as Worker).postMessage(msg)
  }

  return { chunks, salt: encryptionSalt }
}

/* ---- Worker Entry Point ---- */

self.onmessage = async (e: MessageEvent<FastCDCWorkerRequest>) => {
  const { type, file, passphrase } = e.data

  if (type !== 'hash' || !file) {
    const msg: FastCDCWorkerResponse = { type: 'error', error: 'Invalid FastCDC worker message.' }
    ;(self as unknown as Worker).postMessage(msg)
    return
  }

  try {
    const { chunks, salt } = await chunkFileFastCDC(file, passphrase)
    const msg: FastCDCWorkerResponse = { type: 'complete', chunks, encryption_salt: salt }
    ;(self as unknown as Worker).postMessage(msg)
  } catch (err) {
    const msg: FastCDCWorkerResponse = { type: 'error', error: (err as Error).message }
    ;(self as unknown as Worker).postMessage(msg)
  }
}

export {}
