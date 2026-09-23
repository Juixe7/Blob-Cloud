package domain

import (
	"context"
	"errors"
	"time"
)

// ErrPermissionNotFound is returned when no explicit or inherited permission exists for a user.
var ErrPermissionNotFound = errors.New("permission not found")

// File represents both files and directories in the `files` table. The
// is_directory flag discriminates them; parent_id forms the folder hierarchy
// (adjacency-list model).
type File struct {
	ID               string     `json:"id"`
	UserID           string     `json:"user_id"`
	Name             string     `json:"name"`
	ParentID         *string    `json:"parent_id,omitempty"`
	Path             string     `json:"path"`
	IsDirectory      bool       `json:"is_directory"`
	SizeBytes        int64      `json:"size_bytes"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
	AggregateSize    *int64     `json:"aggregate_size,omitempty"`
	ItemCount        *int64     `json:"item_count,omitempty"`
	OriginalLocation *string    `json:"original_location,omitempty"`
	SharedAt         *time.Time `json:"shared_at,omitempty"`
	TargetID         *string    `json:"target_id,omitempty"`
	MimeType         string     `json:"mime_type,omitempty"`
	ShortcutTargetID *string    `json:"shortcut_target_id,omitempty"`
	Role             string     `json:"role,omitempty"`
	IsEncrypted      bool       `json:"is_encrypted"`
	EncryptionSalt   *string    `json:"encryption_salt,omitempty"`
	Tags             *string    `json:"tags,omitempty"`
	Summary          *string    `json:"summary,omitempty"`
	Status           string     `json:"status"`
}

// FileVersion represents a historical snapshot of a file's blocks and size.
type FileVersion struct {
	ID            string    `json:"id"`
	FileID        string    `json:"file_id"`
	VersionNumber int       `json:"version_number"`
	SizeBytes     int64     `json:"size_bytes"`
	ChunkHashes   []string  `json:"chunk_hashes"`
	CreatedAt     time.Time `json:"created_at"`
}

// ShareableLink represents a generated public link for a file/folder.
type ShareableLink struct {
	ID           string     `json:"id"`
	FileID       string     `json:"file_id"`
	LinkToken    string     `json:"link_token"`
	AccessTier   string     `json:"access_tier"`
	PasswordHash *string    `json:"password_hash,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// StorageCategoryUsage breaks down byte usage by file type.
type StorageCategoryUsage struct {
	Images    int64 `json:"images"`
	Documents int64 `json:"documents"`
	Media     int64 `json:"media"`
	Code      int64 `json:"code"`
	Other     int64 `json:"other"`
}

// UserStorageUsage represents real database-calculated storage metrics.
type UserStorageUsage struct {
	TotalUsedBytes      int64                `json:"total_used_bytes"`
	StorageLimit        int64                `json:"storage_limit_bytes"`
	ActiveSessionsCount int                  `json:"active_sessions_count"`
	Categories          StorageCategoryUsage `json:"categories"`
}

// BulkDeleteRequest represents POST /api/files/bulk/delete or DELETE /api/files/bulk/permanent.
type BulkDeleteRequest struct {
	IDs []string `json:"ids"`
}

// BulkRestoreRequest represents POST /api/files/bulk/restore.
type BulkRestoreRequest struct {
	IDs []string `json:"ids"`
}

// BulkMoveRequest represents POST /api/files/bulk/move.
type BulkMoveRequest struct {
	IDs      []string `json:"ids"`
	ParentID *string  `json:"parent_id"`
}

// BulkShareRequest represents POST /api/files/bulk/share.
type BulkShareRequest struct {
	IDs          []string `json:"ids"`
	GranteeEmail string   `json:"grantee_email"`
	Role         string   `json:"role"`
}

// FileRepository abstracts persistence for File aggregates.
type FileRepository interface {
	// Create inserts a new file or directory row.
	Create(ctx context.Context, file *File) error
	// GetByID returns the file with the given id, or an error wrapping
	// sql.ErrNoRows when not found.
	GetByID(ctx context.Context, id string) (*File, error)
	// GetResolvedPermission checks permissions recursively up the folder tree.
	GetResolvedPermission(ctx context.Context, fileID string, userEmail string) (string, error)
	// GetFolderByNameAndParent checks if a non-deleted directory with name and parentID exists for user.
	GetFolderByNameAndParent(ctx context.Context, userID, name string, parentID *string) (*File, error)
	// ListDirectory returns the immediate children of parentID for a user.
	// A nil parentID lists the user's root (top-level) entries.
	ListDirectory(ctx context.Context, userID string, parentID *string) ([]*File, error)
	// ListTrash returns all files/directories owned by user where deleted_at IS NOT NULL.
	ListTrash(ctx context.Context, userID string) ([]*File, error)
	// GetDescendants recursively retrieves all non-deleted files and subfolders nested under rootID.
	GetDescendants(ctx context.Context, rootID string) ([]*File, error)
	// GetSubtreeForItems recursively finds all nested files inside multiple file/folder roots and calculates their relative path.
	GetSubtreeForItems(ctx context.Context, ids []string, userID string) ([]*ZippableItem, error)
	// SoftDelete recursively sets deleted_at = CURRENT_TIMESTAMP for rootID and all nested descendants.
	SoftDelete(ctx context.Context, rootID string, userID string) error
	// BulkSoftDelete soft-deletes multiple file/folder roots in a single transaction.
	BulkSoftDelete(ctx context.Context, ids []string, userID string) error
	// Restore recursively sets deleted_at = NULL for rootID and all nested descendants.
	Restore(ctx context.Context, rootID string, userID string) error
	// BulkRestore restores multiple file/folder roots in a single transaction.
	BulkRestore(ctx context.Context, ids []string, userID string) error
	// BulkMove updates parent_id for multiple specified file IDs.
	BulkMove(ctx context.Context, ids []string, parentID *string, userID string) error
	// Update changes a file/folder's name and/or parent_id, refreshing
	// updated_at. Either field may be omitted by passing the zero value of the
	// pointer (nil parentID means "move to root"; empty name means "leave
	// unchanged"). The updated row is read back onto file.
	Update(ctx context.Context, file *File) error
	// MoveSubtree atomically moves a folder and all its nested descendants to a new parent,
	// updating all materialized paths in O(1) via prefix substitution.
	MoveSubtree(ctx context.Context, folderID string, newParentID *string, userID string) error
	// IsDescendant reports whether candidateID is the same as ancestorID or
	// nested anywhere beneath it in the folder tree. Used to reject moves that
	// would create a parent-cycle (moving a folder into itself or one of its
	// own descendants).
	IsDescendant(ctx context.Context, candidateID, ancestorID string) (bool, error)
	// DeleteRecursive removes a file/folder and, for directories, every nested
	// descendant. It runs against the receiver's DBTX (use WithTx to enroll it
	// in a caller-owned transaction). file_blocks and permissions rows are
	// purged by the schema's ON DELETE CASCADE.
	//
	// It returns the number of file rows removed and the sha256 hashes of
	// blocks that became orphaned (no remaining file_blocks references) as a
	// result of the delete — the caller deletes those physical objects from
	// storage as garbage collection.
	DeleteRecursive(ctx context.Context, rootID string, userID string) (deletedCount int64, orphanedBlockHashes []string, err error)
	// BulkHardDelete recursively hard-deletes multiple file/folder roots and compiles orphaned block hashes.
	BulkHardDelete(ctx context.Context, ids []string, userID string) (deletedCount int64, orphanedBlockHashes []string, err error)
	// CreateFileVersion inserts a new file_versions record.
	CreateFileVersion(ctx context.Context, version *FileVersion) error
	// GetFileVersions returns all historical versions for a file ordered by version_number desc.
	GetFileVersions(ctx context.Context, fileID string) ([]*FileVersion, error)
	GetFileVersion(ctx context.Context, versionID string) (*FileVersion, error)
	// UpdateAIMetadata saves the generated semantic tags, summary, and embedding (vector).
	UpdateAIMetadata(ctx context.Context, id string, tags, summary *string, embedding []float32) error
	// SemanticSearch performs a cosine similarity search using pgvector on the user's files.
	SemanticSearch(ctx context.Context, userID string, queryEmbedding []float32, limit int) ([]*File, error)
	// RecordView upserts a view record for the user and file, updating the viewed_at timestamp.
	RecordView(ctx context.Context, userID, fileID string) error
	// GetRecentViews returns the user's recently viewed files, ordered by most recent first.
	GetRecentViews(ctx context.Context, userID string, limit int) ([]*File, error)
	// UpdateStatus changes the status of a file.
	UpdateStatus(ctx context.Context, id string, status string) error

	// InsertFileChunks bulk inserts file chunks for semantic search.
	InsertFileChunks(ctx context.Context, chunks []*FileChunk) error
	// SemanticSearchChunks searches across all chunks and returns the matching files.
	SemanticSearchChunks(ctx context.Context, userID string, queryEmbedding []float32, limit int) ([]*File, error)
	// HybridSearch combines keyword/tag/summary text matching and vector semantic search across owned and shared files.
	HybridSearch(ctx context.Context, userID, userEmail, query string, queryEmbedding []float32, limit int) ([]*File, error)
}

// ZippableItem represents a nested file returned by the recursive CTE for archiving.
type ZippableItem struct {
	ID           string
	ParentID     *string
	Name         string
	MimeType     string
	RelativePath string
	TargetID     *string
}

// FileChunk represents a chunk of text from a file and its embedding.
type FileChunk struct {
	ID         string
	FileID     string
	ChunkIndex int
	ChunkText  string
	Embedding  []float32
}

