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

// ListFileBlocks returns the ordered blocks (with sha256 and size_bytes) linked to a file,
// ordered by sequence_number. Used by range requests and byte-offset streaming.
func (r *BlockRepository) ListFileBlocks(ctx context.Context, fileID string) ([]*domain.Block, error) {
	const q = `
		SELECT b.id, b.sha256, b.size_bytes, b.created_at
		FROM file_blocks fb
		JOIN blocks b ON b.id = fb.block_id
		WHERE fb.file_id = $1
		ORDER BY fb.sequence_number ASC
	`
	rows, err := r.db.QueryContext(ctx, q, fileID)
	if err != nil {
		return nil, fmt.Errorf("query file blocks: %w", err)
	}
	defer rows.Close()

	var out []*domain.Block
	for rows.Next() {
		var b domain.Block
		if err := rows.Scan(&b.ID, &b.SHA256, &b.SizeBytes, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan file block: %w", err)
		}
		out = append(out, &b)
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

// ReplaceBlocksForFile atomically deletes existing file_blocks and inserts new ones.
func (r *BlockRepository) ReplaceBlocksForFile(ctx context.Context, fileID string, blockIDsWithSeq []domain.BlockSequence) error {
	qDelete := `DELETE FROM file_blocks WHERE file_id = $1`

	execRebuild := func(tx DBTX) error {
		if _, err := tx.ExecContext(ctx, qDelete, fileID); err != nil {
			return fmt.Errorf("delete old file_blocks: %w", err)
		}

		if len(blockIDsWithSeq) > 0 {
			qInsert := `
				INSERT INTO file_blocks (file_id, block_id, sequence_number)
				VALUES ($1, $2, $3)
			`
			for _, bs := range blockIDsWithSeq {
				if _, err := tx.ExecContext(ctx, qInsert, fileID, bs.BlockID, bs.SequenceNumber); err != nil {
					return fmt.Errorf("insert file_blocks seq=%d: %w", bs.SequenceNumber, err)
				}
			}
		}
		return nil
	}

	if _, ok := r.db.(txCommitter); ok {
		return execRebuild(r.db)
	}

	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("ReplaceBlocksForFile: unsupported DBTX type %T", r.db)
	}
	return RunInTx(ctx, db, execRebuild)
}

// AllBlockHashes returns the sha256 of every row in the blocks table, ordered
// for stable iteration. Used exclusively by the GC collector to build the
// authoritative set of known blocks and identify orphans in storage.
func (r *BlockRepository) AllBlockHashes(ctx context.Context) ([]string, error) {
	const q = `
		SELECT DISTINCT b.sha256
		FROM blocks b
		WHERE EXISTS (SELECT 1 FROM file_blocks fb WHERE fb.block_id = b.id)
		   OR EXISTS (SELECT 1 FROM file_versions fv WHERE b.sha256 = ANY(fv.chunk_hashes))
		   OR EXISTS (SELECT 1 FROM session_blocks sb JOIN upload_sessions us ON us.id = sb.session_id
		             WHERE sb.block_hash = b.sha256 AND us.status = 'INITIATED'
	               AND us.updated_at > CURRENT_TIMESTAMP - ($1 * INTERVAL '1 second'))
		UNION
		SELECT sb.block_hash
		FROM session_blocks sb
		JOIN upload_sessions us ON us.id = sb.session_id
		WHERE us.status = 'INITIATED'
		  AND us.updated_at > CURRENT_TIMESTAMP - ($1 * INTERVAL '1 second')
		ORDER BY sha256
	`
	rows, err := r.db.QueryContext(ctx, q, domain.UploadSessionMaxAge.Seconds())
	if err != nil {
		return nil, fmt.Errorf("query all block hashes: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan block hash: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// WithBlockCandidate serializes GC against upload-session creation for this
// hash. The transaction-scoped advisory lock remains held until storage deletion
// completes, so a new session cannot start referencing the object mid-sweep.
func (r *BlockRepository) WithBlockCandidate(ctx context.Context, hash string, dryRun bool, deleteObject func() error) (bool, error) {
	db, ok := r.db.(*sql.DB)
	if !ok {
		return false, fmt.Errorf("WithBlockCandidate requires a database pool, got %T", r.db)
	}
	eligible := false
	err := RunInTx(ctx, db, func(tx DBTX) error {
		lockKey := "blobcloud:block:" + hash
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
			return fmt.Errorf("lock block for GC: %w", err)
		}
		if !dryRun {
			if _, err := tx.ExecContext(ctx, `
			UPDATE upload_sessions us SET status = 'ABORTED'
			WHERE us.status = 'INITIATED'
			  AND us.updated_at <= CURRENT_TIMESTAMP - ($2 * INTERVAL '1 second')
			  AND EXISTS (SELECT 1 FROM session_blocks sb WHERE sb.session_id = us.id AND sb.block_hash = $1)
			`, hash, domain.UploadSessionMaxAge.Seconds()); err != nil {
				return fmt.Errorf("expire stale upload sessions for block: %w", err)
			}
		}
		inUse, err := r.WithTx(tx).isBlockReferenced(ctx, hash)
		if err != nil {
			return err
		}
		if inUse {
			return nil
		}
		if !dryRun {
			if _, err := tx.ExecContext(ctx, `DELETE FROM blocks WHERE sha256 = $1`, hash); err != nil {
				return fmt.Errorf("remove orphan block metadata: %w", err)
			}
		}
		eligible = true
		if err := deleteObject(); err != nil {
			return err
		}
		return nil
	})
	return eligible, err
}

// ExpireStaleUploadSessions transitions inactive upload sessions to ABORTED.
// This invalidates their completion path before GC removes any associated data.
func (r *BlockRepository) ExpireStaleUploadSessions(ctx context.Context, dryRun bool) (int64, error) {
	const cutoff = `CURRENT_TIMESTAMP - ($1 * INTERVAL '1 second')`
	if dryRun {
		var count int64
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM upload_sessions WHERE status = 'INITIATED' AND updated_at <= `+cutoff, domain.UploadSessionMaxAge.Seconds()).Scan(&count); err != nil {
			return 0, fmt.Errorf("count stale upload sessions: %w", err)
		}
		return count, nil
	}
	const q = `
		UPDATE upload_sessions
		SET status = 'ABORTED'
		WHERE status = 'INITIATED'
		  AND updated_at <= CURRENT_TIMESTAMP - ($1 * INTERVAL '1 second')
	`
	result, err := r.db.ExecContext(ctx, q, domain.UploadSessionMaxAge.Seconds())
	if err != nil {
		return 0, fmt.Errorf("expire stale upload sessions: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count expired upload sessions: %w", err)
	}
	return count, nil
}

func (r *BlockRepository) isBlockReferenced(ctx context.Context, hash string) (bool, error) {
	const q = `
		SELECT EXISTS (
		        SELECT 1 FROM file_blocks fb JOIN blocks b ON b.id = fb.block_id WHERE b.sha256 = $1
		    ) OR EXISTS (SELECT 1 FROM file_versions fv WHERE $1 = ANY(fv.chunk_hashes))
		      OR EXISTS (
				SELECT 1 FROM session_blocks sb
				JOIN upload_sessions us ON us.id = sb.session_id
				WHERE sb.block_hash = $1 AND us.status = 'INITIATED'
				  AND us.updated_at > CURRENT_TIMESTAMP - ($2 * INTERVAL '1 second')
			)
	`
	var inUse bool
	if err := r.db.QueryRowContext(ctx, q, hash, domain.UploadSessionMaxAge.Seconds()).Scan(&inUse); err != nil {
		return false, fmt.Errorf("check block usage: %w", err)
	}
	return inUse, nil
}

// WithStagingCandidate locks the upload session row across staging deletion.
// Active sessions are preserved; terminal or missing sessions can be collected
// after the configured object-age grace period.
func (r *BlockRepository) WithStagingCandidate(ctx context.Context, sessionID string, dryRun bool, deleteObject func() error) (bool, error) {
	db, ok := r.db.(*sql.DB)
	if !ok {
		return false, fmt.Errorf("WithStagingCandidate requires a database pool, got %T", r.db)
	}
	eligible := false
	err := RunInTx(ctx, db, func(tx DBTX) error {
		var status string
		var fresh bool
		err := tx.QueryRowContext(ctx, `SELECT status, updated_at > CURRENT_TIMESTAMP - ($2 * INTERVAL '1 second') FROM upload_sessions WHERE id = $1 FOR UPDATE`, sessionID, domain.UploadSessionMaxAge.Seconds()).Scan(&status, &fresh)
		if errors.Is(err, sql.ErrNoRows) {
			status = ""
		} else if err != nil {
			return fmt.Errorf("lock upload session for staging GC: %w", err)
		}
		if status == "INITIATED" && fresh {
			return nil
		}
		if status == "INITIATED" && !dryRun {
			if _, err := tx.ExecContext(ctx, `UPDATE upload_sessions SET status = 'ABORTED' WHERE id = $1 AND status = 'INITIATED'`, sessionID); err != nil {
				return fmt.Errorf("expire stale upload session: %w", err)
			}
		}
		eligible = true
		if err := deleteObject(); err != nil {
			return err
		}
		return nil
	})
	return eligible, err
}
