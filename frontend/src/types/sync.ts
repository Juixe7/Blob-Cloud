/**
 * Phase 7.5 — Real-time WebSocket synchronization types.
 *
 * These mirror the event names and payload shapes broadcast by the Go
 * backend's Hub (internal/sync/hub.go). The backend sends a JSON envelope:
 *
 *   { "type": "THUMBNAIL_READY", "payload": { ... } }
 *
 * Each event type below corresponds to a backend EventXxx constant.
 */

/** Discriminant for incoming WS messages. Matches the Go Hub constants. */
export type WebSocketEventType =
  | 'UPLOAD_COMPLETED'
  | 'THUMBNAIL_READY'
  | 'FILE_SHARED'
  | 'FORCE_LOGOUT'
  | 'VIRUS_DETECTED'

/** The wire envelope the Go backend pushes to every connected client. */
export interface WSMessage<T = unknown> {
  type: WebSocketEventType
  payload: T
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
