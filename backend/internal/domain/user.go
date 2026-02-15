package domain

import (
	"context"
	"time"
)

// User represents an account record in the `users` table.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"` // never serialised to clients
	IsVerified   bool      `json:"is_verified"`
	CreatedAt    time.Time `json:"created_at"`
}

// VerificationCode represents a 6-digit email verification code.
type VerificationCode struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// UserSession represents a logged-in device/browser session.
type UserSession struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	RefreshTokenID string    `json:"refresh_token_id"`
	IPAddress      string    `json:"ip_address"`
	UserAgent      string    `json:"user_agent"`
	DeviceName     string    `json:"device_name"`
	IsActive       bool      `json:"is_active"`
	LastActiveAt   time.Time `json:"last_active_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	IsCurrent      bool      `json:"is_current"`
}

// PasswordReset represents a password reset token record in `password_resets`.
type PasswordReset struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
	CreatedAt time.Time `json:"created_at"`
}

// UserRepository abstracts persistence for User aggregates. The Postgres
// implementation lives in internal/repository/postgres.
type UserRepository interface {
	// Create inserts a new user.
	Create(ctx context.Context, user *User) error
	// GetByEmail returns the user matching email, or sql.ErrNoRows if not found.
	GetByEmail(ctx context.Context, email string) (*User, error)
	// GetByID returns the user matching ID.
	GetByID(ctx context.Context, id string) (*User, error)
	// SetVerified marks a user's is_verified status to true.
	SetVerified(ctx context.Context, userID string) error
	// CreateVerificationCode inserts a 6-digit verification code.
	CreateVerificationCode(ctx context.Context, code *VerificationCode) error
	// GetVerificationCode checks if code matches user_id and is valid.
	GetVerificationCode(ctx context.Context, userID, code string) (*VerificationCode, error)
	// DeleteVerificationCode deletes a used verification code.
	DeleteVerificationCode(ctx context.Context, id string) error
	// CreatePasswordResetToken inserts a new password reset token.
	CreatePasswordResetToken(ctx context.Context, userID, token string, expiresAt time.Time) error
	// ValidateResetToken verifies token is not expired and not used.
	ValidateResetToken(ctx context.Context, token string) (*PasswordReset, error)
	// MarkResetTokenUsed marks a reset token as used.
	MarkResetTokenUsed(ctx context.Context, token string) error
	// UpdatePassword updates user's password hash and revokes all active sessions.
	UpdatePassword(ctx context.Context, userID, hashedPassword string) error
	// DeleteRefreshToken revokes/deletes a specific refresh token session.
	DeleteRefreshToken(ctx context.Context, refreshToken string) error
}


