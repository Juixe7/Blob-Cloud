package postgresrepo

import (
	"context"
	"database/sql"
	"fmt"

	"go-drive-clone/internal/domain"
)

// JournalRepository is the PostgreSQL implementation of domain.JournalRepository.
type JournalRepository struct {
	db DBTX
}

// NewJournalRepository constructs a JournalRepository bound to the given pool.
func NewJournalRepository(db *sql.DB) *JournalRepository {
	return &JournalRepository{db: db}
}

// WithTx returns a copy of the repository bound to tx.
func (r *JournalRepository) WithTx(tx DBTX) *JournalRepository {
	return &JournalRepository{db: tx}
}

// Record inserts a new journal entry and returns the monotonic cursor generated.
func (r *JournalRepository) Record(ctx context.Context, entry *domain.JournalEntry) (int64, error) {
	const q = `
		INSERT INTO journal_entries (
			user_id, file_id, action, parent_id, name,
			is_directory, size_bytes, mime_type, status, thumbnail_url
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING cursor, created_at
	`
	row := r.db.QueryRowContext(ctx, q,
		entry.UserID,
		entry.FileID,
		entry.Action,
		entry.ParentID,
		entry.Name,
		entry.IsDirectory,
		entry.SizeBytes,
		entry.MimeType,
		entry.Status,
		entry.ThumbnailURL,
	)

	var cursor int64
	if err := row.Scan(&cursor, &entry.CreatedAt); err != nil {
		return 0, fmt.Errorf("record journal entry: %w", err)
	}
	entry.Cursor = cursor
	return cursor, nil
}

// ListSince retrieves up to `limit` entries with cursor > sinceCursor for a user.
// It returns the slice of entries, the highest cursor in the batch (or sinceCursor if empty),
// and a boolean indicating if there are more entries remaining.
func (r *JournalRepository) ListSince(ctx context.Context, userID string, sinceCursor int64, limit int) ([]*domain.JournalEntry, int64, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// Fetch limit + 1 to detect has_more
	const q = `
		SELECT cursor, user_id, file_id, action, parent_id, name,
		       is_directory, size_bytes, mime_type, status, thumbnail_url, created_at
		FROM journal_entries
		WHERE user_id = $1 AND cursor > $2
		ORDER BY cursor ASC
		LIMIT $3
	`
	rows, err := r.db.QueryContext(ctx, q, userID, sinceCursor, limit+1)
	if err != nil {
		return nil, sinceCursor, false, fmt.Errorf("list journal entries: %w", err)
	}
	defer rows.Close()

	var entries []*domain.JournalEntry
	for rows.Next() {
		var e domain.JournalEntry
		if err := rows.Scan(
			&e.Cursor,
			&e.UserID,
			&e.FileID,
			&e.Action,
			&e.ParentID,
			&e.Name,
			&e.IsDirectory,
			&e.SizeBytes,
			&e.MimeType,
			&e.Status,
			&e.ThumbnailURL,
			&e.CreatedAt,
		); err != nil {
			return nil, sinceCursor, false, fmt.Errorf("scan journal entry: %w", err)
		}
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, sinceCursor, false, err
	}

	hasMore := false
	if len(entries) > limit {
		hasMore = true
		entries = entries[:limit]
	}

	highestCursor := sinceCursor
	if len(entries) > 0 {
		highestCursor = entries[len(entries)-1].Cursor
	}

	return entries, highestCursor, hasMore, nil
}

// GetLatestCursor returns the highest cursor for the user, or 0 if none exist.
func (r *JournalRepository) GetLatestCursor(ctx context.Context, userID string) (int64, error) {
	const q = `
		SELECT COALESCE(MAX(cursor), 0)
		FROM journal_entries
		WHERE user_id = $1
	`
	var cursor int64
	if err := r.db.QueryRowContext(ctx, q, userID).Scan(&cursor); err != nil {
		return 0, fmt.Errorf("get latest cursor: %w", err)
	}
	return cursor, nil
}
