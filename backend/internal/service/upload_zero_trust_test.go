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
