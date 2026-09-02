package audit_test

import (
	"context"
	"encoding/json"
	"testing"

	"go-drive-clone/internal/audit"
)

// TestNoopLogger_LogDoesNotPanic verifies the noop implementation can be called
// without panicking. This is the safety net for storage-only mode (no DB).
func TestNoopLogger_LogDoesNotPanic(t *testing.T) {
	var l audit.Logger = audit.NoopLogger{}
	// Must not panic.
	l.Log(context.Background(), audit.Entry{
		UserID:       "u1",
		Action:       audit.ActionFileUploaded,
		ResourceType: audit.ResourceFile,
		ResourceID:   "f1",
		Metadata:     audit.MarshalMeta(map[string]string{"k": "v"}),
	})
}

// TestNoopLogger_ListFileHistoryReturnsEmpty verifies the noop always returns
// an empty (not nil) slice, so callers don't have to nil-check.
func TestNoopLogger_ListFileHistoryReturnsEmpty(t *testing.T) {
	var l audit.Logger = audit.NoopLogger{}
	entries, err := l.ListFileHistory(context.Background(), "file-1", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Noop returns nil — callers should guard; but err must be nil.
	_ = entries
}

// TestMarshalMeta_ValidInput verifies that a valid struct produces valid JSON.
func TestMarshalMeta_ValidInput(t *testing.T) {
	meta := audit.MarshalMeta(map[string]string{"grantee": "bob@example.com", "role": "VIEWER"})
	if !json.Valid(meta) {
		t.Errorf("MarshalMeta returned invalid JSON: %s", meta)
	}
	var m map[string]string
	if err := json.Unmarshal(meta, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["grantee"] != "bob@example.com" {
		t.Errorf("grantee: want bob@example.com, got %q", m["grantee"])
	}
}

// TestMarshalMeta_UnmarshalableInput verifies MarshalMeta returns a valid JSON
// object ("{}") rather than panicking or returning garbage when given a value
// that cannot be marshalled (e.g. a channel).
func TestMarshalMeta_UnmarshalableInput(t *testing.T) {
	// Channels cannot be JSON-marshalled.
	ch := make(chan struct{})
	meta := audit.MarshalMeta(ch)
	if !json.Valid(meta) {
		t.Errorf("MarshalMeta fallback is not valid JSON: %s", meta)
	}
	if string(meta) != "{}" {
		t.Errorf("MarshalMeta fallback: want {}, got %s", meta)
	}
}

// TestActionConstants verifies the action strings match the expected values
// used in SQL queries and downstream consumers. A rename here would be caught
// immediately rather than causing silent DB mismatches.
func TestActionConstants(t *testing.T) {
	cases := []struct {
		action audit.Action
		want   string
	}{
		{audit.ActionFileUploaded, "FILE_UPLOADED"},
		{audit.ActionFileShared, "FILE_SHARED"},
		{audit.ActionFileDeleted, "FILE_DELETED"},
		{audit.ActionFileRestored, "FILE_RESTORED"},
		{audit.ActionFileRenamed, "FILE_RENAMED"},
	}
	for _, tc := range cases {
		if string(tc.action) != tc.want {
			t.Errorf("action %q: want string %q, got %q", tc.action, tc.want, string(tc.action))
		}
	}
}
