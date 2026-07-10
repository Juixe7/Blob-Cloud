// Package storage provides concrete implementations of domain.StorageProvider.
//
// This file implements LocalStorage: a content-addressable block store backed
// by the local filesystem. It mirrors the contract of an AWS S3 driver so the
// rest of the application (and the browser client) can run entirely offline in
// development. Swapping to S3 in Phase 4 only requires changing which
// implementation is wired in main.go — no client or handler changes needed.
package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"go-drive-clone/internal/domain"
)

// BlockPrefix is the sub-path/key under which deduplicated blocks live. It
// matches the route mounted by the HTTP layer (PUT /local-storage/blocks/{hash}).
const BlockPrefix = "blocks"

// LocalStore is a filesystem-backed implementation of domain.StorageProvider.
type LocalStore struct {
	baseDir string // root directory backing the store (e.g. ./tmp/storage)
	baseURL string // root URL of this server, used to build client-facing URLs
	log     *slog.Logger
}

// Compile-time check that LocalStore satisfies the StorageProvider interface.
var _ domain.StorageProvider = (*LocalStore)(nil)

// NewLocalStore creates a LocalStore rooted at baseDir. It ensures the blocks
// directory exists (creating it if necessary). baseURL should have no trailing
// slash and is used to construct the URLs returned by GenerateUploadURL.
func NewLocalStore(baseDir, baseURL string, log *slog.Logger) (*LocalStore, error) {
	if err := os.MkdirAll(filepath.Join(baseDir, BlockPrefix), 0o755); err != nil {
		return nil, fmt.Errorf("create local storage dir: %w", err)
	}
	return &LocalStore{
		baseDir: baseDir,
		baseURL: baseURL,
		log:     log,
	}, nil
}

// GenerateUploadURL returns a URL that a client can PUT a block to. For the
// local driver this points back at our own HTTP server's simulated S3 endpoint.
// The lifetime is accepted to honour the interface but is not enforced for
// local uploads (the route is always available while the server runs).
func (s *LocalStore) GenerateUploadURL(_ context.Context, blockHash string, _ time.Duration) (string, error) {
	if blockHash == "" {
		return "", fmt.Errorf("blockHash must not be empty")
	}
	// Build: <baseURL>/local-storage/blocks/<blockHash>
	u, err := url.JoinPath(s.baseURL, "local-storage", BlockPrefix, blockHash)
	if err != nil {
		return "", fmt.Errorf("build upload URL: %w", err)
	}
	return u, nil
}

// PutObject writes the contents of reader to the local file for the given key.
// size may be -1 if unknown; contentType is ignored by the local driver.
func (s *LocalStore) PutObject(_ context.Context, key string, reader io.Reader, _ int64, _ string) error {
	path := s.keyToPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create object dir: %w", err)
	}

	// Write to a temp file in the same directory and atomically rename, so a
	// crash mid-upload never leaves a half-written block.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if _, err := io.Copy(tmp, reader); err != nil {
		cleanup()
		return fmt.Errorf("write object: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("persist object: %w", err)
	}
	return nil
}

// GetObject opens the stored object for reading. The caller must close the
// returned ReadCloser.
func (s *LocalStore) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	path := s.keyToPath(key)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("object %q: %w", key, err)
		}
		return nil, fmt.Errorf("open object: %w", err)
	}
	return f, nil
}

// DeleteObject removes the object identified by key. Missing objects are
// treated as success (idempotent), matching the S3 semantics.
func (s *LocalStore) DeleteObject(_ context.Context, key string) error {
	path := s.keyToPath(key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// keyToPath resolves a storage key (e.g. "blocks/<hash>") to an absolute
// filesystem path under baseDir. Keys are cleaned to prevent path traversal.
func (s *LocalStore) keyToPath(key string) string {
	// filepath.Clean collapses ".." / "." segments so callers cannot escape
	// baseDir via a crafted key.
	cleaned := filepath.Clean(string(os.PathSeparator) + key)
	// Trim the leading separator that Clean prepended so Join stays in baseDir.
	return filepath.Join(s.baseDir, cleaned[len(string(os.PathSeparator)):])
}
