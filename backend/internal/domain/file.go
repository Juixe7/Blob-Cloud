package domain

import (
	"context"
	"time"
)

// File represents both files and directories in the `files` table. The
// is_directory flag discriminates them; parent_id forms the folder hierarchy
// (adjacency-list model).
type File struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Name        string    `json:"name"`
	ParentID    *string   `json:"parent_id,omitempty"`
	IsDirectory bool      `json:"is_directory"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// FileRepository abstracts persistence for File aggregates.
type FileRepository interface {
	// Create inserts a new file or directory row.
	Create(ctx context.Context, file *File) error
	// GetByID returns the file with the given id, or an error wrapping
	// sql.ErrNoRows when not found.
	GetByID(ctx context.Context, id string) (*File, error)
	// ListDirectory returns the immediate children of parentID for a user.
	// A nil parentID lists the user's root (top-level) entries.
	ListDirectory(ctx context.Context, userID string, parentID *string) ([]*File, error)
}
