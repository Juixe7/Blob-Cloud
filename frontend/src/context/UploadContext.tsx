import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import axios from 'axios'
import { apiClient } from '../lib/api'
import { useAuth } from './AuthContext'
import type {
  UploadJob,
  UploadStatus,
  InitiateRequest,
  InitiateResponse,
  CompleteResponse,
} from '../types/file'
import type { FastCDCChunkResult, FastCDCWorkerResponse } from '../workers/fastcdc.worker'
import { deriveKeyPBKDF2, encryptChunkPayload } from '../lib/crypto'

/** Custom event dispatched on window when an upload finishes, so the file
 *  listing in Dashboard can refresh. */
export const UPLOAD_COMPLETE_EVENT = 'blobcloud:upload-complete'

/** Exact chunk size (must match the worker): 4 MiB. */
const CHUNK_SIZE = 4 * 1024 * 1024

/** Progress banding so the bar feels continuous across phases. */
const HASH_BAND_END = 30 // hashing phase: 0 → 30%
const INITIATE_BAND_END = 35 // initiate: 30 → 35%
const UPLOAD_BAND_END = 95 // uploading: 35 → 95%
const COMPLETING_BAND_END = 99 // completing: 95 → 99%
// COMPLETED = 100

export interface UploadContextValue {
  jobs: UploadJob[]
  uploadFile: (file: File, parentId: string | null, folderJobId?: string, passphrase?: string) => void
  uploadFolder: (files: File[], parentId: string | null, passphrase?: string) => Promise<void>
  clearCompleted: () => void
  isE2EEnabled: boolean
  setIsE2EEnabled: (val: boolean) => void
}

const UploadContext = createContext<UploadContextValue | undefined>(undefined)

/** Generate a short unique ID for an upload job. */
function generateJobId(): string {
  return `upl_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`
}

