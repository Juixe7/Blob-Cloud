package postgresrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go-drive-clone/internal/domain"
)

// UploadSessionRepository persists resumable uploads and their per-chunk state.
type UploadSessionRepository struct {
	db DBTX
}

// NewUploadSessionRepository constructs an UploadSessionRepository bound to db.
func NewUploadSessionRepository(db DBTX) *UploadSessionRepository {
	return &UploadSessionRepository{db: db}
}

// WithTx returns a copy bound to tx, so session writes can participate in a
// caller-owned transaction (used by the complete flow).
func (r *UploadSessionRepository) WithTx(tx DBTX) *UploadSessionRepository {
	return &UploadSessionRepository{db: tx}
}

// CreateSession inserts the session row and all of its blocks inside a single
// transaction. A half-created session (row without blocks) would be useless to
// resume, so atomicity here is required.
func (r *UploadSessionRepository) CreateSession(ctx context.Context, session *domain.UploadSession, blocks []domain.SessionBlock) error {
	const sessionQ = `
		INSERT INTO upload_sessions (user_id, filename, parent_id, total_size, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at
	`
	const blockQ = `
		INSERT INTO session_blocks (session_id, block_hash, sequence_number, size_bytes, is_uploaded)
		VALUES ($1, $2, $3, $4, $5)
	`

	// Use a tx whether bound to pool or caller tx: if bound to a caller tx, we
	// run inline so the caller's commit/rollback governs everything.
	if _, ok := r.db.(txCommitter); ok {
		return r.createSessionInline(ctx, session, blocks, sessionQ, blockQ)
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("CreateSession: unsupported DBTX type %T", r.db)
	}
	return RunInTx(ctx, db, func(tx DBTX) error {
		return r.WithTx(tx).createSessionInline(ctx, session, blocks, sessionQ, blockQ)
	})
}

// createSessionInline runs the session + block inserts against the receiver's
// current DBTX (pool or tx). The DB generates the session UUID; we read it back
// so we can stamp it onto each block before inserting.
func (r *UploadSessionRepository) createSessionInline(
	ctx context.Context,
	session *domain.UploadSession,
	blocks []domain.SessionBlock,
	sessionQ, blockQ string,
) error {
	row := r.db.QueryRowContext(ctx, sessionQ,
		session.UserID, session.Filename, session.ParentID,
		session.TotalSize, session.Status)
	if err := row.Scan(&session.ID, &session.CreatedAt, &session.UpdatedAt); err != nil {
		return fmt.Errorf("insert upload_session: %w", err)
	}

	for _, b := range blocks {
		b.SessionID = session.ID
		if _, err := r.db.ExecContext(ctx, blockQ,
			b.SessionID, b.BlockHash, b.SequenceNumber, b.SizeBytes, b.IsUploaded); err != nil {
			return fmt.Errorf("insert session_block seq=%d: %w", b.SequenceNumber, err)
		}
	}
	return nil
}

// GetSessionByID returns the session and its blocks ordered by sequence.
func (r *UploadSessionRepository) GetSessionByID(ctx context.Context, id string) (*domain.UploadSession, []domain.SessionBlock, error) {
	const sessionQ = `
		SELECT id, user_id, filename, parent_id, total_size, status, created_at, updated_at
		FROM upload_sessions
		WHERE id = $1
	`
	var s domain.UploadSession
	err := r.db.QueryRowContext(ctx, sessionQ, id).Scan(
		&s.ID, &s.UserID, &s.Filename, &s.ParentID, &s.TotalSize, &s.Status,
		&s.CreatedAt, &s.UpdatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil, fmt.Errorf("upload session %q: %w", id, sql.ErrNoRows)
	case err != nil:
		return nil, nil, fmt.Errorf("query upload session: %w", err)
	}

	const blocksQ = `
		SELECT session_id, block_hash, sequence_number, size_bytes, is_uploaded
		FROM session_blocks
		WHERE session_id = $1
		ORDER BY sequence_number ASC
	`
	rows, err := r.db.QueryContext(ctx, blocksQ, id)
	if err != nil {
		return nil, nil, fmt.Errorf("query session_blocks: %w", err)
	}
	defer rows.Close()

	var blocks []domain.SessionBlock
	for rows.Next() {
		var b domain.SessionBlock
		if err := rows.Scan(&b.SessionID, &b.BlockHash, &b.SequenceNumber, &b.SizeBytes, &b.IsUploaded); err != nil {
			return nil, nil, fmt.Errorf("scan session_block: %w", err)
		}
		blocks = append(blocks, b)
	}
	return &s, blocks, rows.Err()
}

// UpdateSessionStatus transitions a session (e.g. INITIATED -> COMPLETED).
func (r *UploadSessionRepository) UpdateSessionStatus(ctx context.Context, id string, status string) error {
	const q = `UPDATE upload_sessions SET status = $1 WHERE id = $2`
	res, err := r.db.ExecContext(ctx, q, status, id)
	if err != nil {
		return fmt.Errorf("update session status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("update session status: session %q: %w", id, sql.ErrNoRows)
	}
	return nil
}

// MarkBlockUploaded flips one chunk's is_uploaded flag after the client PUT.
func (r *UploadSessionRepository) MarkBlockUploaded(ctx context.Context, sessionID string, seqNum int) error {
	const q = `
		UPDATE session_blocks
		SET is_uploaded = TRUE
		WHERE session_id = $1 AND sequence_number = $2
	`
	res, err := r.db.ExecContext(ctx, q, sessionID, seqNum)
	if err != nil {
		return fmt.Errorf("mark block uploaded: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("mark block uploaded: session %q seq %d: %w", sessionID, seqNum, sql.ErrNoRows)
	}
	return nil
}
