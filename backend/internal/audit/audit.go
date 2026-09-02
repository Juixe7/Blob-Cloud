// Package audit provides structured, immutable audit logging for Blob-Cloud.
//
// Every significant user action is recorded as an Entry in the audit_logs
// Postgres table. The Logger interface keeps callers decoupled from the
// persistence layer — handlers depend only on audit.Logger, never on the
// concrete Postgres implementation.
//
// Actions are predefined string constants so the audit trail is machine-
// readable and can be indexed without scanning free-text.
//
// Design goals:
//   - Append-only: no Update or Delete is ever issued.
//   - Non-blocking: audit writes happen asynchronously via a fire-and-forget
//     goroutine so a slow DB write never delays the HTTP response.
//   - Fail-open: if the audit insert fails it is logged at Warn but the
//     primary operation is not rolled back.
package audit

import (
	"context"
	"encoding/json"
	"time"
)

// Action is the string label stored in audit_logs.action.
// Keep values UPPER_SNAKE_CASE to match the WS event naming convention.
type Action string

const (
	ActionFileUploaded Action = "FILE_UPLOADED"
	ActionFileShared   Action = "FILE_SHARED"
	ActionFileDeleted  Action = "FILE_DELETED"
	ActionFileRestored Action = "FILE_RESTORED"
	ActionFileRenamed  Action = "FILE_RENAMED"
)

// ResourceType discriminates what resource_id points at.
type ResourceType string

const (
	ResourceFile    ResourceType = "file"
	ResourceSession ResourceType = "session"
)

// Entry is one audit log record. It maps 1:1 to a row in audit_logs.
type Entry struct {
	ID           string          `json:"id"`
	UserID       string          `json:"user_id"`
	Action       Action          `json:"action"`
	ResourceType ResourceType    `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Metadata     json.RawMessage `json:"metadata"`
	ClientIP     string          `json:"client_ip"`
	CreatedAt    time.Time       `json:"created_at"`
}

// Logger is the narrow interface callers depend on. The concrete Postgres
// implementation lives in internal/repository/postgres/audit.go.
type Logger interface {
	// Log records one audit entry. It returns immediately; any persistence
	// error is logged internally and never propagated to the caller.
	Log(ctx context.Context, entry Entry)

	// ListFileHistory returns audit entries for a file in reverse chronological
	// order, limited to limit rows (max 100).
	ListFileHistory(ctx context.Context, fileID string, limit int) ([]Entry, error)
}

// NoopLogger discards all audit entries. Used in unit tests and when the DB is
// not configured.
type NoopLogger struct{}

func (NoopLogger) Log(_ context.Context, _ Entry) {}

func (NoopLogger) ListFileHistory(_ context.Context, _ string, _ int) ([]Entry, error) {
	return nil, nil
}

// MarshalMeta serialises any value to JSON for use as audit Entry.Metadata.
// Returns an empty JSON object on marshal failure (never panics).
func MarshalMeta(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}
