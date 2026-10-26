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
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go-drive-clone/internal/domain"
)

// BlockPrefix is the sub-path/key under which deduplicated blocks live.
const BlockPrefix = "blocks"

// StagingPrefix is the sub-path/key under which unverified uploads are quarantined.
const StagingPrefix = "staging"

// LocalStore is a filesystem-backed implementation of domain.StorageProvider.
type LocalStore struct {
	baseDir   string // root directory backing the store (e.g. ./tmp/storage)
	baseURL   string // root URL of this server, used to build client-facing URLs
	uploadKey []byte // per-process key used to sign local staging capabilities
	log       *slog.Logger
}

// Compile-time check that LocalStore satisfies the StorageProvider interface.
var _ domain.StorageProvider = (*LocalStore)(nil)

// NewLocalStore creates a LocalStore rooted at baseDir. It ensures the blocks
// and staging directories exist (creating them if necessary). baseURL should
// have no trailing slash and is used to construct local staging upload URLs.
func NewLocalStore(baseDir, baseURL string, log *slog.Logger) (*LocalStore, error) {
	uploadKey := make([]byte, 32)
	if _, err := rand.Read(uploadKey); err != nil {
		return nil, fmt.Errorf("generate local upload signing key: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(baseDir, BlockPrefix), 0o755); err != nil {
		return nil, fmt.Errorf("create local storage dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(baseDir, StagingPrefix), 0o755); err != nil {
		return nil, fmt.Errorf("create local staging dir: %w", err)
	}
	return &LocalStore{
		baseDir:   baseDir,
		baseURL:   baseURL,
		uploadKey: uploadKey,
		log:       log,
	}, nil
}

// GenerateStagingUploadURL returns a short-lived, signed URL that a client can
// PUT a temporary chunk to. The session service still validates the declared
// sequence, size, and SHA-256 before accepting the staged object.
func (s *LocalStore) GenerateStagingUploadURL(_ context.Context, stagingKey string, expires time.Duration) (string, error) {
	if err := validateStagingKey(stagingKey); err != nil {
		return "", err
	}
	if expires <= 0 {
		return "", fmt.Errorf("staging URL expiry must be positive")
	}
	// Build: <baseURL>/local-storage/<stagingKey>
	u, err := url.JoinPath(s.baseURL, "local-storage", stagingKey)
	if err != nil {
		return "", fmt.Errorf("build staging upload URL: %w", err)
	}
	expiresAt := time.Now().Add(expires).Unix()
	parsed, err := url.Parse(u)
	if err != nil {
		return "", fmt.Errorf("parse staging upload URL: %w", err)
	}
	query := parsed.Query()
	query.Set("expires", strconv.FormatInt(expiresAt, 10))
	query.Set("signature", s.signStagingUpload(stagingKey, expiresAt))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// ValidateStagingUploadURL validates a URL capability and rejects expired or
// modified paths. The signed material is the exact storage key and expiry.
func (s *LocalStore) ValidateStagingUploadURL(stagingKey string, expiresAt int64, signature string) bool {
	if validateStagingKey(stagingKey) != nil || expiresAt <= time.Now().Unix() || signature == "" {
		return false
	}
	expected, err := base64.RawURLEncoding.DecodeString(s.signStagingUpload(stagingKey, expiresAt))
	if err != nil {
		return false
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	return err == nil && hmac.Equal(expected, provided)
}

func validateStagingKey(key string) error {
	if !strings.HasPrefix(key, StagingPrefix+"/") || strings.Contains(key, "\\") || path.Clean(key) != key {
		return fmt.Errorf("invalid staging key")
	}
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[1] == "" {
		return fmt.Errorf("invalid staging key")
	}
	sequence, err := strconv.Atoi(parts[2])
	if err != nil || sequence < 0 || strconv.Itoa(sequence) != parts[2] {
		return fmt.Errorf("invalid staging key")
	}
	return nil
}

func validateCASKey(key string) error {
	if !strings.HasPrefix(key, BlockPrefix+"/") || path.Clean(key) != key {
		return fmt.Errorf("invalid CAS key")
	}
	parts := strings.Split(key, "/")
	if len(parts) != 2 || len(parts[1]) != sha256.Size*2 {
		return fmt.Errorf("invalid CAS key")
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return fmt.Errorf("invalid CAS key")
	}
	return nil
}

func (s *LocalStore) signStagingUpload(stagingKey string, expiresAt int64) string {
	mac := hmac.New(sha256.New, s.uploadKey)
	_, _ = fmt.Fprintf(mac, "%s\n%d", stagingKey, expiresAt)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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

// GetObjectRange opens the local file starting at offset for up to length bytes.
func (s *LocalStore) GetObjectRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	path := s.keyToPath(key)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("object %q: %w", key, err)
		}
		return nil, fmt.Errorf("open object: %w", err)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return nil, fmt.Errorf("seek local object to %d: %w", offset, err)
	}
	if length >= 0 {
		return &struct {
			io.Reader
			io.Closer
		}{io.LimitReader(f, length), f}, nil
	}
	return f, nil
}

// HeadObject returns object size and MD5 hash for local storage.
func (s *LocalStore) HeadObject(_ context.Context, key string) (*domain.ObjectMetadata, error) {
	path := s.keyToPath(key)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("object %q: %w", key, err)
		}
		return nil, fmt.Errorf("stat object: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read object for md5: %w", err)
	}
	hash := md5.Sum(data)
	etag := hex.EncodeToString(hash[:])

	return &domain.ObjectMetadata{
		ContentLength: info.Size(),
		ETag:          etag,
	}, nil
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

// PromoteObject moves a verified object from staging to its permanent CAS key.
func (s *LocalStore) PromoteObject(_ context.Context, srcKey, destKey string) error {
	if err := validateStagingKey(srcKey); err != nil {
		return err
	}
	if err := validateCASKey(destKey); err != nil {
		return err
	}
	srcPath := s.keyToPath(srcKey)
	destPath := s.keyToPath(destKey)

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	// Try atomic rename first
	if err := os.Rename(srcPath, destPath); err == nil {
		return nil
	}

	// Fallback to copy + delete if across volume boundaries
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open src %q for promote: %w", srcKey, err)
	}
	defer srcFile.Close()

	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create dest %q for promote: %w", destKey, err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		_ = os.Remove(destPath)
		return fmt.Errorf("copy object for promote: %w", err)
	}

	_ = srcFile.Close()
	_ = os.Remove(srcPath)
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

