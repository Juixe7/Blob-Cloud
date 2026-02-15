package storage

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestStore creates a LocalStore rooted in a fresh temp directory and
// returns a cleanup func that removes it. The silent slog.Logger keeps test
// output readable.
func newTestStore(t *testing.T) (*LocalStore, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "go-drive-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	store, err := NewLocalStore(dir, "http://localhost:8080", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store, func() { _ = os.RemoveAll(dir) }
}

func TestPutAndGetRoundTrip(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	key := "blocks/abc123"
	want := []byte("hello world, this is a block payload")

	if err := store.PutObject(ctx, key, bytes.NewReader(want), int64(len(want)), "application/octet-stream"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	rc, err := store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch: got %q, want %q", got, want)
	}
}

func TestGetObject_MissingIsError(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	if _, err := store.GetObject(context.Background(), "blocks/does-not-exist"); err == nil {
		t.Fatal("expected error for missing object, got nil")
	}
}

func TestDeleteObject(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	key := "blocks/deleteme"
	payload := []byte("bye")

	if err := store.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), ""); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	if err := store.DeleteObject(ctx, key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	// File should be gone from disk.
	if _, err := os.Stat(store.keyToPath(key)); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, stat err = %v", err)
	}
}

func TestDeleteObject_IdempotentWhenMissing(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	// Deleting a key that was never written must not error.
	if err := store.DeleteObject(context.Background(), "blocks/never-existed"); err != nil {
		t.Fatalf("DeleteObject on missing key: %v", err)
	}
}

func TestPutObject_OverwritesExisting(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	key := "blocks/replace"
	if err := store.PutObject(ctx, key, strings.NewReader("v1"), 2, ""); err != nil {
		t.Fatalf("PutObject v1: %v", err)
	}
	if err := store.PutObject(ctx, key, strings.NewReader("v2-content"), 10, ""); err != nil {
		t.Fatalf("PutObject v2: %v", err)
	}

	rc, err := store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "v2-content" {
		t.Fatalf("got %q, want v2-content", got)
	}
}

func TestGenerateUploadURL(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir, "http://localhost:8080", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	got, err := store.GenerateUploadURL(context.Background(), "deadbeef", 5*time.Minute)
	if err != nil {
		t.Fatalf("GenerateUploadURL: %v", err)
	}
	want := "http://localhost:8080/local-storage/blocks/deadbeef"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGenerateUploadURL_RejectsEmptyHash(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	if _, err := store.GenerateUploadURL(context.Background(), "", time.Minute); err == nil {
		t.Fatal("expected error for empty blockHash, got nil")
	}
}

// TestPutObject_NoPathTraversal ensures a crafted key cannot escape baseDir.
func TestPutObject_NoPathTraversal(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	payload := []byte("evil")
	// A key attempting to break out of the blocks dir.
	malicious := "blocks/../../escaped"

	if err := store.PutObject(ctx, malicious, bytes.NewReader(payload), int64(len(payload)), ""); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// The written file must live under baseDir, never at <baseDir>/../escaped.
	escapedPath := filepath.Join(store.baseDir, "..", "escaped")
	if _, err := os.Stat(escapedPath); !os.IsNotExist(err) {
		t.Fatalf("traversal escaped baseDir: stat(%q) err = %v", escapedPath, err)
	}
}
