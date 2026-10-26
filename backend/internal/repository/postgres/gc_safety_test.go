package postgresrepo_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/gc"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/storage"
)

func TestGCCandidateLockSerializesWithUploadSessionCreation(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)
	ctx := context.Background()
	repo := postgresrepo.NewBlockRepository(db)
	sessions := postgresrepo.NewUploadSessionRepository(db)
	hash := "gc-lock-race-hash"
	deleting := make(chan struct{})
	releaseDelete := make(chan struct{})
	type candidateResult struct {
		eligible bool
		err      error
	}
	candidateDone := make(chan candidateResult, 1)
	go func() {
		eligible, err := repo.WithBlockCandidate(ctx, hash, false, func() error {
			close(deleting)
			<-releaseDelete
			return nil
		})
		candidateDone <- candidateResult{eligible: eligible, err: err}
	}()
	<-deleting

	session := &domain.UploadSession{UserID: "00000000-0000-0000-0000-000000000001", Filename: "upload.bin", TotalSize: 1, Status: domain.SessionStatusInitiated}
	sessionDone := make(chan error, 1)
	go func() {
		sessionDone <- sessions.CreateSession(ctx, session, []domain.SessionBlock{{BlockHash: hash, SequenceNumber: 0, SizeBytes: 1}})
	}()
	select {
	case err := <-sessionDone:
		t.Fatalf("session creation passed the GC hash lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseDelete)

	result := <-candidateDone
	if result.err != nil || !result.eligible {
		t.Fatalf("GC candidate result = eligible %v, err %v; want eligible without error", result.eligible, result.err)
	}
	if err := <-sessionDone; err != nil {
		t.Fatalf("create session after GC deletion: %v", err)
	}
	inUse, err := repo.WithBlockCandidate(ctx, hash, false, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if inUse {
		t.Fatal("GC candidate was eligible after an active session registered the hash")
	}
}

func TestGCLeavesActiveUploadBlockAndStagingObjectsAlone(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)
	ctx := context.Background()
	repo := postgresrepo.NewBlockRepository(db)
	sessions := postgresrepo.NewUploadSessionRepository(db)
	session := &domain.UploadSession{UserID: "00000000-0000-0000-0000-000000000002", Filename: "active.bin", TotalSize: 1, Status: domain.SessionStatusInitiated}
	if err := sessions.CreateSession(ctx, session, []domain.SessionBlock{{BlockHash: "active-hash", SequenceNumber: 0, SizeBytes: 1}}); err != nil {
		t.Fatal(err)
	}
	var createdAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM upload_sessions WHERE id = $1`, session.ID).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := sessions.MarkBlockUploaded(ctx, session.ID, 0); err != nil {
		t.Fatal(err)
	}
	var progressedAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM upload_sessions WHERE id = $1`, session.ID).Scan(&progressedAt); err != nil {
		t.Fatal(err)
	}
	if !progressedAt.After(createdAt) {
		t.Fatalf("upload progress did not refresh session activity: before=%s after=%s", createdAt, progressedAt)
	}
	blockEligible, err := repo.WithBlockCandidate(ctx, "active-hash", false, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if blockEligible {
		t.Fatal("GC marked a block referenced by an active upload session as eligible")
	}
	stagingEligible, err := repo.WithStagingCandidate(ctx, session.ID, false, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if stagingEligible {
		t.Fatal("GC marked active-session staging as eligible")
	}

	if err := sessions.UpdateSessionStatus(ctx, session.ID, domain.SessionStatusCompleted); err != nil {
		t.Fatal(err)
	}
	stagingEligible, err = repo.WithStagingCandidate(ctx, session.ID, false, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !stagingEligible {
		t.Fatal("GC should allow staging cleanup after session completion")
	}

	stale := &domain.UploadSession{UserID: "00000000-0000-0000-0000-000000000005", Filename: "stale.bin", TotalSize: 1, Status: domain.SessionStatusInitiated}
	staleHash := "stale-session-hash"
	if err := sessions.CreateSession(ctx, stale, []domain.SessionBlock{{BlockHash: staleHash, SequenceNumber: 0, SizeBytes: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE upload_sessions DISABLE TRIGGER trg_touch_upload_session`); err != nil {
		t.Fatal(err)
	}
	_, ageErr := db.ExecContext(ctx, `UPDATE upload_sessions SET updated_at = CURRENT_TIMESTAMP - INTERVAL '2 days' WHERE id = $1`, stale.ID)
	_, enableErr := db.ExecContext(ctx, `ALTER TABLE upload_sessions ENABLE TRIGGER trg_touch_upload_session`)
	if ageErr != nil || enableErr != nil {
		t.Fatalf("age stale upload session: update=%v enable trigger=%v", ageErr, enableErr)
	}
	found, err := repo.ExpireStaleUploadSessions(ctx, true)
	if err != nil || found != 1 {
		t.Fatalf("dry-run stale sessions = %d, err %v; want exactly 1", found, err)
	}
	var staleStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM upload_sessions WHERE id = $1`, stale.ID).Scan(&staleStatus); err != nil {
		t.Fatal(err)
	}
	if staleStatus != domain.SessionStatusInitiated {
		t.Fatalf("dry-run changed stale session status to %q", staleStatus)
	}
	expired, err := repo.ExpireStaleUploadSessions(ctx, false)
	if err != nil || expired != 1 {
		t.Fatalf("expired stale sessions = %d, err %v; want exactly 1", expired, err)
	}
	blockEligible, err = repo.WithBlockCandidate(ctx, staleHash, false, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !blockEligible {
		t.Fatal("GC should expire and collect a stale session block")
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM upload_sessions WHERE id = $1`, stale.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != domain.SessionStatusAborted {
		t.Fatalf("stale session status = %q, want ABORTED", status)
	}
	if err := sessions.MarkBlockUploaded(ctx, stale.ID, 0); err == nil {
		t.Fatal("aborted session accepted a late block-upload mark")
	}
	if err := sessions.UpdateSessionStatus(ctx, stale.ID, domain.SessionStatusCompleted); err == nil {
		t.Fatal("aborted session was allowed to complete")
	}
}

func TestGCPostgresAndLocalStorageSafetyBoundaries(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)
	ctx := context.Background()
	storageRoot := t.TempDir()
	store, err := storage.NewLocalStore(storageRoot, "http://localhost", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	blocks := postgresrepo.NewBlockRepository(db)
	known := &domain.Block{SHA256: strings.Repeat("a", 64), SizeBytes: 1}
	if err := blocks.Create(ctx, known); err != nil {
		t.Fatal(err)
	}
	owner := &domain.User{Email: "gc-owner@example.test", PasswordHash: "test", IsVerified: true}
	if err := postgresrepo.NewUserRepository(db).Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	file := &domain.File{UserID: owner.ID, Name: "referenced.bin", SizeBytes: 1, Status: "ACTIVE"}
	if err := postgresrepo.NewFileRepository(db).Create(ctx, file); err != nil {
		t.Fatal(err)
	}
	if err := blocks.LinkBlocksToFile(ctx, file.ID, []domain.BlockSequence{{BlockID: known.ID, SequenceNumber: 0}}); err != nil {
		t.Fatal(err)
	}
	active := &domain.UploadSession{UserID: "00000000-0000-0000-0000-000000000003", Filename: "active.bin", TotalSize: 1, Status: domain.SessionStatusInitiated}
	sessions := postgresrepo.NewUploadSessionRepository(db)
	activeHash := strings.Repeat("b", 64)
	if err := sessions.CreateSession(ctx, active, []domain.SessionBlock{{BlockHash: activeHash, SequenceNumber: 0, SizeBytes: 1}}); err != nil {
		t.Fatal(err)
	}
	terminal := &domain.UploadSession{UserID: "00000000-0000-0000-0000-000000000004", Filename: "done.bin", TotalSize: 1, Status: domain.SessionStatusInitiated}
	if err := sessions.CreateSession(ctx, terminal, []domain.SessionBlock{{BlockHash: "terminal-hash", SequenceNumber: 0, SizeBytes: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.UpdateSessionStatus(ctx, terminal.ID, domain.SessionStatusCompleted); err != nil {
		t.Fatal(err)
	}

	keys := []string{
		"blocks/" + known.SHA256,
		"blocks/" + activeHash,
		"blocks/" + strings.Repeat("c", 64), // old orphan
		"blocks/" + strings.Repeat("d", 64), // young orphan
		"staging/" + active.ID + "/0",
		"staging/" + terminal.ID + "/0",
	}
	orphan := &domain.Block{SHA256: strings.Repeat("c", 64), SizeBytes: 1}
	if err := blocks.Create(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if err := store.PutObject(ctx, key, strings.NewReader("x"), 1, "application/octet-stream"); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, key := range []string{keys[2], keys[4], keys[5]} {
		if err := os.Chtimes(filepath.Join(storageRoot, filepath.FromSlash(key)), old, old); err != nil {
			t.Fatalf("age %s: %v", key, err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dryCollector := gc.New(store, blocks, store, gc.Config{DryRun: true, MinBlockAge: time.Hour}, logger)
	dryResult, err := dryCollector.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dryResult.OrphansFound != 1 || dryResult.StagingFound != 1 {
		t.Fatalf("unexpected dry-run candidates: %+v", dryResult)
	}
	var orphanMetadataExists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM blocks WHERE sha256 = $1)`, orphan.SHA256).Scan(&orphanMetadataExists); err != nil {
		t.Fatal(err)
	}
	if !orphanMetadataExists {
		t.Fatal("dry-run removed orphan block metadata")
	}
	if _, err := store.HeadObject(ctx, keys[2]); err != nil {
		t.Fatalf("dry-run removed orphan object: %v", err)
	}
	collector := gc.New(store, blocks, store, gc.Config{MinBlockAge: time.Hour}, logger)
	result, err := collector.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.OrphansFound != 1 || result.OrphansDeleted != 1 || result.StagingFound != 1 || result.StagingDeleted != 1 {
		t.Fatalf("unexpected GC result: %+v", result)
	}
	for _, key := range []string{keys[0], keys[1], keys[3], keys[4]} {
		if _, err := store.HeadObject(ctx, key); err != nil {
			t.Fatalf("GC removed protected object %s: %v", key, err)
		}
	}
	for _, key := range []string{keys[2], keys[5]} {
		if _, err := store.HeadObject(ctx, key); err == nil {
			t.Fatalf("GC left eligible object %s", key)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM blocks WHERE sha256 = $1)`, orphan.SHA256).Scan(&orphanMetadataExists); err != nil {
		t.Fatal(err)
	}
	if orphanMetadataExists {
		t.Fatal("live GC left metadata for the deleted orphan block")
	}
}

func TestGCDeleteFailureRollsBackBlockMetadataRemoval(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	freshSchema(t, db)
	ctx := context.Background()
	repo := postgresrepo.NewBlockRepository(db)
	block := &domain.Block{SHA256: strings.Repeat("e", 64), SizeBytes: 1}
	if err := repo.Create(ctx, block); err != nil {
		t.Fatal(err)
	}
	eligible, err := repo.WithBlockCandidate(ctx, block.SHA256, false, func() error { return os.ErrPermission })
	if err == nil || !eligible {
		t.Fatalf("candidate = eligible %v, err %v; want eligible with delete error", eligible, err)
	}
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM blocks WHERE sha256 = $1)`, block.SHA256).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("failed storage deletion committed block metadata removal")
	}
}
