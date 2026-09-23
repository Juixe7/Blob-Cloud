package postgresrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"go-drive-clone/internal/audit"
)

// AuditRepository is the Postgres implementation of audit.Logger.
//
// Log() is fire-and-forget — it launches a goroutine to INSERT the row so the
// calling handler is never delayed by a slow audit write. Any DB error is
// logged at Warn but never surfaced to the caller.
//
// ListFileHistory() is synchronous (it serves a user-facing GET endpoint that
// explicitly asks for the history, so a round-trip is expected).
type AuditRepository struct {
	db  *sql.DB
	log *slog.Logger
}

// NewAuditRepository constructs an AuditRepository bound to the given pool.
func NewAuditRepository(db *sql.DB, log *slog.Logger) *AuditRepository {
	return &AuditRepository{db: db, log: log}
}

// Log persists entry to the audit_logs table in a background goroutine.
// It implements audit.Logger and never blocks the caller.
func (r *AuditRepository) Log(ctx context.Context, entry audit.Entry) {
	go func() {
		// Use a fresh background context so the insert isn't cancelled when
		// the HTTP request context is done (race between response sent and
		// goroutine starting).
		bctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		meta := entry.Metadata
		if meta == nil {
			meta = json.RawMessage("{}")
		}

		const q = `
			INSERT INTO audit_logs
			    (user_id, action, resource_type, resource_id, metadata, client_ip)
			VALUES ($1, $2, $3, $4, $5, $6)
		`
		if _, err := r.db.ExecContext(bctx, q,
			entry.UserID,
			string(entry.Action),
			string(entry.ResourceType),
			entry.ResourceID,
			meta,
			entry.ClientIP,
		); err != nil {
			r.log.Warn("audit: failed to persist log entry",
				"action", entry.Action,
				"resource_id", entry.ResourceID,
				"err", err,
			)
		}
	}()
}

// ListFileHistory returns up to limit audit entries for the given fileID,
// ordered newest-first. Limit is capped at 100 to prevent runaway queries.
func (r *AuditRepository) ListFileHistory(ctx context.Context, fileID string, limit int) ([]audit.Entry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	const q = `
		SELECT id, user_id, action, resource_type, resource_id, metadata, client_ip, created_at
		FROM audit_logs
		WHERE resource_type = 'file' AND resource_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := r.db.QueryContext(ctx, q, fileID, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit history: %w", err)
	}
	defer rows.Close()

	var out []audit.Entry
	for rows.Next() {
		var e audit.Entry
		var action, resourceType string
		if err := rows.Scan(
			&e.ID, &e.UserID, &action, &resourceType,
			&e.ResourceID, &e.Metadata, &e.ClientIP, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}
		e.Action = audit.Action(action)
		e.ResourceType = audit.ResourceType(resourceType)
		out = append(out, e)
	}
	return out, rows.Err()
}
