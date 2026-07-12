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
import type { HashWorkerResponse } from '../workers/hash.worker'

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
  uploadFile: (file: File, parentId: string | null) => void
  clearCompleted: () => void
}

const UploadContext = createContext<UploadContextValue | undefined>(undefined)

/** Generate a short unique ID for an upload job. */
function generateJobId(): string {
  return `upl_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`
}

export function UploadProvider({ children }: { children: ReactNode }) {
  const { user } = useAuth()
  const [jobs, setJobs] = useState<UploadJob[]>([])

  // Keep a ref to jobs so async closuresures read the latest snapshot without
  // re-subscribing on every state change.
  const jobsRef = useRef<UploadJob[]>([])
  jobsRef.current = jobs

  /** Patch a single job by id immutably. */
  const patchJob = useCallback((id: string, patch: Partial<UploadJob>) => {
    setJobs((prev) =>
      prev.map((j) => (j.id === id ? { ...j, ...patch } : j)),
    )
  }, [])

  /**
   * Run the full upload lifecycle for a single file. Each invocation owns its
   * own worker instance, which is terminated on completion/failure.
   */
  const uploadFile = useCallback(
    async (file: File, parentId: string | null) => {
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
        status: 'HASHING',
        progress: 0,
      }
      setJobs((prev) => [...prev, newJob])

      // Spawn the hashing worker.
      const worker = new Worker(
        new URL('../workers/hash.worker.ts', import.meta.url),
        { type: 'module' },
      )

      // Per-chunk uploaded-byte tracker for aggregate progress.
      const chunkBytesUploaded = new Map<string, number>()

      /** Mark a job as failed and tear down the worker. */
      const failJob = (message: string) => {
        patchJob(jobId, { status: 'FAILED', error: message, progress: 0 })
        worker.terminate()
      }

      try {
        /* ---- 1. HASHING ---- */
        const chunks = await new Promise<{
          sha256: string
          size_bytes: number
        }[]>((resolve, reject) => {
          worker.onmessage = (e: MessageEvent<HashWorkerResponse>) => {
            const msg = e.data
            if (msg.type === 'progress') {
              // Map 0..100 hashing progress onto the 0..30 band.
              const mapped = Math.round((msg.progress / 100) * HASH_BAND_END)
              patchJob(jobId, { progress: mapped })
            } else if (msg.type === 'complete') {
              resolve(msg.chunks)
            } else if (msg.type === 'error') {
              reject(new Error(msg.error))
            }
          }
          worker.onerror = (e) => reject(new Error(e.message || 'Worker error'))
          worker.postMessage({ type: 'hash', file })
        })

        worker.terminate()

        /* ---- 2. INITIATING ---- */
        patchJob(jobId, { status: 'INITIATING', progress: INITIATE_BAND_END })

        const initiateBody: InitiateRequest = {
          filename: file.name,
          parent_id: parentId,
          user_id: user.user_id,
          total_size: file.size,
          chunks: chunks.map((c) => ({ sha256: c.sha256, size_bytes: c.size_bytes })),
        }

        const initiateRes = await apiClient.post<InitiateResponse>(
          '/upload/initiate',
          initiateBody,
        )
        const session = initiateRes.data
        // eslint-disable-next-line no-console
        console.info('[upload] session initiated:', session.session_id)

        /* ---- 3. UPLOADING (direct S3 PUT, dedup-aware) ---- */
        patchJob(jobId, { status: 'UPLOADING' })

        // Initialize the byte tracker. Deduped chunks count as fully uploaded.
        for (const c of session.chunks) {
          chunkBytesUploaded.set(c.sha256, c.already_exists ? c.size_bytes : 0)
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

        // Match each missing chunk to its original slice by sequence_number.
        await Promise.all(
          missing.map((chunk) => {
            const offset = chunk.sequence_number * CHUNK_SIZE
            const blob = file.slice(offset, offset + chunk.size_bytes)

            return axios.put(chunk.upload_url as string, blob, {
              headers: { 'Content-Type': 'application/octet-stream' },
              onUploadProgress: (evt) => {
                const loaded = evt.loaded ?? 0
                chunkBytesUploaded.set(chunk.sha256, Math.min(loaded, chunk.size_bytes))
                recomputeProgress()
              },
            })
          }),
        )

        /* ---- 4. COMPLETING ---- */
        patchJob(jobId, { status: 'COMPLETING', progress: COMPLETING_BAND_END })

        const completeRes = await apiClient.post<CompleteResponse>(
          '/upload/complete',
          { session_id: session.session_id },
        )
        // eslint-disable-next-line no-console
        console.info('[upload] completed, file_id:', completeRes.data.file_id)

        /* ---- 5. FINALIZE ---- */
        patchJob(jobId, { status: 'COMPLETED', progress: 100 })
        window.dispatchEvent(new CustomEvent(UPLOAD_COMPLETE_EVENT))
      } catch (err) {
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

  /** Remove all COMPLETED and FAILED jobs from the queue. */
  const clearCompleted = useCallback(() => {
    setJobs((prev) =>
      prev.filter(
        (j) => j.status !== 'COMPLETED' && j.status !== 'FAILED',
      ),
    )
  }, [])

  const value = useMemo<UploadContextValue>(
    () => ({ jobs, uploadFile, clearCompleted }),
    [jobs, uploadFile, clearCompleted],
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
