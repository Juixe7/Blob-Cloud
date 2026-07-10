package domain

import (
	"context"
	"time"
)

// Block represents a content-addressed physical storage unit in the `blocks`
// table. The sha256 doubles as the S3/local-storage object key, enabling global
// deduplication: identical blocks are stored once on disk.
type Block struct {
	ID        string    `json:"id"`
	SHA256    string    `json:"sha256"`
	SizeBytes int32     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// BlockSequence pairs a block ID with its position when reconstructing a file
// from its constituent blocks. Order matters: sequence_number defines it.
type BlockSequence struct {
	BlockID        string
	SequenceNumber int
}

// BlockRepository abstracts persistence for Block aggregates and the
// file<->block mapping table.
type BlockRepository interface {
	// Create inserts a new physical block row.
	Create(ctx context.Context, block *Block) error
	// GetByHash returns the block with the given sha256, or an error wrapping
	// sql.ErrNoRows when not found.
	GetByHash(ctx context.Context, sha256 string) (*Block, error)
	// GetMultipleByHashes returns all blocks whose sha256 is in hashes. Used by
	// the upload-initiate flow to determine which chunks already exist.
	GetMultipleByHashes(ctx context.Context, hashes []string) ([]*Block, error)
	// LinkBlocksToFile atomically writes the ordered file_blocks rows linking a
	// file to its constituent blocks. Implemented inside a single transaction
	// so a partial link set is rolled back on any failure.
	LinkBlocksToFile(ctx context.Context, fileID string, blockIDsWithSeq []BlockSequence) error
	// ListFileBlockHashes returns the sha256 hashes of all blocks linked to a
	// file, ordered by sequence_number. Used by the thumbnail processor to
	// assemble the image bytes from storage.
	ListFileBlockHashes(ctx context.Context, fileID string) ([]string, error)
}
