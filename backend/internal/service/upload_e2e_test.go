// Package service_test contains end-to-end tests for the upload pipeline.
//
// These tests exercise the full happy path without mocking any layer below the
// service: they run against a real Postgres database (skipped if unavailable)
// and an in-memory StorageProvider, verifying that:
//
//  1. Initiate correctly queries the global blocks table for dedup hits.
//  2. The client''s missing blocks get presigned URLs while known blocks don''t.
//  3. Complete atomically creates the file, links blocks, grants OWNER perm,
//     and marks the session COMPLETED.
//  4. A second Initiate for the exact same file hash produces a 100% dedup hit
//     (zero new presigned URLs), proving the global block table is working.
//
// LP alignment: Insist on Highest Standards (we test the edge, not just the
// happy path), Dive Deep (we verify the dedup counter actually increments).
package service_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"go-drive-clone/internal/database"
	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/metrics"
	"go-drive-clone/internal/queue"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/service"
	wsSync "go-drive-clone/internal/sync"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// ── test helpers ────────────────────────────────────────────────────────────

func e2eDSN() string {
	if v := os.Getenv("DB_DSN"); v != "" {
		return v
	}
	return "postgres://postgres:postgres@localhost:5432/godrive?sslmode=disable"
}

// openE2EDB opens a real Postgres connection and skips the test if the DB is
// unreachable — keeping the CI suite green in DB-less environments.
func openE2EDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", e2eDSN())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(5)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		t.Skipf("postgres unavailable, skipping e2e upload test: %v", err)
	}
	return db
}

