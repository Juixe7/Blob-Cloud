package postgresrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go-drive-clone/internal/domain"
)

// UserRepository is the Postgres implementation of domain.UserRepository.
type UserRepository struct {
	db DBTX
}

// NewUserRepository constructs a UserRepository bound to the given pool.
func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// WithTx returns a copy of the repository bound to tx.
func (r *UserRepository) WithTx(tx DBTX) *UserRepository {
	return &UserRepository{db: tx}
}

// Create inserts user. On success the DB-generated id/created_at are read back
// onto the struct, so callers see the persisted values without a second query.
func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	const q = `
		INSERT INTO users (email, password_hash)
		VALUES ($1, $2)
		RETURNING id, created_at
	`
	row := r.db.QueryRowContext(ctx, q, user.Email, user.PasswordHash)
	if err := row.Scan(&user.ID, &user.CreatedAt); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

// GetByEmail returns the user matching email. Missing rows surface as
// sql.ErrNoRows (wrapped), so callers can errors.Is against it.
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	const q = `
		SELECT id, email, password_hash, created_at
		FROM users
		WHERE email = $1
	`
	var u domain.User
	err := r.db.QueryRowContext(ctx, q, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("user by email %q: %w", email, sql.ErrNoRows)
	case err != nil:
		return nil, fmt.Errorf("query user by email: %w", err)
	}
	return &u, nil
}

// GetByID returns the user with the given id. Needed by the upload service to
// resolve the uploader's email when writing the default OWNER permission.
func (r *UserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	const q = `
		SELECT id, email, password_hash, created_at
		FROM users
		WHERE id = $1
	`
	var u domain.User
	err := r.db.QueryRowContext(ctx, q, id).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("user by id %q: %w", id, sql.ErrNoRows)
	case err != nil:
		return nil, fmt.Errorf("query user by id: %w", err)
	}
	return &u, nil
}
