package postgresrepo

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-drive-clone/internal/domain"
)

// JournalRepository is the PostgreSQL implementation of domain.JournalRepository.
type JournalRepository struct {
	db DBTX
}

// journalCursorLockSeed derives stable transaction lock keys per recipient.
// BIGSERIAL values are allocated before commit, so each recipient's writers
// must hold a lock through commit to keep their visible cursor order gap-safe.
// Per-user locks avoid serializing unrelated users; batch writers acquire them
// in sorted order to avoid deadlocks.
const journalCursorLockSeed int64 = 781245930124

// NewJournalRepository constructs a JournalRepository bound to the given pool.
func NewJournalRepository(db *sql.DB) *JournalRepository {
	return &JournalRepository{db: db}
}

// WithTx returns a copy of the repository bound to tx.
func (r *JournalRepository) WithTx(tx DBTX) *JournalRepository {
	return &JournalRepository{db: tx}
}

func (r *JournalRepository) lockJournalUsers(ctx context.Context, userIDs ...string) error {
	unique := make(map[string]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id != "" {
			unique[id] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		if _, err := r.db.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, $2))`, id, journalCursorLockSeed); err != nil {
			return fmt.Errorf("lock journal cursor order for user: %w", err)
		}
	}
	return nil
}

// Record inserts a new journal entry and returns the monotonic cursor generated.
func (r *JournalRepository) Record(ctx context.Context, entry *domain.JournalEntry) (int64, error) {
	if db, ok := r.db.(*sql.DB); ok {
		var cursor int64
		err := RunInTx(ctx, db, func(tx DBTX) error {
			var err error
			cursor, err = r.WithTx(tx).Record(ctx, entry)
			return err
		})
		return cursor, err
	}
	if err := r.lockJournalUsers(ctx, entry.UserID); err != nil {
		return 0, err
	}
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

// BatchRecord inserts multiple journal entries in a single multi-row query.
func (r *JournalRepository) BatchRecord(ctx context.Context, entries []*domain.JournalEntry) ([]int64, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	if db, ok := r.db.(*sql.DB); ok {
		var cursors []int64
		err := RunInTx(ctx, db, func(tx DBTX) error {
			var err error
			cursors, err = r.WithTx(tx).BatchRecord(ctx, entries)
			return err
		})
		return cursors, err
	}
	if len(entries) == 1 {
		c, err := r.Record(ctx, entries[0])
		if err != nil {
			return nil, err
		}
		return []int64{c}, nil
	}
	userIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		userIDs = append(userIDs, entry.UserID)
	}
	if err := r.lockJournalUsers(ctx, userIDs...); err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString(`INSERT INTO journal_entries (
		user_id, file_id, action, parent_id, name,
		is_directory, size_bytes, mime_type, status, thumbnail_url
	) VALUES `)

	args := make([]any, 0, len(entries)*10)
	for i, e := range entries {
		if i > 0 {
			b.WriteString(", ")
		}
		offset := i * 10
		b.WriteString(fmt.Sprintf(
			"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			offset+1, offset+2, offset+3, offset+4, offset+5,
			offset+6, offset+7, offset+8, offset+9, offset+10,
		))
		args = append(args,
			e.UserID,
			e.FileID,
			e.Action,
			e.ParentID,
			e.Name,
			e.IsDirectory,
			e.SizeBytes,
			e.MimeType,
			e.Status,
			e.ThumbnailURL,
		)
	}
	b.WriteString(" RETURNING cursor, created_at, user_id, file_id, action")

	rows, err := r.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("batch record journal entries: %w", err)
	}
	defer rows.Close()

	type returnedEntry struct {
		cursor    int64
		createdAt time.Time
		userID    string
		fileID    string
		action    string
	}
	returned := make([]returnedEntry, 0, len(entries))
	for rows.Next() {
		var result returnedEntry
		if err := rows.Scan(&result.cursor, &result.createdAt, &result.userID, &result.fileID, &result.action); err != nil {
			return nil, fmt.Errorf("scan batch journal entry: %w", err)
		}
		returned = append(returned, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(returned, func(i, j int) bool { return returned[i].cursor < returned[j].cursor })
	type entryKey struct{ userID, fileID, action string }
	byKey := make(map[entryKey][]*domain.JournalEntry, len(entries))
	indexes := make(map[*domain.JournalEntry]int, len(entries))
	for i, entry := range entries {
		key := entryKey{entry.UserID, entry.FileID, entry.Action}
		byKey[key] = append(byKey[key], entry)
		indexes[entry] = i
	}
	cursors := make([]int64, len(entries))
	for _, result := range returned {
		key := entryKey{result.userID, result.fileID, result.action}
		matching := byKey[key]
		if len(matching) == 0 {
			return nil, fmt.Errorf("batch journal returned an unexpected row")
		}
		entry := matching[0]
		byKey[key] = matching[1:]
		entry.Cursor = result.cursor
		entry.CreatedAt = result.createdAt
		cursors[indexes[entry]] = result.cursor
	}
	return cursors, nil
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
