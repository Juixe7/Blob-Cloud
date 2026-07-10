package domain

import (
	"context"
	"time"
)

// ---------------------------------------------------------------------------
// Upload session statuses. Stored as varchar in the DB; these constants keep
// the spelling consistent across service, repository, and transport layers.
// ---------------------------------------------------------------------------

const (
	SessionStatusInitiated = "INITIATED"
	SessionStatusCompleted = "COMPLETED"
	SessionStatusAborted   = "ABORTED"
)

// UploadSession models a resumable, multi-chunk upload. A session is created
// at /api/upload/initiate and transitions INITIATED -> COMPLETED (or ABORTED).
type UploadSession struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Filename  string    `json:"filename"`
	ParentID  *string   `json:"parent_id,omitempty"`
	TotalSize int64     `json:"total_size"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionBlock is one chunk within an upload session. IsUploaded flips to true
// once the client has PUT the chunk to storage; SizeBytes is needed at
// completion time to populate the global blocks table.
type SessionBlock struct {
	SessionID      string `json:"session_id"`
	BlockHash      string `json:"block_hash"`
	SequenceNumber int    `json:"sequence_number"`
	SizeBytes      int32  `json:"size_bytes"`
	IsUploaded     bool   `json:"is_uploaded"`
}

// UploadSessionRepository abstracts persistence for resumable uploads.
type UploadSessionRepository interface {
	// CreateSession atomically inserts the session and all of its blocks. The
	// session and its blocks must land together, so implementations run this in
	// a single transaction.
	CreateSession(ctx context.Context, session *UploadSession, blocks []SessionBlock) error
	// GetSessionByID returns the session and its blocks ordered by sequence.
	GetSessionByID(ctx context.Context, id string) (*UploadSession, []SessionBlock, error)
	// UpdateSessionStatus transitions a session's status (e.g. to COMPLETED).
	UpdateSessionStatus(ctx context.Context, id string, status string) error
	// MarkBlockUploaded flips one chunk's is_uploaded flag after the client
	// confirms a successful PUT to storage.
	MarkBlockUploaded(ctx context.Context, sessionID string, seqNum int) error
}

// ---------------------------------------------------------------------------
// Sharing roles. Higher rank implies the lower: OWNER > EDITOR > VIEWER. The
// permission check uses an explicit rank table rather than string compare so
// "EDITOR satisfies a VIEWER requirement" is trivially correct.
// ---------------------------------------------------------------------------

const (
	RoleViewer = "VIEWER"
	RoleEditor = "EDITOR"
	RoleOwner  = "OWNER"
)

// Permission models an access grant on a file/folder for a user (by email).
type Permission struct {
	ID          string    `json:"id"`
	FileID      string    `json:"file_id"`
	GranteeEmail string   `json:"grantee_email"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

// PermissionRepository abstracts persistence for sharing.
type PermissionRepository interface {
	// GrantPermission inserts a permission row. A duplicate (file_id,
	// grantee_email) violates the unique constraint and surfaces as an error.
	GrantPermission(ctx context.Context, perm *Permission) error
	// GetPermissionsByFile lists all grants for a file.
	GetPermissionsByFile(ctx context.Context, fileID string) ([]*Permission, error)
	// CheckUserPermission returns true if granteeEmail holds one of
	// requiredRoles on fileID, walking up the folder hierarchy via a recursive
	// CTE so a permission on an ancestor folder covers its descendants.
	CheckUserPermission(ctx context.Context, fileID string, userEmail string, requiredRoles []string) (bool, error)
}
