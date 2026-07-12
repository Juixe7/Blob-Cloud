/**
 * Core data contracts mapping to the Go backend database models.
 * These are used throughout the file explorer (Dashboard, ListView, GridView,
 * Sidebar, etc.) and must stay in sync with the backend's JSON responses.
 */

/** A file or folder record returned by GET /api/files. */
export interface FileItem {
  id: string
  user_id: string
  name: string
  parent_id: string | null
  is_directory: boolean
  size_bytes: number
  created_at: string
  updated_at: string
}

/** A single node in the clickable breadcrumb trail. */
export interface BreadcrumbNode {
  /** null represents the root "My Drive" level. */
  id: string | null
  name: string
}

/* ------------------------------------------------------------------ *
 * Phase 7.3 — Upload pipeline types
 *
 * These match the Go backend upload handlers EXACTLY (verified from
 * internal/service/upload_service.go and upload_handlers.go). The backend
 * enables strict decoding (DisallowUnknownFields), so field names must be
 * snake_case and no extra fields may be sent.
 * ------------------------------------------------------------------ */

/** A 4MB slice of a file plus its SHA-256 hash (client-side concept). */
export interface ChunkMetadata {
  sha256: string
  size_bytes: number
  /** The actual binary slice of the file. Never sent over the API. */
  blob: Blob
}

/** Lifecycle states for a single upload job. */
export type UploadStatus =
  | 'IDLE'
  | 'HASHING'
  | 'INITIATING'
  | 'UPLOADING'
  | 'COMPLETING'
  | 'COMPLETED'
  | 'FAILED'

/** A single upload tracked in the global queue. */
export interface UploadJob {
  /** Unique random ID for this upload run. */
  id: string
  filename: string
  totalSize: number
  status: UploadStatus
  /** Aggregate percentage (0 to 100). */
  progress: number
  error?: string
}

/* ---- API request/response contracts (snake_case, match backend) ---- */

/** Chunk descriptor sent to POST /api/upload/initiate. */
export interface InitiateChunk {
  sha256: string
  size_bytes: number
}

/** POST /api/upload/initiate request body. */
export interface InitiateRequest {
  filename: string
  parent_id: string | null
  user_id: string
  total_size: number
  chunks: InitiateChunk[]
}

/** Per-chunk entry in the initiate response. */
export interface InitiateRespChunk {
  sha256: string
  size_bytes: number
  sequence_number: number
  /** Dedup hit: block already exists in S3, skip the PUT. */
  already_exists: boolean
  /** Presigned S3 URL. Omitted when already_exists is true (omitempty). */
  upload_url?: string
}

/** POST /api/upload/initiate response body (HTTP 201). */
export interface InitiateResponse {
  session_id: string
  status: string
  chunks: InitiateRespChunk[]
}

/** POST /api/upload/complete request body. */
export interface CompleteRequest {
  session_id: string
}

/** POST /api/upload/complete response body (HTTP 200). */
export interface CompleteResponse {
  session_id: string
  status: string
  file_id: string
}
