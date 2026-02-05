package postgresrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go-drive-clone/internal/domain"
)

// ShareableLinkRepository is the Postgres implementation of domain.ShareableLinkRepository (if it existed, but we'll just implement the methods).
type ShareableLinkRepository struct {
	db DBTX
}

// NewShareableLinkRepository creates a new ShareableLinkRepository.
func NewShareableLinkRepository(db DBTX) *ShareableLinkRepository {
	return &ShareableLinkRepository{db: db}
}

// Create inserts a new shareable link.
func (r *ShareableLinkRepository) Create(ctx context.Context, link *domain.ShareableLink) error {
	const q = `
		INSERT INTO shareable_links (file_id, link_token, access_tier, password_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`
	row := r.db.QueryRowContext(ctx, q,
		link.FileID, link.LinkToken, link.AccessTier, link.PasswordHash, link.ExpiresAt)
	if err := row.Scan(&link.ID, &link.CreatedAt); err != nil {
		return fmt.Errorf("insert shareable link: %w", err)
	}
	return nil
}

// GetByToken returns a shareable link by its unique token.
func (r *ShareableLinkRepository) GetByToken(ctx context.Context, token string) (*domain.ShareableLink, error) {
	const q = `
		SELECT id, file_id, link_token, access_tier, password_hash, expires_at, created_at
		FROM shareable_links
		WHERE link_token = $1
	`
	var link domain.ShareableLink
	err := r.db.QueryRowContext(ctx, q, token).Scan(
		&link.ID, &link.FileID, &link.LinkToken, &link.AccessTier,
		&link.PasswordHash, &link.ExpiresAt, &link.CreatedAt,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, sql.ErrNoRows
	case err != nil:
		return nil, fmt.Errorf("query shareable link by token: %w", err)
	}
	return &link, nil
}
