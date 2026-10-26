// Package service_test contains end-to-end tests for the upload pipeline.
//
// These tests exercise the full happy path without mocking any layer below the
// service: they run against a real Postgres database (skipped if unavailable)
// and an in-memory StorageProvider, verifying that:
//
//  1. Initiate correctly queries the global blocks table for dedup hits.
//  2. The client”s missing blocks get presigned URLs while known blocks don”t.
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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
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
func (s *memStorage) GenerateStagingUploadURL(_ context.Context, stagingKey string, _ time.Duration) (string, error) {
	return "http://test-storage/" + stagingKey, nil
}
func (s *memStorage) PromoteObject(_ context.Context, srcKey, destKey string) error {
	b, ok := s.objects[srcKey]
	if !ok {
		return errors.New("not found: " + srcKey)
	}
	s.objects[destKey] = b
	delete(s.objects, srcKey)
	return nil
}

// ── publisher spy ────────────────────────────────────────────────────────────

// capturingPublisher records every ThumbnailMessage so the test can assert
// the SQS event was emitted without a real queue.
type capturingPublisher struct {
	Jobs []queue.ThumbnailMessage
	Err  error
}

func (p *capturingPublisher) PublishThumbnailJob(_ context.Context, msg queue.ThumbnailMessage) error {
	if p.Err != nil {
		return p.Err
	}
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
	block1Data := bytes.Repeat([]byte("A"), int(blockSize))
	block2Data := bytes.Repeat([]byte("B"), int(blockSize))
	h1 := sha256.Sum256(block1Data)
	h2 := sha256.Sum256(block2Data)
	block1Hash := hex.EncodeToString(h1[:])
	block2Hash := hex.EncodeToString(h2[:])

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

		// Simulate the client PUT-ing blocks to staging keys (Zero-Trust pipeline)
		stagingKey1 := fmt.Sprintf("staging/%s/0", initResp.SessionID)
		stagingKey2 := fmt.Sprintf("staging/%s/1", initResp.SessionID)
		if err := stor.PutObject(ctx, stagingKey1, bytes.NewReader(block1Data), int64(blockSize), "application/octet-stream"); err != nil {
			t.Fatalf("put block1: %v", err)
		}
		if err := stor.PutObject(ctx, stagingKey2, bytes.NewReader(block2Data), int64(blockSize), "application/octet-stream"); err != nil {
			t.Fatalf("put block2: %v", err)
		}

		// Complete the session.
		completeResp, err := svc.Complete(ctx, service.CompleteRequest{SessionID: initResp.SessionID}, user.ID)
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}

		// Verify promotion to CAS blocks/ occurred and staging objects were deleted
		if _, err := stor.HeadObject(ctx, "blocks/"+block1Hash); err != nil {
			t.Errorf("block1 not promoted to CAS: %v", err)
		}
		if _, err := stor.HeadObject(ctx, "blocks/"+block2Hash); err != nil {
			t.Errorf("block2 not promoted to CAS: %v", err)
		}
		if _, err := stor.HeadObject(ctx, stagingKey1); err == nil {
			t.Errorf("stagingKey1 not cleaned up after promotion")
		}

		// Session must be COMPLETED.
		if completeResp.Status != domain.SessionStatusCompleted {
			t.Errorf("session status: want %s, got %s", domain.SessionStatusCompleted, completeResp.Status)
		}
		if completeResp.FileID == "" {
			t.Error("Complete returned empty FileID")
		}
		file, err := files.GetByID(ctx, completeResp.FileID)
		if err != nil {
			t.Fatalf("load completed file: %v", err)
		}
		if file.Status != "PROCESSING" {
			t.Fatalf("new upload status = %q, want PROCESSING", file.Status)
		}
		var jobs int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_processing_outbox WHERE file_id = $1`, file.ID).Scan(&jobs); err != nil {
			t.Fatalf("count processing outbox jobs: %v", err)
		}
		if jobs != 1 {
			t.Fatalf("processing outbox rows = %d, want 1", jobs)
		}
		if _, err := db.ExecContext(ctx, `UPDATE file_processing_outbox SET next_attempt_at = NOW() WHERE file_id = $1`, file.ID); err != nil {
			t.Fatalf("make outbox job due: %v", err)
		}
		failedPublisher := &capturingPublisher{Err: errors.New("queue unavailable")}
		if err := queue.NewOutboxDispatcher(db, failedPublisher, log).DispatchOnce(ctx); err != nil {
			t.Fatalf("dispatch retryable outbox job: %v", err)
		}
		var attempts int
		if err := db.QueryRowContext(ctx, `SELECT attempts FROM file_processing_outbox WHERE file_id = $1`, file.ID).Scan(&attempts); err != nil {
			t.Fatalf("read outbox attempts: %v", err)
		}
		if attempts != 1 {
			t.Fatalf("outbox attempts = %d, want 1 after publish failure", attempts)
		}
		fileSvc := service.NewFileService(db, users, files, blocks, perms, stor, log)
		if err := fileSvc.AuthoriseRead(ctx, user.ID, file.ID); err == nil {
			t.Fatal("owner could read file before scan completed")
		}
		activated, err := files.UpdateProcessingStatus(ctx, file.ID, file.UpdatedAt, "ACTIVE")
		if err != nil || !activated {
			t.Fatalf("activate clean file fixture: activated=%v err=%v", activated, err)
		}
		if err := fileSvc.AuthoriseRead(ctx, user.ID, file.ID); err != nil {
			t.Fatalf("owner could not read active file: %v", err)
		}
		if err := files.UpdateStatus(ctx, file.ID, "PROCESSING"); err != nil {
			t.Fatalf("reset file fixture to processing: %v", err)
		}
		file, err = files.GetByID(ctx, file.ID)
		if err != nil {
			t.Fatalf("reload file fixture: %v", err)
		}
		quarantined, err := files.UpdateProcessingStatus(ctx, file.ID, file.UpdatedAt, "QUARANTINED")
		if err != nil || !quarantined {
			t.Fatalf("quarantine file fixture: quarantined=%v err=%v", quarantined, err)
		}
		if err := fileSvc.AuthoriseRead(ctx, user.ID, file.ID); err == nil {
			t.Fatal("owner could read quarantined file")
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

func TestUploadDestinationAuthorization_RequiresEditorOnLiveFolder(t *testing.T) {
	db := openE2EDB(t)
	defer db.Close()
	freshE2ESchema(t, db)
	metrics.Init()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	storage := newMemStorage()
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	blocks := postgresrepo.NewBlockRepository(db)
	sessions := postgresrepo.NewUploadSessionRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)
	svc := service.NewUploadService(db, users, files, blocks, sessions, perms, storage, queue.NoopPublisher{}, wsSync.NoopNotifier(), log)

	owner := &domain.User{Email: fmt.Sprintf("folder-owner-%d@example.com", time.Now().UnixNano()), PasswordHash: "hash", IsVerified: true}
	uploader := &domain.User{Email: fmt.Sprintf("folder-uploader-%d@example.com", time.Now().UnixNano()), PasswordHash: "hash", IsVerified: true}
	for _, user := range []*domain.User{owner, uploader} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	folder := &domain.File{UserID: owner.ID, Name: "shared-destination", IsDirectory: true}
	if err := files.Create(ctx, folder); err != nil {
		t.Fatalf("create destination folder: %v", err)
	}
	payload := []byte("folder upload validation")
	hash := sha256.Sum256(payload)
	request := service.InitiateRequest{
		UserID: uploader.ID, Filename: "payload.txt", ParentID: &folder.ID,
		TotalSize: int64(len(payload)),
		Chunks:    []service.InitiateChunk{{SHA256: hex.EncodeToString(hash[:]), SizeBytes: int32(len(payload))}},
	}

	if _, err := svc.Initiate(ctx, request); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("unshared destination should reject upload, got %v", err)
	}
	if err := perms.GrantPermission(ctx, &domain.Permission{FileID: folder.ID, GranteeEmail: uploader.Email, Role: domain.RoleViewer}); err != nil {
		t.Fatalf("grant viewer permission: %v", err)
	}
	if _, err := svc.Initiate(ctx, request); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("viewer destination should reject upload, got %v", err)
	}
	if err := perms.UpdatePermission(ctx, folder.ID, uploader.Email, domain.RoleEditor); err != nil {
		t.Fatalf("promote to editor: %v", err)
	}
	initResp, err := svc.Initiate(ctx, request)
	if err != nil {
		t.Fatalf("editor should initiate upload: %v", err)
	}

	// Revoke write access after initiation. Completion must recheck before it
	// promotes any untrusted staged bytes into the canonical CAS namespace.
	if err := perms.UpdatePermission(ctx, folder.ID, uploader.Email, domain.RoleViewer); err != nil {
		t.Fatalf("demote to viewer: %v", err)
	}
	storage.objects[fmt.Sprintf("staging/%s/0", initResp.SessionID)] = payload
	if _, err := svc.Complete(ctx, service.CompleteRequest{SessionID: initResp.SessionID}, uploader.ID); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("revoked destination write permission should reject completion, got %v", err)
	}
	if _, ok := storage.objects["blocks/"+hex.EncodeToString(hash[:])]; ok {
		t.Fatal("completion promoted a block after destination permission was revoked")
	}

	regularFile := &domain.File{UserID: owner.ID, Name: "not-a-folder.txt"}
	if err := files.Create(ctx, regularFile); err != nil {
		t.Fatalf("create non-folder destination fixture: %v", err)
	}
	request.ParentID = &regularFile.ID
	if _, err := svc.Initiate(ctx, request); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("non-directory destination should reject upload, got %v", err)
	}
}
