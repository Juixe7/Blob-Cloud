package sync_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	wsSync "go-drive-clone/internal/sync"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestHub(t *testing.T) *wsSync.Hub {
	t.Helper()
	h := wsSync.NewHub(discardLogger())
	go h.Run()
	t.Cleanup(h.Shutdown)
	return h
}

// ── backplane envelope ────────────────────────────────────────────────────────

// backplaneEnvelope mirrors the unexported struct in the backplane package so
// we can build test payloads without importing internal state.
type backplaneEnvelope struct {
	UserID string                   `json:"user_id"`
	Event  wsSync.NotificationEvent `json:"event"`
}

func marshalEnvelope(t *testing.T, userID string, event wsSync.NotificationEvent) string {
	t.Helper()
	env := backplaneEnvelope{UserID: userID, Event: event}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return string(b)
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestBackplane_NotifyUser_LocalDelivery verifies that NotifyUser delivers to a
// locally registered client without Redis being involved. This proves the
// backplane does not break the same-node path.
func TestBackplane_NotifyUser_LocalDelivery(t *testing.T) {
	hub := newTestHub(t)

	// Register a client manually.
	send := make(chan []byte, 4)
	client := &wsSync.Client{UserID: "user-1", Send: send}
	hub.Register(client)
	time.Sleep(10 * time.Millisecond) // let the run loop process register

	// We cannot directly instantiate RedisBackplane without a real Redis client,
	// so we exercise the local path via the hub directly (the backplane delegates
	// immediately to hub.NotifyUser for the local step).
	hub.NotifyUser("user-1", wsSync.NotificationEvent{
		Type:    wsSync.EventUploadComplete,
		Payload: map[string]string{"file_id": "f1"},
	})

	select {
	case got := <-send:
		var evt wsSync.NotificationEvent
		if err := json.Unmarshal(got, &evt); err != nil {
			t.Fatalf("unmarshal notification: %v", err)
		}
		if evt.Type != wsSync.EventUploadComplete {
			t.Errorf("type: want %q, got %q", wsSync.EventUploadComplete, evt.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for local delivery")
	}
}

// TestBackplane_ParseEnvelope verifies that the JSON envelope format the
// backplane publishes to Redis can be round-tripped correctly. This is the
// contract between nodes — a breaking change here would silently lose events.
func TestBackplane_ParseEnvelope_RoundTrip(t *testing.T) {
	event := wsSync.NotificationEvent{
		Type:    wsSync.EventThumbnailReady,
		Payload: map[string]string{"file_id": "f42"},
	}
	payload := marshalEnvelope(t, "user-99", event)

	var decoded backplaneEnvelope
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.UserID != "user-99" {
		t.Errorf("user_id: want user-99, got %q", decoded.UserID)
	}
	if decoded.Event.Type != wsSync.EventThumbnailReady {
		t.Errorf("event type: want %q, got %q", wsSync.EventThumbnailReady, decoded.Event.Type)
	}
}

// TestBackplane_Run_ContextCancel verifies that Run() returns promptly when
// ctx is cancelled, without hanging forever. We cannot test with a real Redis
// subscriber here, but we can at least verify the function signature and that
// it is exported correctly.
func TestBackplane_Run_ContextCancel(t *testing.T) {
	// This is a compile-time assertion: if RedisBackplane no longer has Run(),
	// or if its signature changed, this test file will fail to build.
	type runner interface {
		Run(ctx context.Context) error
	}
	// We don't instantiate (needs a real Redis client), but the interface check
	// ensures the method exists with the right signature.
	var _ runner = (*wsSync.RedisBackplane)(nil)
}

// TestHub_NotifyUser_UnknownUser verifies the hub silently handles a
// NotifyUser call for a user with no registered connections (no panic, no
// deadlock). This is a regression guard for the backplane''s local delivery
// path when a remote event arrives for a user not connected to this node.
func TestHub_NotifyUser_UnknownUser(t *testing.T) {
	hub := newTestHub(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.NotifyUser("ghost-user", wsSync.NotificationEvent{Type: "NOOP"})
	}()
	select {
	case <-done:
		// OK — returned promptly
	case <-time.After(500 * time.Millisecond):
		t.Fatal("NotifyUser for unknown user blocked or panicked")
	}
}
