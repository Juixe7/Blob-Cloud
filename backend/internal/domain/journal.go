package domain

import (
	"context"
	"time"
)

// Journal action constants representing specific file mutations.
const (
	ActionFileCreated  = "FILE_CREATED"
	ActionFileUpdated  = "FILE_UPDATED"
	ActionFileRenamed  = "FILE_RENAMED"
	ActionFileMoved    = "FILE_MOVED"
	ActionFileTrashed  = "FILE_TRASHED"
	ActionFileRestored = "FILE_RESTORED"
	ActionFileDeleted  = "FILE_DELETED"
)

// JournalEntry represents a single change entry in the monotonic append-only delta log.
type JournalEntry struct {
	Cursor       int64     `json:"cursor"`
	UserID       string    `json:"user_id"`
	FileID       string    `json:"file_id"`
	Action       string    `json:"action"`
	ParentID     *string   `json:"parent_id"`
	Name         string    `json:"name"`
	IsDirectory  bool      `json:"is_directory"`
	SizeBytes    int64     `json:"size_bytes"`
	MimeType     string    `json:"mime_type,omitempty"`
	Status       string    `json:"status"`
	ThumbnailURL *string   `json:"thumbnail_url,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// JournalRepository defines operations on the append-only journal table.
type JournalRepository interface {
	Record(ctx context.Context, entry *JournalEntry) (int64, error)
	ListSince(ctx context.Context, userID string, sinceCursor int64, limit int) ([]*JournalEntry, int64, bool, error)
	GetLatestCursor(ctx context.Context, userID string) (int64, error)
}
