/**
 * Core data contracts mapping to the Go backend database models.
 * These are used throughout the file explorer (Dashboard, ListView, GridView,
 * Sidebar, etc.) and must stay in sync with the backend's JSON responses.
 */

/** A file or folder record returned by GET /api/files. */
export type FileStatus = 'PROCESSING' | 'ACTIVE' | 'QUARANTINED';

export interface FileItem {
  id: string
  user_id: string
  name: string
  status: FileStatus
  parent_id: string | null
  is_directory: boolean
  size_bytes: number
  created_at: string
  updated_at: string
  deleted_at?: string | null
  /** Aggregate size in bytes for deleted folders in Trash. */
  aggregate_size?: number
  /** Nested sub-items count for deleted folders in Trash. */
  item_count?: number
  /** Original parent folder name prior to deletion. */
  original_location?: string
  /** Optional thumbnail URL, populated when the SQS worker finishes. Pushed
   *  over WebSocket via THUMBNAIL_READY and merged into local state. */
  thumbnail_url?: string
  /** The date/time when this file/folder was shared. */
  shared_at?: string
  /** Optional target file/folder ID when this item is a shortcut. */
  target_id?: string | null
  /** User's role on this file/folder. */
  role?: 'OWNER' | 'EDITOR' | 'VIEWER'
  /** MIME type, standard or Google Workspace mapped */
  mime_type?: string
  /** The target ID if this is a shortcut */
  shortcut_target_id?: string | null
  /** Whether the file is E2E encrypted */
  is_encrypted?: boolean
  /** E2E encryption salt */
  encryption_salt?: string | null
  /** AI-generated tags (comma-separated) */
  tags?: string | null
  /** AI-generated document summary */
  summary?: string | null
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
  /** Human-readable string for failed uploads. */
  error?: string
  /** If this job represents an aggregated folder upload. */
  is_folder?: boolean
  /** The number of files in the aggregated folder upload. */
  file_count?: number
  /** ID of the parent folder job, if this file is part of a folder upload. */
  folder_job_id?: string
}

/* ---- API request/response contracts (snake_case, match backend) ---- */

/** Chunk descriptor sent to POST /api/upload/initiate. */
export interface InitiateChunk {
  sha256: string
  block_md5?: string
  size_bytes: number
}

/** POST /api/upload/initiate request body. */
export interface InitiateRequest {
  filename: string
  parent_id: string | null
  user_id: string
  total_size: number
  is_encrypted?: boolean
  encryption_salt?: string
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
  is_encrypted?: boolean
  encryption_salt?: string
}

/** POST /api/upload/complete response body (HTTP 200). */
export interface CompleteResponse {
  file_id: string
  status: string
  message: string
}

/* ------------------------------------------------------------------ *
 * Phase C.1 — Bulk Operations API Contracts
 * ------------------------------------------------------------------ */

export interface BulkDeleteRequest {
  ids: string[]
}

export interface BulkRestoreRequest {
  ids: string[]
}

export interface BulkMoveRequest {
  ids: string[]
  parent_id: string | null
}

export interface BulkShareRequest {
  ids: string[]
  grantee_email: string
  role: 'VIEWER' | 'EDITOR'
}

/* ------------------------------------------------------------------ *
 * Phase 7.4 — File operations & sharing types
 *
 * Contracts for the context-menu action layer: sharing (Upgrade B),
 * rename, move (parent_id change), and delete. Snake_case to match the
 * Go backend's strict JSON decoder.
 * ------------------------------------------------------------------ */

/** Roles assignable to a collaborator. OWNER is read-only from the API. */
export type CollaboratorRole = 'VIEWER' | 'EDITOR' | 'OWNER'

/** A permission row returned by GET /api/files/{id}/permissions. */
export interface CollaboratorPermission {
  id: string
  file_id: string
  grantee_email: string
  role: CollaboratorRole
  created_at: string
}

/** POST /api/files/{id}/share request body. OWNER cannot be assigned. */
export interface ShareRequest {
  grantee_email: string
  role: Exclude<CollaboratorRole, 'OWNER'>
}

/** PATCH /api/files/{id} request body. Either or both fields may be sent. */
export interface RenameMoveRequest {
  name?: string
  parent_id?: string | null
}
