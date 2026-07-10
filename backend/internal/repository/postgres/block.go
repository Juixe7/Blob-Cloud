package postgresrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"go-drive-clone/internal/domain"
)

// BlockRepository is the Postgres implementation of domain.BlockRepository.
//
// It holds a DBTX, which may be either the connection pool (*sql.DB) or an
// in-flight transaction (*sql.Tx). WithTx returns a copy bound to a specific
// transaction so this repository can participate in a caller-owned tx.
type BlockRepository struct {
	db DBTX
}

// NewBlockRepository constructs a BlockRepository bound to the given pool.
func NewBlockRepository(db *sql.DB) *BlockRepository {
	return &BlockRepository{db: db}
}

// WithTx returns a copy of the repository that runs against tx. Callers use
// this inside RunInTx to make repository writes participate in one transaction.
func (r *BlockRepository) WithTx(tx DBTX) *BlockRepository {
	return &BlockRepository{db: tx}
}

// Create inserts block and reads back the DB-generated id/created_at.
func (r *BlockRepository) Create(ctx context.Context, block *domain.Block) error {
	const q = `
		INSERT INTO blocks (sha256, size_bytes)
		VALUES ($1, $2)
		RETURNING id, created_at
	`
	row := r.db.QueryRowContext(ctx, q, block.SHA256, block.SizeBytes)
	if err := row.Scan(&block.ID, &block.CreatedAt); err != nil {
		return fmt.Errorf("insert block: %w", err)
	}
	return nil
}

// GetOrCreate fetches the block by hash, or inserts it if absent (deduplication
// upsert). It returns the block's stable ID either way. ON CONFLICT makes this
// race-safe under concurrent uploads of the same hash.
//
// This is the core of single-instance storage: two files referencing the same
// block both resolve to the same blocks.id, and the physical S3/local object is
// stored exactly once.
func (r *BlockRepository) GetOrCreate(ctx context.Context, block *domain.Block) error {
	const q = `
		WITH ins AS (
			INSERT INTO blocks (sha256, size_bytes)
			VALUES ($1, $2)
			ON CONFLICT (sha256) DO NOTHING
			RETURNING id, created_at
		)
		SELECT id, created_at FROM ins
		UNION ALL
		SELECT id, created_at FROM blocks WHERE sha256 = $1
		LIMIT 1
	`
	if err := r.db.QueryRowContext(ctx, q, block.SHA256, block.SizeBytes).
		Scan(&block.ID, &block.CreatedAt); err != nil {
		return fmt.Errorf("get-or-create block %s: %w", block.SHA256, err)
	}
	return nil
}

// GetByHash returns the block with the given sha256.
func (r *BlockRepository) GetByHash(ctx context.Context, sha256 string) (*domain.Block, error) {
	const q = `
		SELECT id, sha256, size_bytes, created_at
		FROM blocks
		WHERE sha256 = $1
	`
	var b domain.Block
	err := r.db.QueryRowContext(ctx, q, sha256).Scan(&b.ID, &b.SHA256, &b.SizeBytes, &b.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("block by hash %q: %w", sha256, sql.ErrNoRows)
	case err != nil:
		return nil, fmt.Errorf("query block by hash: %w", err)
	}
	return &b, nil
}

// GetMultipleByHashes returns all blocks whose sha256 is in hashes. An empty
// input slice returns an empty result with no query, avoiding an IN () syntax
// error.
func (r *BlockRepository) GetMultipleByHashes(ctx context.Context, hashes []string) ([]*domain.Block, error) {
	if len(hashes) == 0 {
		return nil, nil
	}

	// Build a parameterised IN clause: ($1), ($2), ... we use a string builder
	// to keep allocation low for the typical (dozens of hashes) case.
	var sb strings.Builder
	sb.WriteString("SELECT id, sha256, size_bytes, created_at FROM blocks WHERE sha256 IN (")
	args := make([]any, 0, len(hashes))
	for i, h := range hashes {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("$%d", i+1))
		args = append(args, h)
	}
	sb.WriteByte(')')

	rows, err := r.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query blocks by hashes: %w", err)
	}
	defer rows.Close()

	var out []*domain.Block
	for rows.Next() {
		var b domain.Block
		if err := rows.Scan(&b.ID, &b.SHA256, &b.SizeBytes, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan block row: %w", err)
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

// ListFileBlockHashes returns the sha256 of every block linked to a file,
// ordered by sequence_number. This is used by the thumbnail processor to
// assemble the image from storage in the correct order.
func (r *BlockRepository) ListFileBlockHashes(ctx context.Context, fileID string) ([]string, error) {
	const q = `
		SELECT b.sha256
		FROM file_blocks fb
		JOIN blocks b ON b.id = fb.block_id
		WHERE fb.file_id = $1
		ORDER BY fb.sequence_number ASC
	`
	rows, err := r.db.QueryContext(ctx, q, fileID)
	if err != nil {
		return nil, fmt.Errorf("query file block hashes: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var sha256 string
		if err := rows.Scan(&sha256); err != nil {
			return nil, fmt.Errorf("scan block hash: %w", err)
		}
		out = append(out, sha256)
	}
	return out, rows.Err()
}

// LinkBlocksToFile writes all file_blocks rows for a file atomically. If the
// repository is bound to the pool it opens its own transaction; if it is bound
// to an in-flight transaction (via WithTx) it participates in that one. Either
// way, a partial link set is rolled back so reconstruction order stays valid.
func (r *BlockRepository) LinkBlocksToFile(ctx context.Context, fileID string, blockIDsWithSeq []domain.BlockSequence) error {
	if len(blockIDsWithSeq) == 0 {
		return nil
	}

	const q = `
		INSERT INTO file_blocks (file_id, block_id, sequence_number)
		VALUES ($1, $2, $3)
	`
	// Already inside a caller-owned transaction: just run the inserts.
	if _, ok := r.db.(txCommitter); ok {
		for _, bs := range blockIDsWithSeq {
			if _, err := r.db.ExecContext(ctx, q, fileID, bs.BlockID, bs.SequenceNumber); err != nil {
				return fmt.Errorf("insert file_blocks seq=%d: %w", bs.SequenceNumber, err)
			}
		}
		return nil
	}

	// Bound to the pool: wrap our own transaction.
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("LinkBlocksToFile: unsupported DBTX type %T", r.db)
	}
	return RunInTx(ctx, db, func(tx DBTX) error {
		for _, bs := range blockIDsWithSeq {
			if _, err := tx.ExecContext(ctx, q, fileID, bs.BlockID, bs.SequenceNumber); err != nil {
				return fmt.Errorf("insert file_blocks seq=%d: %w", bs.SequenceNumber, err)
			}
		}
		return nil
	})
}
