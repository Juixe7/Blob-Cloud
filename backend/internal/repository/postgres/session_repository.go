package postgresrepo

import (
	"context"
	"database/sql"
	"fmt"

	"go-drive-clone/internal/domain"
)

// SessionRepository manages database operations for `user_sessions`.
type SessionRepository struct {
	db DBTX
}

// NewSessionRepository constructs a SessionRepository targeting db.
func NewSessionRepository(db DBTX) *SessionRepository {
	return &SessionRepository{db: db}
}

// WithTx returns a new SessionRepository bound to the given transaction.
func (r *SessionRepository) WithTx(tx DBTX) *SessionRepository {
	return &SessionRepository{db: tx}
}

// CreateSession inserts a new user session row.
func (r *SessionRepository) CreateSession(ctx context.Context, session *domain.UserSession) error {
	query := `
		INSERT INTO user_sessions (user_id, refresh_token_id, ip_address, user_agent, device_name, is_active, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, last_active_at;
	`

	err := r.db.QueryRowContext(ctx, query,
		session.UserID,
		session.RefreshTokenID,
		session.IPAddress,
		session.UserAgent,
		session.DeviceName,
		session.IsActive,
		session.ExpiresAt,
	).Scan(&session.ID, &session.CreatedAt, &session.LastActiveAt)

	if err != nil {
		return fmt.Errorf("create user session: %w", err)
	}
	return nil
}

// GetSessionsByUserID returns all active sessions for a given user.
func (r *SessionRepository) GetSessionsByUserID(ctx context.Context, userID string) ([]*domain.UserSession, error) {
	query := `
		SELECT id, user_id, refresh_token_id, ip_address, user_agent, device_name, is_active, last_active_at, expires_at, created_at
		FROM user_sessions
		WHERE user_id = $1 AND expires_at > CURRENT_TIMESTAMP
		ORDER BY last_active_at DESC;
	`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list user sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*domain.UserSession
	for rows.Next() {
		s := &domain.UserSession{}
		if err := rows.Scan(&s.ID, &s.UserID, &s.RefreshTokenID, &s.IPAddress, &s.UserAgent, &s.DeviceName, &s.IsActive, &s.LastActiveAt, &s.ExpiresAt, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user session: %w", err)
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error user sessions: %w", err)
	}
	return sessions, nil
}

// GetSessionByID retrieves a session by ID.
func (r *SessionRepository) GetSessionByID(ctx context.Context, id string) (*domain.UserSession, error) {
	query := `
		SELECT id, user_id, refresh_token_id, ip_address, user_agent, device_name, is_active, last_active_at, expires_at, created_at
		FROM user_sessions
		WHERE id = $1;
	`
	s := &domain.UserSession{}
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&s.ID, &s.UserID, &s.RefreshTokenID, &s.IPAddress, &s.UserAgent, &s.DeviceName, &s.IsActive, &s.LastActiveAt, &s.ExpiresAt, &s.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found: %w", err)
		}
		return nil, fmt.Errorf("get session by id: %w", err)
	}
	return s, nil
}

// TouchSession updates the last_active_at timestamp for a session.
func (r *SessionRepository) TouchSession(ctx context.Context, id string) error {
	query := `
		UPDATE user_sessions
		SET last_active_at = CURRENT_TIMESTAMP
		WHERE id = $1;
	`
	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("touch user session: %w", err)
	}
	return nil
}

// DeleteSession deletes a specific session.
func (r *SessionRepository) DeleteSession(ctx context.Context, id string, userID string) error {
	query := `
		DELETE FROM user_sessions
		WHERE id = $1 AND user_id = $2;
	`
	res, err := r.db.ExecContext(ctx, query, id, userID)
	if err != nil {
		return fmt.Errorf("delete user session: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteOtherSessions deletes all sessions for a user EXCEPT the current session.
func (r *SessionRepository) DeleteOtherSessions(ctx context.Context, currentSessionID string, userID string) (int64, error) {
	query := `
		DELETE FROM user_sessions
		WHERE user_id = $1 AND id != $2;
	`
	res, err := r.db.ExecContext(ctx, query, userID, currentSessionID)
	if err != nil {
		return 0, fmt.Errorf("delete other user sessions: %w", err)
	}
	return res.RowsAffected()
}

// GetSessionByRefreshTokenID retrieves a session by its associated refresh token ID.
func (r *SessionRepository) GetSessionByRefreshTokenID(ctx context.Context, refreshTokenID string) (*domain.UserSession, error) {
	query := `
		SELECT id, user_id, refresh_token_id, ip_address, user_agent, device_name, is_active, last_active_at, expires_at, created_at
		FROM user_sessions
		WHERE refresh_token_id = $1;
	`
	s := &domain.UserSession{}
	err := r.db.QueryRowContext(ctx, query, refreshTokenID).Scan(
		&s.ID, &s.UserID, &s.RefreshTokenID, &s.IPAddress, &s.UserAgent, &s.DeviceName, &s.IsActive, &s.LastActiveAt, &s.ExpiresAt, &s.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found for refresh token: %w", err)
		}
		return nil, fmt.Errorf("get session by refresh token: %w", err)
	}
	return s, nil
}