// ListBlockKeys walks the blocks/ subdirectory and returns every stored block
// key in "blocks/<sha256>" form. Satisfies the gc.BlockLister interface used
// by the orphaned-block garbage collector.
func (s *LocalStore) ListBlockKeys(_ context.Context) ([]string, error) {
	dir := filepath.Join(s.baseDir, BlockPrefix)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list local block keys: %w", err)
	}
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			keys = append(keys, BlockPrefix+"/"+e.Name())
		}
	}
	return keys, nil
}

// ListBlockObjects returns block keys with filesystem modification times for GC.
func (s *LocalStore) ListBlockObjects(_ context.Context) ([]domain.StorageObject, error) {
	dir := filepath.Join(s.baseDir, BlockPrefix)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list local block objects: %w", err)
	}
	objects := make([]domain.StorageObject, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat local block %q: %w", entry.Name(), err)
		}
		objects = append(objects, domain.StorageObject{Key: BlockPrefix + "/" + entry.Name(), LastModified: info.ModTime()})
	}
	return objects, nil
}

// ListStagingKeys walks the staging/ subdirectory and returns all staged object keys.
// Satisfies the gc.StagingLister interface.
func (s *LocalStore) ListStagingKeys(_ context.Context) ([]string, error) {
	dir := filepath.Join(s.baseDir, StagingPrefix)
	var keys []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			rel, relErr := filepath.Rel(s.baseDir, path)
			if relErr == nil {
				keys = append(keys, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list local staging keys: %w", err)
	}
	return keys, nil
}

// ListStagingObjects returns staging keys with modification times for GC.
func (s *LocalStore) ListStagingObjects(_ context.Context) ([]domain.StorageObject, error) {
	dir := filepath.Join(s.baseDir, StagingPrefix)
	var objects []domain.StorageObject
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			rel, relErr := filepath.Rel(s.baseDir, path)
			if relErr != nil {
				return relErr
			}
			objects = append(objects, domain.StorageObject{Key: filepath.ToSlash(rel), LastModified: info.ModTime()})
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list local staging objects: %w", err)
	}
	return objects, nil
}