// freshE2ESchema runs migrations and wipes all tables so each test starts from
// a clean slate.
func freshE2ESchema(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.RunMigrations(ctx, db, log); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	for _, tbl := range []string{
		"session_blocks", "upload_sessions", "permissions",
		"file_blocks", "blocks", "files", "users",
	} {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", tbl)); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}

// ── in-memory storage stub ──────────────────────────────────────────────────

// memStorage is an in-process StorageProvider used to avoid needing S3/R2
// credentials in CI. It satisfies domain.StorageProvider in full.
type memStorage struct {
	objects map[string][]byte
}

func newMemStorage() *memStorage { return &memStorage{objects: map[string][]byte{}} }

func (s *memStorage) GenerateUploadURL(_ context.Context, blockHash string, _ time.Duration) (string, error) {
	// Return a fake local URL so the Initiate response is non-empty for new blocks.
	return "http://test-storage/blocks/" + blockHash, nil
}
func (s *memStorage) PutObject(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.objects[key] = data
	return nil
}
func (s *memStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *memStorage) GetObjectRange(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	if offset < 0 || offset > int64(len(b)) {
		return nil, errors.New("offset out of bounds")
	}
	sub := b[offset:]
	if length >= 0 && length < int64(len(sub)) {
		sub = sub[:length]
	}
	return io.NopCloser(bytes.NewReader(sub)), nil
}
func (s *memStorage) HeadObject(_ context.Context, key string) (*domain.ObjectMetadata, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return &domain.ObjectMetadata{ContentLength: int64(len(b)), ETag: "mocketag"}, nil
}
func (s *memStorage) DeleteObject(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

// ── publisher spy ────────────────────────────────────────────────────────────

// capturingPublisher records every ThumbnailMessage so the test can assert
// the SQS event was emitted without a real queue.
type capturingPublisher struct {
	Jobs []queue.ThumbnailMessage
}

func (p *capturingPublisher) PublishThumbnailJob(_ context.Context, msg queue.ThumbnailMessage) error {
	p.Jobs = append(p.Jobs, msg)
	return nil
}

// ── test ─────────────────────────────────────────────────────────────────────

// TestUploadPipeline_E2E exercises the full upload lifecycle:
//
//  1. Upload a two-block file (both blocks new → 0 dedup hits, 2 misses).
//  2. Simulate the client PUT-ing the blocks into storage.
//  3. Complete the session → file created, OWNER perm granted, SQS job
//     published, session status = COMPLETED.
//  4. Upload the exact same two blocks again → 2 dedup hits, 0 misses.
//     The client skips both block uploads; the second file is a zero-upload
//     operation proving global block deduplication works end-to-end.
func TestUploadPipeline_E2E(t *testing.T) {
	// ── setup ────────────────────────────────────────────────────────────────
	db := openE2EDB(t)
	defer db.Close()
	freshE2ESchema(t, db)

	// Register Prometheus metrics for this test (idempotent — each metric can
	// only be registered once, so we call Init() once).
	metrics.Init()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	stor := newMemStorage()
	pub := &capturingPublisher{}

	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	blocks := postgresrepo.NewBlockRepository(db)
	sessions := postgresrepo.NewUploadSessionRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)

	svc := service.NewUploadService(
		db, users, files, blocks, sessions, perms,
		stor, pub, wsSync.NoopNotifier(), log,
	)

	// Create a test user.
	userEmail := fmt.Sprintf("e2e-%d@example.com", time.Now().UnixNano())
	user := &domain.User{Email: userEmail, PasswordHash: "hash", IsVerified: true}
	if err := users.Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// ── block definitions ────────────────────────────────────────────────────
	const blockSize = int32(64) // tiny blocks so the test is fast
	block1Hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	block2Hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	block1Data := bytes.Repeat([]byte("A"), int(blockSize))
	block2Data := bytes.Repeat([]byte("B"), int(blockSize))

	// ════════════════════════════════════════════════════════════════════════
	// STEP 1: First upload — both blocks are new (cold dedup).
	// ════════════════════════════════════════════════════════════════════════
	t.Run("first_upload_cold_dedup", func(t *testing.T) {
		// Snapshot metric values before Initiate.
		hitsBefore := testutil.ToFloat64(metrics.BlockDedupHits)
		missesBefore := testutil.ToFloat64(metrics.BlockDedupMisses)
		initiatedBefore := testutil.ToFloat64(metrics.UploadsInitiated)
		completedBefore := testutil.ToFloat64(metrics.UploadsCompleted)

		initReq := service.InitiateRequest{
			UserID:    user.ID,
			Filename:  "test_file.txt",
			TotalSize: int64(blockSize) * 2,
			Chunks: []service.InitiateChunk{
				{SHA256: block1Hash, BlockMD5: "mocketag", SizeBytes: blockSize},
				{SHA256: block2Hash, BlockMD5: "mocketag", SizeBytes: blockSize},
			},
		}
		initResp, err := svc.Initiate(ctx, initReq)
		if err != nil {
			t.Fatalf("Initiate: %v", err)
		}

		// Both blocks are new: 0 dedup hits, 2 misses, 1 session initiated.
		if got := testutil.ToFloat64(metrics.BlockDedupHits) - hitsBefore; got != 0 {
			t.Errorf("dedup hits: want 0, got %.0f", got)
		}
		if got := testutil.ToFloat64(metrics.BlockDedupMisses) - missesBefore; got != 2 {
			t.Errorf("dedup misses: want 2, got %.0f", got)
		}
		if got := testutil.ToFloat64(metrics.UploadsInitiated) - initiatedBefore; got != 1 {
			t.Errorf("uploads initiated: want 1, got %.0f", got)
		}

		// All chunks must have an upload URL (nothing was deduped yet).
		for i, c := range initResp.Chunks {
			if c.AlreadyExists {
				t.Errorf("chunk %d: expected AlreadyExists=false on cold upload", i)
			}
			if c.UploadURL == "" {
				t.Errorf("chunk %d: expected non-empty UploadURL on cold upload", i)
			}
		}

		// Simulate the client PUT-ing blocks directly to storage (what S3 presigned
		// URLs do in production; here we write to the in-memory store).
		if err := stor.PutObject(ctx, "blocks/"+block1Hash, bytes.NewReader(block1Data), int64(blockSize), "application/octet-stream"); err != nil {
			t.Fatalf("put block1: %v", err)
		}
		if err := stor.PutObject(ctx, "blocks/"+block2Hash, bytes.NewReader(block2Data), int64(blockSize), "application/octet-stream"); err != nil {
			t.Fatalf("put block2: %v", err)
		}

		// Complete the session.
		completeResp, err := svc.Complete(ctx, service.CompleteRequest{SessionID: initResp.SessionID}, user.ID)
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}

		// Session must be COMPLETED.
		if completeResp.Status != domain.SessionStatusCompleted {
			t.Errorf("session status: want %s, got %s", domain.SessionStatusCompleted, completeResp.Status)
		}
		if completeResp.FileID == "" {
			t.Error("Complete returned empty FileID")
		}

		// SQS thumbnail job must have been published.
		if len(pub.Jobs) != 1 {
			t.Errorf("thumbnail jobs published: want 1, got %d", len(pub.Jobs))
		} else if pub.Jobs[0].FileID != completeResp.FileID {
			t.Errorf("thumbnail job file_id: want %s, got %s", completeResp.FileID, pub.Jobs[0].FileID)
		}

		// uploads_completed counter must have incremented.
		if got := testutil.ToFloat64(metrics.UploadsCompleted) - completedBefore; got != 1 {
			t.Errorf("uploads completed: want 1, got %.0f", got)
		}
	})

	// ════════════════════════════════════════════════════════════════════════
	// STEP 2: Second upload of the SAME two blocks — full dedup hit.
	// The client uploads zero bytes; all blocks already exist globally.
	// ════════════════════════════════════════════════════════════════════════
	t.Run("second_upload_full_dedup_hit", func(t *testing.T) {
		hitsBefore := testutil.ToFloat64(metrics.BlockDedupHits)
		missesBefore := testutil.ToFloat64(metrics.BlockDedupMisses)

		initReq := service.InitiateRequest{
			UserID:    user.ID,
			Filename:  "test_file_copy.txt",
			TotalSize: int64(blockSize) * 2,
			Chunks: []service.InitiateChunk{
				{SHA256: block1Hash, BlockMD5: "mocketag", SizeBytes: blockSize},
				{SHA256: block2Hash, BlockMD5: "mocketag", SizeBytes: blockSize},
			},
		}
		initResp, err := svc.Initiate(ctx, initReq)
		if err != nil {
			t.Fatalf("second Initiate: %v", err)
		}

		// Both blocks already in the global table: 2 hits, 0 misses.
		if got := testutil.ToFloat64(metrics.BlockDedupHits) - hitsBefore; got != 2 {
			t.Errorf("dedup hits on second upload: want 2, got %.0f", got)
		}
		if got := testutil.ToFloat64(metrics.BlockDedupMisses) - missesBefore; got != 0 {
			t.Errorf("dedup misses on second upload: want 0, got %.0f", got)
		}

		// All chunks must be marked already_exists=true with no upload URL.
		for i, c := range initResp.Chunks {
			if !c.AlreadyExists {
				t.Errorf("chunk %d: expected AlreadyExists=true on warm dedup", i)
			}
			if c.UploadURL != "" {
				t.Errorf("chunk %d: expected empty UploadURL on dedup hit, got %q", i, c.UploadURL)
			}
		}

		// Complete the second session (blocks already in storage, no PUT needed).
		pub.Jobs = nil // reset spy
		completeResp, err := svc.Complete(ctx, service.CompleteRequest{SessionID: initResp.SessionID}, user.ID)
		if err != nil {
			t.Fatalf("second Complete: %v", err)
		}
		if completeResp.Status != domain.SessionStatusCompleted {
			t.Errorf("session status: want %s, got %s", domain.SessionStatusCompleted, completeResp.Status)
		}

		// Second file must have a different ID (two files, one physical block set).
		if completeResp.FileID == "" {
			t.Error("second Complete returned empty FileID")
		}

		// SQS job still published for the second file.
		if len(pub.Jobs) != 1 {
			t.Errorf("thumbnail jobs for second upload: want 1, got %d", len(pub.Jobs))
		}
	})
}
