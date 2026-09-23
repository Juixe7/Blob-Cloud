/**
 * Real-time WebSocket synchronization & Delta Sync Engine types.
 *
 * These mirror the event names and payload shapes broadcast by the Go
 * backend's Hub (internal/sync/hub.go). The backend sends a JSON envelope:
 *
 *   { "type": "SYNC_DELTA", "payload": { ... } }
 *
 * Each event type below corresponds to a backend EventXxx constant.
 */

/** Discriminant for incoming WS messages. Matches the Go Hub constants. */
export type WebSocketEventType =
  | 'UPLOAD_COMPLETED'
  | 'THUMBNAIL_READY'
  | 'FILE_SHARED'
  | 'SHARE_INVITATION'
  | 'FORCE_LOGOUT'
  | 'VIRUS_DETECTED'
  | 'SYNC_DELTA'
  | 'AI_METADATA_READY'

/** The wire envelope the Go backend pushes to every connected client. */
export interface WSMessage<T = unknown> {
  type: WebSocketEventType
  payload: T
}

/** Actions recorded in the append-only journal entries table. */
export type JournalAction =
  | 'FILE_CREATED'
  | 'FILE_UPDATED'
  | 'FILE_DELETED'
  | 'FILE_MOVED'
  | 'FILE_RESTORED'

/** A single append-only change log entry from GET /api/sync/delta. */
export interface JournalEntry {
  cursor: number
  user_id: string
  file_id: string
  action: JournalAction
  parent_id: string | null
  name: string
  is_directory: boolean
  size_bytes: number
  mime_type?: string
  status: string
  thumbnail_url?: string | null
  created_at: string
}

/** Response payload from GET /api/sync/delta?since=<cursor>. */
export interface DeltaSyncResponse {
  entries: JournalEntry[]
  next_cursor: number
  has_more: boolean
}

/** Payload for SYNC_DELTA push notification. */
export interface SyncDeltaPayload {
  cursor: number
  action: JournalAction
  file_id: string
}

/** Payload for THUMBNAIL_READY — the SQS worker finished processing. */
export interface ThumbnailReadyPayload {
  file_id: string
  thumbnail_url: string
}

/** Payload for UPLOAD_COMPLETED — fired when /upload/complete finishes. */
export interface UploadCompletedPayload {
  file_id: string
  session_id?: string
}

/** Payload for FILE_SHARED — fired when another user shares a file with you. */
export interface FileSharedPayload {
  file_id: string
  filename: string
  shared_by: string
}

/** Payload for SHARE_INVITATION — fired when a user receives a new share invitation. */
export interface ShareInvitationPayload {
  invitation_id: string
  file_id: string
  filename: string
  role: string
  shared_by: string
  message?: string
  expires_at: string
}

/** Payload for VIRUS_DETECTED — fired when ClamAV blocks a file. */
export interface VirusDetectedPayload {
  file_id: string
  filename: string
  virus_name: string
}

/** Lifecycle of the underlying socket connection. Drives the sidebar dot. */
export type WebSocketStatus =
  | 'CONNECTING'
  | 'CONNECTED'
  | 'DISCONNECTED'
  | 'RECONNECTING'
