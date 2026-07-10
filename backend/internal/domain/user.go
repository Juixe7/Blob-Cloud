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
	CreatedAt    time.Time `json:"created_at"`
}

// UserRepository abstracts persistence for User aggregates. The Postgres
// implementation lives in internal/repository/postgres.
type UserRepository interface {
	// Create inserts a new user. The caller is responsible for hashing the
	// password; this layer stores it verbatim.
	Create(ctx context.Context, user *User) error
	// GetByEmail returns the user matching email, or an error wrapping
	// sql.ErrNoRows when not found.
	GetByEmail(ctx context.Context, email string) (*User, error)
}
