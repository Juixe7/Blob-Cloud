package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/service"
	wsSync "go-drive-clone/internal/sync"
)

func TestZeroTrust_CASPoisoningRejected(t *testing.T) {
	db := openE2EDB(t)
	defer db.Close()
	freshE2ESchema(t, db)

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

	// Create test user
	user := &domain.User{
		Email:        fmt.Sprintf("zero-trust-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		IsVerified:   true,
	}
	if err := users.Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Legitimate claimed block data and hash
	genuineData := []byte("legitimate genuine document content")
	genuineSum := sha256.Sum256(genuineData)
	claimedHash := hex.EncodeToString(genuineSum[:])

	// Tampered data that an attacker actually attempts to upload
	tamperedData := []byte("malicious poisoned corrupted content")

	// 1. Client initiates declaring claimedHash
	initReq := service.InitiateRequest{
		UserID:    user.ID,
		Filename:  "contract.pdf",
		TotalSize: int64(len(genuineData)),
		Chunks: []service.InitiateChunk{
			{
				SHA256:    claimedHash,
				SizeBytes: int32(len(genuineData)),
			},
		},
	}
	initResp, err := svc.Initiate(ctx, initReq)
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}

	// 2. Attacker puts tampered bytes into the staging URL
	stagingKey := fmt.Sprintf("staging/%s/0", initResp.SessionID)
	// Sizing matches len(genuineData) but content is poisoned
	poisonedPayload := append(tamperedData[:len(genuineData)-1], '!')
	if err := stor.PutObject(ctx, stagingKey, bytes.NewReader(poisonedPayload), int64(len(poisonedPayload)), "application/octet-stream"); err != nil {
		t.Fatalf("put tampered object: %v", err)
	}

	// 3. Client calls Complete
	_, err = svc.Complete(ctx, service.CompleteRequest{SessionID: initResp.SessionID}, user.ID)
	if err == nil {
		t.Fatal("expected Complete to reject tampered payload, but got nil error")
	}

	// Verify error explicitly cites checksum mismatch / CAS poisoning
	if !strings.Contains(err.Error(), "CAS poisoning rejected") {
		t.Errorf("expected error to mention 'CAS poisoning rejected', got: %v", err)
	}

	// 4. Verify CAS store was NOT contaminated
	casKey := "blocks/" + claimedHash
	if _, err := stor.HeadObject(ctx, casKey); err == nil {
		t.Fatalf("CRITICAL SECURITY VIOLATION: %s was created in CAS with tampered content!", casKey)
	}

	// 5. Verify the poisoned staging object was purged
	if _, err := stor.HeadObject(ctx, stagingKey); err == nil {
		t.Errorf("poisoned staging object %s was not purged after rejection", stagingKey)
	}
}

// TestGetSession_StagingReconciliation verifies that GetSession correctly
// identifies chunks already present in S3 staging after a network interruption.
//
// Scenario: A 3-chunk upload where chunks 0 and 1 successfully reach S3 staging
// before the connection drops. On reconnect, the client calls GET /session/:id.
// The backend must recognise those two chunks as already staged (AlreadyExists=true,
// no UploadURL) and only issue a fresh URL for the genuinely missing chunk 2.
// This prevents wasteful re-uploading of chunks that already cleared the public WAN.
func TestGetSession_StagingReconciliation(t *testing.T) {
	db := openE2EDB(t)
	defer db.Close()
	freshE2ESchema(t, db)

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

	user := &domain.User{
		Email:        fmt.Sprintf("resume-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		IsVerified:   true,
	}
	if err := users.Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Build 3 distinct chunks.
	type chunkDef struct {
		data []byte
		hash string
		size int32
	}
	makeChunk := func(content string) chunkDef {
		d := []byte(content)
		sum := sha256.Sum256(d)
		return chunkDef{data: d, hash: hex.EncodeToString(sum[:]), size: int32(len(d))}
	}
	chunk0 := makeChunk("chunk-zero-data-payload-alpha")
	chunk1 := makeChunk("chunk-one-data-payload-beta")
	chunk2 := makeChunk("chunk-two-data-payload-gamma")

	// Initiate upload session with all 3 chunks.
	initResp, err := svc.Initiate(ctx, service.InitiateRequest{
		UserID:    user.ID,
		Filename:  "large-video.mp4",
		TotalSize: int64(chunk0.size + chunk1.size + chunk2.size),
		Chunks: []service.InitiateChunk{
			{SHA256: chunk0.hash, SizeBytes: chunk0.size},
			{SHA256: chunk1.hash, SizeBytes: chunk1.size},
			{SHA256: chunk2.hash, SizeBytes: chunk2.size},
		},
	})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	sessionID := initResp.SessionID

	// Simulate: chunks 0 and 1 successfully uploaded to S3 staging before disconnect.
	// Chunk 2 never arrived (network dropped).
	for i, chunk := range []chunkDef{chunk0, chunk1} {
		key := fmt.Sprintf("staging/%s/%d", sessionID, i)
		if err := stor.PutObject(ctx, key, bytes.NewReader(chunk.data), int64(chunk.size), "application/octet-stream"); err != nil {
			t.Fatalf("put chunk %d: %v", i, err)
		}
	}

	// Call GetSession (simulates reconnect — client asks: what do I still need to upload?).
	statusResp, err := svc.GetSession(ctx, sessionID, user.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(statusResp.Chunks) != 3 {
		t.Fatalf("expected 3 chunks in response, got %d", len(statusResp.Chunks))
	}

	// Validate per-chunk reconciliation results.
	for _, rc := range statusResp.Chunks {
		switch rc.SequenceNumber {
		case 0, 1:
			// These chunks are already in S3 staging — must not be re-uploaded.
			if !rc.AlreadyExists {
				t.Errorf("chunk %d: expected AlreadyExists=true (already staged), got false", rc.SequenceNumber)
			}
			if rc.UploadURL != "" {
				t.Errorf("chunk %d: expected no UploadURL for already-staged chunk, got %q", rc.SequenceNumber, rc.UploadURL)
			}
		case 2:
			// This chunk never made it to staging — must receive a fresh presigned URL.
			if rc.AlreadyExists {
				t.Errorf("chunk 2: expected AlreadyExists=false (not yet staged), got true")
			}
			if rc.UploadURL == "" {
				t.Errorf("chunk 2: expected a fresh UploadURL for missing chunk, got empty string")
			}
		default:
			t.Errorf("unexpected sequence_number %d in response", rc.SequenceNumber)
		}
	}
}