export function UploadProvider({ children }: { children: ReactNode }) {
  const { user } = useAuth()
  const [jobs, setJobs] = useState<UploadJob[]>([])
  const [isE2EEnabled, setIsE2EEnabled] = useState(false)

  // Keep a ref to jobs so async closures read the latest snapshot without
  // re-subscribing on every state change.
  const jobsRef = useRef<UploadJob[]>([])
  jobsRef.current = jobs

  /** Concurrency limiter to prevent browser tab freezes and OOM on massive folder uploads. */
  const MAX_CONCURRENT_UPLOADS = 3
  const uploadQueueRef = useRef<(() => Promise<void>)[]>([])
  const activeUploadsRef = useRef(0)

  /** Patch a single job by id immutably and propagate folder status. */
  const patchJob = useCallback((id: string, patch: Partial<UploadJob>) => {
    setJobs((prev) => {
      const next = prev.map((j) => (j.id === id ? { ...j, ...patch } : j))
      const target = next.find((j) => j.id === id)
      if (target?.folder_job_id) {
        const folderId = target.folder_job_id
        const siblings = next.filter((j) => j.folder_job_id === folderId)
        if (siblings.length > 0 && siblings.every((s) => s.status === 'COMPLETED' || s.status === 'FAILED')) {
          const hasFailed = siblings.some((s) => s.status === 'FAILED')
          const totalProgress = siblings.reduce((acc, s) => acc + s.progress, 0)
          const avgProgress = totalProgress / siblings.length
          return next.map((j) =>
            j.id === folderId
              ? {
                  ...j,
                  status: hasFailed ? 'FAILED' : 'COMPLETED',
                  progress: avgProgress,
                  error: hasFailed ? 'Some files failed to upload' : undefined,
                }
              : j,
          )
        }
      }
      return next
    })
  }, [])

  /** Dequeue waiting jobs up to MAX_CONCURRENT_UPLOADS. */
  const processQueue = useCallback(() => {
    while (activeUploadsRef.current < MAX_CONCURRENT_UPLOADS && uploadQueueRef.current.length > 0) {
      const nextTask = uploadQueueRef.current.shift()
      if (!nextTask) break
      activeUploadsRef.current++
      nextTask().finally(() => {
        activeUploadsRef.current--
        processQueue()
      })
    }
  }, [])

  /**
   * Run the full upload lifecycle for a single file. Each invocation owns its
   * own worker instance, which is terminated on completion/failure.
   */
  const executeUpload = useCallback(
    async (jobId: string, file: File, parentId: string | null, passphrase?: string) => {
      if (!user) {
        patchJob(jobId, { status: 'FAILED', error: 'User not authenticated', progress: 0 })
        return
      }
      patchJob(jobId, { status: 'HASHING' })

      // Spawn the FastCDC content-defined chunking worker.
      const worker = new Worker(
        new URL('../workers/fastcdc.worker.ts', import.meta.url),
        { type: 'module' },
      )

      // Per-chunk uploaded-byte tracker for aggregate progress (indexed by sequence_number).
      const chunkBytesUploaded = new Map<number, number>()

      /** Mark a job as failed and tear down the worker. */
      const failJob = (message: string) => {
        patchJob(jobId, { status: 'FAILED', error: message, progress: 0 })
        worker.terminate()
      }

      let activeSessionId: string | null = null

      try {
        /* ---- 1. HASHING (FastCDC Rolling Gear Hash with Dual Masks) ---- */
        const { chunks, encryptionSalt } = await new Promise<{ chunks: FastCDCChunkResult[], encryptionSalt?: string }>((resolve, reject) => {
          worker.onmessage = (e: MessageEvent<FastCDCWorkerResponse>) => {
            const msg = e.data
            if (msg.type === 'progress') {
              // Map 0..100 hashing progress onto the 0..30 band.
              const mapped = Math.round((msg.progress / 100) * HASH_BAND_END)
              patchJob(jobId, { progress: mapped })
            } else if (msg.type === 'complete') {
              resolve({ chunks: msg.chunks, encryptionSalt: msg.encryption_salt })
            } else if (msg.type === 'error') {
              reject(new Error(msg.error))
            }
          }
          worker.onerror = (e) => reject(new Error(e.message || 'Worker error'))
          worker.postMessage({ type: 'hash', file, passphrase })
        })

        worker.terminate()

        /* ---- 2. INITIATING ---- */
        patchJob(jobId, { status: 'INITIATING', progress: INITIATE_BAND_END })

        const initiateBody: InitiateRequest = {
          filename: file.name,
          parent_id: parentId,
          user_id: user.user_id,
          total_size: file.size,
          is_encrypted: !!passphrase,
          encryption_salt: encryptionSalt,
          chunks: chunks.map((c) => ({
            sha256: c.sha256,
            block_md5: c.md5,
            size_bytes: c.size_bytes,
          })),
        }

        const initiateRes = await apiClient.post<InitiateResponse>(
          '/upload/initiate',
          initiateBody,
        )
        const session = initiateRes.data
        activeSessionId = session.session_id
        // eslint-disable-next-line no-console
        console.info('[upload] session initiated:', session.session_id)

        /* ---- TOCTOU State Recording ---- */
        const toctouKey = `upload_toctou_${session.session_id}`
        localStorage.setItem(
          toctouKey,
          JSON.stringify({
            name: file.name,
            size: file.size,
            lastModified: file.lastModified,
          }),
        )

        /* ---- 3. UPLOADING (direct S3 PUT, dedup-aware) ---- */
        patchJob(jobId, { status: 'UPLOADING' })

        /* ---- TOCTOU Validation Gate ---- */
        const savedToctou = localStorage.getItem(toctouKey)
        if (savedToctou) {
          try {
            const saved = JSON.parse(savedToctou) as {
              size: number
              lastModified: number
            }
            if (file.size !== saved.size || file.lastModified !== saved.lastModified) {
              localStorage.removeItem(toctouKey)
              const warnMsg =
                'File modification detected. Upload aborted to prevent data corruption. Starting upload from scratch.'
              // eslint-disable-next-line no-alert
              alert(warnMsg)
              failJob(warnMsg)
              return
            }
          } catch {
            // Ignore parse error
          }
        }

        // Initialize the byte tracker. Deduped chunks count as fully uploaded.
        for (const c of session.chunks) {
          chunkBytesUploaded.set(c.sequence_number, c.already_exists ? c.size_bytes : 0)
        }

        /** Recompute aggregate progress from the byte tracker. */
        const recomputeProgress = () => {
          let uploaded = 0
          for (const v of chunkBytesUploaded.values()) uploaded += v
          const frac = file.size > 0 ? uploaded / file.size : 1
          // Map [0..1] onto the uploading band [INITIATE_BAND_END .. UPLOAD_BAND_END].
          const mapped =
            INITIATE_BAND_END +
            Math.round(frac * (UPLOAD_BAND_END - INITIATE_BAND_END))
          patchJob(jobId, { progress: Math.min(mapped, UPLOAD_BAND_END) })
        }

        // Build the list of PUTs needed. We re-slice the original file to get
        // raw bytes (Blob.slice is lazy / cheap) and match by sha256 + index.
        const missing = session.chunks.filter((c) => !c.already_exists && c.upload_url)

        // Initialize CryptoKey for main-thread chunk encryption if E2EE is active.
        let cryptoKey: CryptoKey | undefined
        if (passphrase && encryptionSalt) {
          const derived = await deriveKeyPBKDF2(passphrase, encryptionSalt)
          cryptoKey = derived.key
        }

        // Upload missing chunks with bounded concurrency (limit = 3) and exponential backoff retry.
        // This avoids saturating browser network connections and tripping upload rate limits.
        const CHUNK_UPLOAD_CONCURRENCY = 3
        const MAX_RETRIES = 3

        const uploadChunkWithRetry = async (chunk: typeof missing[0]) => {
          const chunkMeta = chunks[chunk.sequence_number]
          const offset = chunkMeta ? chunkMeta.offset : chunk.sequence_number * CHUNK_SIZE
          const plainSize = chunkMeta ? chunkMeta.plaintext_size : chunk.size_bytes
          const blobSlice = file.slice(offset, offset + plainSize)
          
          let uploadPayload: Blob | ArrayBuffer = blobSlice
          if (cryptoKey && encryptionSalt) {
            const arrayBuffer = await blobSlice.arrayBuffer()
            uploadPayload = await encryptChunkPayload(arrayBuffer, cryptoKey, encryptionSalt, chunk.sequence_number)
          }

          let attempt = 0
          while (attempt < MAX_RETRIES) {
            try {
              await axios.put(chunk.upload_url as string, uploadPayload, {
                headers: { 'Content-Type': 'application/octet-stream' },
                onUploadProgress: (evt) => {
                  const loaded = evt.loaded ?? 0
                  chunkBytesUploaded.set(chunk.sequence_number, Math.min(loaded, chunk.size_bytes))
                  recomputeProgress()
                },
              })
              return
            } catch (err) {
              attempt++
              if (attempt >= MAX_RETRIES) {
                throw err
              }
              // Exponential backoff before retry (e.g. 500ms, 1000ms)
              await new Promise((resolve) => setTimeout(resolve, attempt * 500))
            }
          }
        }

        let chunkCursor = 0
        const workerCount = Math.min(CHUNK_UPLOAD_CONCURRENCY, missing.length)
        if (workerCount > 0) {
          const pool = Array.from({ length: workerCount }, async () => {
            while (chunkCursor < missing.length) {
              const currentChunk = missing[chunkCursor++]
              await uploadChunkWithRetry(currentChunk)
            }
          })
          await Promise.all(pool)
        }


        /* ---- 4. COMPLETING ---- */
        patchJob(jobId, { status: 'COMPLETING', progress: COMPLETING_BAND_END })

        const completeBody = {
          session_id: activeSessionId,
          is_encrypted: !!passphrase,
          encryption_salt: encryptionSalt,
        }

        const completeRes = await apiClient.post<CompleteResponse>(
          '/upload/complete',
          completeBody,
        )
        // eslint-disable-next-line no-console
        console.info('[upload] completed, file_id:', completeRes.data.file_id)

        /* ---- 5. FINALIZE ---- */
        localStorage.removeItem(toctouKey)
        patchJob(jobId, { status: 'COMPLETED', progress: 100 })
        window.dispatchEvent(new CustomEvent(UPLOAD_COMPLETE_EVENT))
      } catch (err) {
        if (activeSessionId) {
          localStorage.removeItem(`upload_toctou_${activeSessionId}`)
        }
        let message = 'Upload failed.'
        if (axios.isAxiosError(err)) {
          const data = err.response?.data as { error?: string; message?: string } | undefined
          message = data?.error || data?.message || err.message || message
        } else if (err instanceof Error) {
          message = err.message
        }
        failJob(message)
      }
    },
    [user, patchJob],
  )

  /**
   * Enqueue a file upload task. Adds job in IDLE state and schedules it through
   * the bounded concurrency worker queue.
   */
  const uploadFile = useCallback(
    (file: File, parentId: string | null = null, folderJobId?: string, passphrase?: string) => {
      if (!user) {
        // eslint-disable-next-line no-console
        console.warn('[upload] no authenticated user — aborting')
        return
      }

      const jobId = generateJobId()
      const newJob: UploadJob = {
        id: jobId,
        filename: file.name,
        totalSize: file.size,
        status: 'IDLE',
        progress: 0,
        folder_job_id: folderJobId,
      }
      setJobs((prev) => [...prev, newJob])

      uploadQueueRef.current.push(() => executeUpload(jobId, file, parentId, passphrase))
      processQueue()
    },
    [user, executeUpload, processQueue],
  )

  /**
   * Recursive path resolution engine for folder uploads.
   * Recreates the folder tree via POST /api/folders (leveraging backend idempotency)
   * using a local PathCache map so duplicate requests are not made.
   */
  const uploadFolder = useCallback(
    async (files: File[], parentId: string | null, passphrase?: string) => {
      if (!user || files.length === 0) return

      // Local path cache for this batch: "Vacation/2026" -> "folder-uuid"
      const pathCache: Record<string, string> = {}
      
      const folderJobId = generateJobId()
      const folderName = files[0]?.webkitRelativePath?.split('/')[0] || 'Folder'
      const folderJob: UploadJob = {
        id: folderJobId,
        filename: `Folder: ${folderName}`,
        totalSize: files.reduce((acc, f) => acc + f.size, 0),
        status: 'HASHING',
        progress: 0,
        is_folder: true,
        file_count: files.length,
      }
      setJobs((prev) => [...prev, folderJob])

      for (const file of files) {
        const relPath = file.webkitRelativePath || file.name
        const parts = relPath.split('/').filter(Boolean)
        const dirParts = parts.slice(0, -1) // All segments except filename

        let currentParentId = parentId
        let cumulativePath = ''

        for (const segment of dirParts) {
          cumulativePath = cumulativePath ? `${cumulativePath}/${segment}` : segment

          if (pathCache[cumulativePath]) {
            currentParentId = pathCache[cumulativePath]
          } else {
            try {
              const res = await apiClient.post<{ id: string }>('/folders', {
                name: segment,
                parent_id: currentParentId,
              })
              const createdId = res.data.id
              pathCache[cumulativePath] = createdId
              currentParentId = createdId
            } catch (err) {
              // eslint-disable-next-line no-console
              console.error('[uploadFolder] failed to resolve folder segment:', segment, err)
              break
            }
          }
        }

        // Enqueue the file into its resolved leaf directory
        uploadFile(file, currentParentId, folderJobId, passphrase)
      }
    },
    [user, uploadFile],
  )

  /** Remove all COMPLETED and FAILED jobs from the queue. */
  const clearCompleted = useCallback(() => {
    setJobs((prev) => {
      const completedFolderIds = new Set<string>()
      for (const j of prev) {
        if (j.is_folder) {
          const children = prev.filter((c) => c.folder_job_id === j.id)
          if (children.length > 0 && children.every((c) => c.status === 'COMPLETED' || c.status === 'FAILED')) {
            completedFolderIds.add(j.id)
          }
        }
      }

      return prev.filter((j) => {
        if (j.is_folder && completedFolderIds.has(j.id)) return false
        if (j.folder_job_id && completedFolderIds.has(j.folder_job_id)) return false
        return j.status !== 'COMPLETED' && j.status !== 'FAILED'
      })
    })
  }, [])

  const value = useMemo<UploadContextValue>(
    () => ({ jobs, uploadFile, uploadFolder, clearCompleted, isE2EEnabled, setIsE2EEnabled }),
    [jobs, uploadFile, uploadFolder, clearCompleted, isE2EEnabled, setIsE2EEnabled],
  )

  return <UploadContext.Provider value={value}>{children}</UploadContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useUpload(): UploadContextValue {
  const ctx = useContext(UploadContext)
  if (!ctx) throw new Error('useUpload must be used within an <UploadProvider>')
  return ctx
}

export type { UploadStatus }
export default UploadContext
