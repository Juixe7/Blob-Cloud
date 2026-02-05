package postgresrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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

// Create inserts user. On success the DB-generated id/created_at are read back.
func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	const q = `
		INSERT INTO users (email, password_hash, is_verified)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`
	row := r.db.QueryRowContext(ctx, q, user.Email, user.PasswordHash, user.IsVerified)
	if err := row.Scan(&user.ID, &user.CreatedAt); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

// GetByEmail returns the user matching email. Missing rows surface as sql.ErrNoRows.
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	const q = `
		SELECT id, email, password_hash, is_verified, created_at
		FROM users
		WHERE email = $1
	`
	var u domain.User
	err := r.db.QueryRowContext(ctx, q, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsVerified, &u.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("user by email %q: %w", email, sql.ErrNoRows)
	case err != nil:
		return nil, fmt.Errorf("query user by email: %w", err)
	}
	return &u, nil
}

// GetByID returns the user with the given id.
func (r *UserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	const q = `
		SELECT id, email, password_hash, is_verified, created_at
		FROM users
		WHERE id = $1
	`
	var u domain.User
	err := r.db.QueryRowContext(ctx, q, id).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsVerified, &u.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("user by id %q: %w", id, sql.ErrNoRows)
	case err != nil:
		return nil, fmt.Errorf("query user by id: %w", err)
	}
	return &u, nil
}

// SetVerified marks a user's is_verified status as true in PostgreSQL.
func (r *UserRepository) SetVerified(ctx context.Context, userID string) error {
	const q = `UPDATE users SET is_verified = TRUE WHERE id = $1`
	_, err := r.db.ExecContext(ctx, q, userID)
	if err != nil {
		return fmt.Errorf("set verified: %w", err)
	}
	return nil
}

// CreateVerificationCode inserts a 6-digit verification code into PostgreSQL.
func (r *UserRepository) CreateVerificationCode(ctx context.Context, code *domain.VerificationCode) error {
	const q = `
		INSERT INTO verification_codes (user_id, code, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`
	row := r.db.QueryRowContext(ctx, q, code.UserID, code.Code, code.ExpiresAt)
	if err := row.Scan(&code.ID, &code.CreatedAt); err != nil {
		return fmt.Errorf("create verification code: %w", err)
	}
	return nil
}

// GetVerificationCode finds a valid, non-expired verification code for user_id.
func (r *UserRepository) GetVerificationCode(ctx context.Context, userID, code string) (*domain.VerificationCode, error) {
	const q = `
		SELECT id, user_id, code, expires_at, created_at
		FROM verification_codes
		WHERE user_id = $1 AND code = $2 AND expires_at > CURRENT_TIMESTAMP
		ORDER BY created_at DESC
		LIMIT 1
	`
	var vc domain.VerificationCode
	err := r.db.QueryRowContext(ctx, q, userID, code).Scan(&vc.ID, &vc.UserID, &vc.Code, &vc.ExpiresAt, &vc.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("verification code invalid or expired: %w", sql.ErrNoRows)
	case err != nil:
		return nil, fmt.Errorf("query verification code: %w", err)
	}
	return &vc, nil
}

// DeleteVerificationCode deletes a verification code after it has been used.
func (r *UserRepository) DeleteVerificationCode(ctx context.Context, id string) error {
	const q = `DELETE FROM verification_codes WHERE id = $1`
	_, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete verification code: %w", err)
	}
	return nil
}

// CreatePasswordResetToken inserts a new password reset token.
func (r *UserRepository) CreatePasswordResetToken(ctx context.Context, userID, token string, expiresAt time.Time) error {
	const q = `
		INSERT INTO password_resets (user_id, token, expires_at)
		VALUES ($1, $2, $3)
	`
	_, err := r.db.ExecContext(ctx, q, userID, token, expiresAt)
	if err != nil {
		return fmt.Errorf("create password reset token: %w", err)
	}
	return nil
}

// ValidateResetToken checks if token exists, is not expired, and has not been used.
func (r *UserRepository) ValidateResetToken(ctx context.Context, token string) (*domain.PasswordReset, error) {
	const q = `
		SELECT id, user_id, token, expires_at, used, created_at
		FROM password_resets
		WHERE token = $1 AND expires_at > CURRENT_TIMESTAMP AND used = FALSE
	`
	var pr domain.PasswordReset
	err := r.db.QueryRowContext(ctx, q, token).Scan(&pr.ID, &pr.UserID, &pr.Token, &pr.ExpiresAt, &pr.Used, &pr.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("password reset token invalid or expired: %w", sql.ErrNoRows)
	case err != nil:
		return nil, fmt.Errorf("query password reset token: %w", err)
	}
	return &pr, nil
}

// MarkResetTokenUsed sets used = true for the given token.
func (r *UserRepository) MarkResetTokenUsed(ctx context.Context, token string) error {
	const q = `UPDATE password_resets SET used = TRUE WHERE token = $1`
	_, err := r.db.ExecContext(ctx, q, token)
	if err != nil {
		return fmt.Errorf("mark password reset token used: %w", err)
	}
	return nil
}

// UpdatePassword updates user's password hash and revokes all active sessions for global logout.
func (r *UserRepository) UpdatePassword(ctx context.Context, userID, hashedPassword string) error {
	const qUser = `UPDATE users SET password_hash = $1 WHERE id = $2`
	if _, err := r.db.ExecContext(ctx, qUser, hashedPassword, userID); err != nil {
		return fmt.Errorf("update user password: %w", err)
	}
	const qSess = `DELETE FROM user_sessions WHERE user_id = $1`
	if _, err := r.db.ExecContext(ctx, qSess, userID); err != nil {
		return fmt.Errorf("invalidate user sessions on password change: %w", err)
	}
	return nil
}

// DeleteRefreshToken deletes/invalidates the specific refresh token session during logout.
func (r *UserRepository) DeleteRefreshToken(ctx context.Context, refreshToken string) error {
	const q = `DELETE FROM user_sessions WHERE id = $1 OR user_id = $1`
	_, err := r.db.ExecContext(ctx, q, refreshToken)
	if err != nil {
		return fmt.Errorf("delete refresh token session: %w", err)
	}
	return nil
}

