package postgresrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go-drive-clone/internal/domain"
)

// FileRepository is the Postgres implementation of domain.FileRepository.
type FileRepository struct {
	db DBTX
}

// NewFileRepository constructs a FileRepository bound to the given pool.
func NewFileRepository(db *sql.DB) *FileRepository {
	return &FileRepository{db: db}
}

// WithTx returns a copy of the repository bound to tx.
func (r *FileRepository) WithTx(tx DBTX) *FileRepository {
	return &FileRepository{db: tx}
}

// Create inserts file and reads back the DB-generated id/timestamps.
func (r *FileRepository) Create(ctx context.Context, file *domain.File) error {
	const q = `
		INSERT INTO files (user_id, name, parent_id, is_directory, size_bytes)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at
	`
	row := r.db.QueryRowContext(ctx, q,
		file.UserID, file.Name, file.ParentID, file.IsDirectory, file.SizeBytes)
	if err := row.Scan(&file.ID, &file.CreatedAt, &file.UpdatedAt); err != nil {
		return fmt.Errorf("insert file: %w", err)
	}
	return nil
}

// GetByID returns the file with the given id.
func (r *FileRepository) GetByID(ctx context.Context, id string) (*domain.File, error) {
	const q = `
		SELECT id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at
		FROM files
		WHERE id = $1
	`
	var f domain.File
	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
		&f.CreatedAt, &f.UpdatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("file by id %q: %w", id, sql.ErrNoRows)
	case err != nil:
		return nil, fmt.Errorf("query file by id: %w", err)
	}
	return &f, nil
}

// ListDirectory returns the immediate children of parentID for a user. A nil
// parentID lists the user's top-level entries (parent_id IS NULL).
func (r *FileRepository) ListDirectory(ctx context.Context, userID string, parentID *string) ([]*domain.File, error) {
	// Two query shapes depending on whether parentID is set; keeping them
	// separate avoids nullable-binding subtleties with pgx.
	var (
		rows *sql.Rows
		err  error
	)
	if parentID == nil {
		const q = `
			SELECT id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at
			FROM files
			WHERE user_id = $1 AND parent_id IS NULL
			ORDER BY is_directory DESC, name ASC
		`
		rows, err = r.db.QueryContext(ctx, q, userID)
	} else {
		const q = `
			SELECT id, user_id, name, parent_id, is_directory, size_bytes, created_at, updated_at
			FROM files
			WHERE user_id = $1 AND parent_id = $2
			ORDER BY is_directory DESC, name ASC
		`
		rows, err = r.db.QueryContext(ctx, q, userID, *parentID)
	}
	if err != nil {
		return nil, fmt.Errorf("query directory: %w", err)
	}
	defer rows.Close()

	var out []*domain.File
	for rows.Next() {
		var f domain.File
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.Name, &f.ParentID, &f.IsDirectory, &f.SizeBytes,
			&f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan file row: %w", err)
		}
		out = append(out, &f)
	}
	return out, rows.Err()
}
