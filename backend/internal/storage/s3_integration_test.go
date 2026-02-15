//go:build integration

// S3 integration tests. These hit the real AWS S3 / Cloudflare R2 bucket
// configured via the .env file in the project root, so they are excluded from
// the default `go test` run.
//
// Run with:
//
//	go test -tags=integration ./internal/storage/ -v -run TestS3
//
// Requirements:
//   - a .env file at the repo root with STORAGE_PROVIDER=s3, AWS_S3_BUCKET,
//     AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY (and AWS_REGION / AWS_S3_ENDPOINT
//     / CLOUDFRONT_DOMAIN as needed).
package storage

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"

	appcfg "go-drive-clone/internal/config"
)

// loadEnv loads .env from cwd and ancestors, then returns the effective config.
func loadEnv(t *testing.T) appcfg.Config {
	t.Helper()
	// godotenv silently no-ops if .env is missing; that's fine — the caller may
	// have exported real env vars instead.
	_ = godotenv.Load("../../.env")

	cfg, err := appcfg.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// newS3OrFail builds an S3Storage from the env, skipping the test if S3 isn't
// selected or the bucket is unreachable.
func newS3OrFail(t *testing.T) *S3Storage {
	t.Helper()
	cfg := loadEnv(t)

	if cfg.StorageProvider != "s3" {
		t.Skipf("STORAGE_PROVIDER=%q, skipping S3 integration test", cfg.StorageProvider)
	}
	if cfg.AWSS3Bucket == "" {
		t.Skip("AWS_S3_BUCKET not set, skipping S3 integration test")
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s, err := NewS3Storage(context.Background(), cfg, log)
	if err != nil {
		t.Skipf("S3 unavailable, skipping: %v", err)
	}
	return s
}

// TestS3Storage_PresignPutUploadGetDelete exercises the full lifecycle against
// the real bucket: presign a PUT URL, upload via the presigned URL, then
// verify through GetObject and DeleteObject.
func TestS3Storage_PresignPutUploadGetDelete(t *testing.T) {
	s := newS3OrFail(t)
	ctx := context.Background()

	hash := "phase4-integration-test-block-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	key := s3BlockPrefix + "/" + hash
	payload := []byte("hello from phase 4 S3 integration test")

	t.Cleanup(func() {
		if err := s.DeleteObject(ctx, key); err != nil {
			t.Logf("cleanup delete %q: %v", key, err)
		}
	})

	// 1. Generate a presigned PUT URL.
	uploadURL, err := s.GenerateUploadURL(ctx, hash, 5*time.Minute)
	if err != nil {
		t.Fatalf("GenerateUploadURL: %v", err)
	}
	t.Logf("presigned URL: %s", uploadURL)
	if !strings.Contains(uploadURL, "X-Amz-Signature") {
		t.Fatalf("presigned URL missing signature query param: %s", uploadURL)
	}

	// 2. Upload via the presigned URL exactly as a browser would.
	req, err := newPresignedPutRequest(uploadURL, payload)
	if err != nil {
		t.Fatalf("build PUT request: %v", err)
	}
	resp, err := httpDefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT via presigned URL: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT returned %d: %s", resp.StatusCode, body)
	}

	// 3. Retrieve the object through the driver and verify the payload.
	rc, err := s.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q, want %q", got, payload)
	}

	// 4. Delete and confirm it's gone.
	if err := s.DeleteObject(ctx, key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if _, err := s.GetObject(ctx, key); err == nil {
		t.Fatal("expected GetObject to fail after delete, got nil error")
	}
}

// TestS3Storage_PutObjectDirect verifies the server-side PutObject path (used
// for thumbnails/metadata) round-trips correctly.
func TestS3Storage_PutObjectDirect(t *testing.T) {
	s := newS3OrFail(t)
	ctx := context.Background()

	key := s3BlockPrefix + "/phase4-putobject-direct-" + time.Now().Format("150405")
	payload := []byte("server-side put")

	t.Cleanup(func() { _ = s.DeleteObject(ctx, key) })

	if err := s.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	rc, err := s.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q, want %q", got, payload)
	}
}
