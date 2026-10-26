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

func (r *ShareableLinkRepository) WithTx(tx DBTX) *ShareableLinkRepository {
	return &ShareableLinkRepository{db: tx}
}

// Create inserts a new shareable link.
func (r *ShareableLinkRepository) Create(ctx context.Context, link *domain.ShareableLink) error {
	const q = `
		INSERT INTO shareable_links (file_id, link_token_hash, access_tier, password_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`
	row := r.db.QueryRowContext(ctx, q,
		link.FileID, hashBearerToken(link.LinkToken), link.AccessTier, link.PasswordHash, link.ExpiresAt)
	if err := row.Scan(&link.ID, &link.CreatedAt); err != nil {
		return fmt.Errorf("insert shareable link: %w", err)
	}
	return nil
}

// GetByToken returns a shareable link by its unique token.
func (r *ShareableLinkRepository) GetByToken(ctx context.Context, token string) (*domain.ShareableLink, error) {
	return r.getByToken(ctx, token, false)
}

// GetByTokenForUpdate locks the link until the surrounding transaction ends,
// serializing shortcut creation with link revocation.
func (r *ShareableLinkRepository) GetByTokenForUpdate(ctx context.Context, token string) (*domain.ShareableLink, error) {
	return r.getByToken(ctx, token, true)
}

func (r *ShareableLinkRepository) getByToken(ctx context.Context, token string, forUpdate bool) (*domain.ShareableLink, error) {
	lockClause := ""
	if forUpdate {
		lockClause = " FOR UPDATE"
	}
	const q = `
		SELECT id, file_id, access_tier, password_hash, expires_at, created_at
		FROM shareable_links
		WHERE link_token_hash = $1
	`
	var link domain.ShareableLink
	err := r.db.QueryRowContext(ctx, q+lockClause, hashBearerToken(token)).Scan(
		&link.ID, &link.FileID, &link.AccessTier,
		&link.PasswordHash, &link.ExpiresAt, &link.CreatedAt,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, sql.ErrNoRows
	case err != nil:
		return nil, fmt.Errorf("query shareable link by token: %w", err)
	}
	link.LinkToken = token
	return &link, nil
}

// DeleteByFileAndToken revokes one link without allowing a token from another
// file to be used to mutate the link set.
func (r *ShareableLinkRepository) DeleteByFileAndToken(ctx context.Context, fileID, token string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM shareable_links WHERE file_id=$1 AND link_token_hash=$2`, fileID, hashBearerToken(token))
	if err != nil {
		return fmt.Errorf("revoke shareable link: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke shareable link rows affected: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}
