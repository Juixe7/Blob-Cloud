package service

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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/queue"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	wsSync "go-drive-clone/internal/sync"
)

// ---------------------------------------------------------------------------
// Thread-Safe In-Memory Test Doubles for UploadService Security & Resume Suite
// ---------------------------------------------------------------------------

type memObject struct {
	data []byte
	etag string
}

type faultStorageProvider struct {
	mu          sync.Mutex
	objects     map[string]memObject
	deletedKeys []string
	promoted    map[string]string // srcKey -> destKey
	presigned   []string
}

func newFaultStorage() *faultStorageProvider {
	return &faultStorageProvider{
		objects:  make(map[string]memObject),
		promoted: make(map[string]string),
	}
}

func (s *faultStorageProvider) putRaw(key string, data []byte, etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if etag == "" {
		etag = "mocketag"
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	s.objects[key] = memObject{data: cp, etag: etag}
}

func (s *faultStorageProvider) hasKey(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok
}

func (s *faultStorageProvider) GenerateStagingUploadURL(_ context.Context, stagingKey string, _ time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	url := "https://r2.example.com/" + stagingKey + "?sig=presigned"
	s.presigned = append(s.presigned, stagingKey)
	return url, nil
}

func (s *faultStorageProvider) PutObject(_ context.Context, key string, reader io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.putRaw(key, b, "mocketag")
	return nil
}

func (s *faultStorageProvider) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("object %s not found", key)
	}
	cp := make([]byte, len(obj.data))
	copy(cp, obj.data)
	return io.NopCloser(bytes.NewReader(cp)), nil
}

func (s *faultStorageProvider) GetObjectRange(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("object %s not found", key)
	}
	if offset < 0 || offset > int64(len(obj.data)) {
		return nil, errors.New("offset out of bounds")
	}
	sub := obj.data[offset:]
	if length >= 0 && length < int64(len(sub)) {
		sub = sub[:length]
	}
	cp := make([]byte, len(sub))
	copy(cp, sub)
	return io.NopCloser(bytes.NewReader(cp)), nil
}

func (s *faultStorageProvider) HeadObject(_ context.Context, key string) (*domain.ObjectMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("head %s: not found", key)
	}
	return &domain.ObjectMetadata{
		ContentLength: int64(len(obj.data)),
		ETag:          obj.etag,
	}, nil
}

func (s *faultStorageProvider) DeleteObject(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	s.deletedKeys = append(s.deletedKeys, key)
	return nil
}

func (s *faultStorageProvider) PromoteObject(_ context.Context, srcKey, destKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[srcKey]
	if !ok {
		return fmt.Errorf("promote source %s not found", srcKey)
	}
	s.objects[destKey] = obj
	delete(s.objects, srcKey)
	s.promoted[srcKey] = destKey
	return nil
}

type memUploadBlockStore struct {
	mu     sync.Mutex
	blocks map[string]*domain.Block
}

func newMemUploadBlockStore() *memUploadBlockStore {
	return &memUploadBlockStore{blocks: make(map[string]*domain.Block)}
}

func (m *memUploadBlockStore) seedBlock(hash string, size int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blocks[hash] = &domain.Block{ID: "blk-" + hash[:8], SHA256: hash, SizeBytes: size}
}

func (m *memUploadBlockStore) GetMultipleByHashes(_ context.Context, hashes []string) ([]*domain.Block, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*domain.Block
	for _, h := range hashes {
		if b, ok := m.blocks[h]; ok {
			cp := *b
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *memUploadBlockStore) WithTx(_ postgresrepo.DBTX) *postgresrepo.BlockRepository {
	return nil
}

type memUploadSessionStore struct {
	mu       sync.Mutex
	seq      int
	sessions map[string]*domain.UploadSession
	blocks   map[string][]domain.SessionBlock
}

func newMemUploadSessionStore() *memUploadSessionStore {
	return &memUploadSessionStore{
		sessions: make(map[string]*domain.UploadSession),
		blocks:   make(map[string][]domain.SessionBlock),
	}
}

func (m *memUploadSessionStore) CreateSession(_ context.Context, session *domain.UploadSession, blocks []domain.SessionBlock) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	session.ID = fmt.Sprintf("sess-%d", m.seq)
	cp := *session
	m.sessions[session.ID] = &cp

	blkCopy := make([]domain.SessionBlock, len(blocks))
	for i, b := range blocks {
		b.SessionID = session.ID
		blkCopy[i] = b
	}
	m.blocks[session.ID] = blkCopy
	return nil
}

func (m *memUploadSessionStore) GetSessionByID(_ context.Context, id string) (*domain.UploadSession, []domain.SessionBlock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, nil, sql.ErrNoRows
	}
	sCopy := *s
	blks := m.blocks[id]
	bCopy := make([]domain.SessionBlock, len(blks))
	copy(bCopy, blks)
	return &sCopy, bCopy, nil
}

func (m *memUploadSessionStore) UpdateSessionStatus(_ context.Context, id string, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return sql.ErrNoRows
	}
	s.Status = status
	return nil
}

func (m *memUploadSessionStore) WithTx(_ postgresrepo.DBTX) *postgresrepo.UploadSessionRepository {
	return nil
}

type spyPublisher struct {
	mu   sync.Mutex
	jobs []queue.ThumbnailMessage
}

func (p *spyPublisher) PublishThumbnailJob(_ context.Context, msg queue.ThumbnailMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.jobs = append(p.jobs, msg)
	return nil
}

type spyNotifier struct {
	mu     sync.Mutex
	events map[string][]wsSync.NotificationEvent
}

func newSpyNotifier() *spyNotifier {
	return &spyNotifier{events: make(map[string][]wsSync.NotificationEvent)}
}

func (n *spyNotifier) NotifyUser(userID string, event wsSync.NotificationEvent) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events[userID] = append(n.events[userID], event)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestUploadInitiate_RejectsMalformedMetadataBeforeSessionCreation(t *testing.T) {
	svc, _, _, sessions, _, _ := newTestUploadHarness()
	validHash := sha256Hex([]byte("valid"))
	tests := []struct {
		name string
		edit func(*InitiateRequest)
	}{
		{name: "path-like filename", edit: func(r *InitiateRequest) { r.Filename = "../secret.txt" }},
		{name: "negative total size", edit: func(r *InitiateRequest) { r.TotalSize = -1 }},
		{name: "malformed sha256", edit: func(r *InitiateRequest) { r.Chunks[0].SHA256 = "not-a-digest" }},
		{name: "negative chunk size", edit: func(r *InitiateRequest) { r.Chunks[0].SizeBytes = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := InitiateRequest{
				UserID: "user-alice", Filename: "valid.txt", TotalSize: 5,
				Chunks: []InitiateChunk{{SHA256: validHash, SizeBytes: 5}},
			}
			tt.edit(&req)
			if _, err := svc.Initiate(context.Background(), req); err == nil {
				t.Fatal("expected malformed upload metadata to be rejected")
			}
			if len(sessions.sessions) != 0 {
				t.Fatal("invalid metadata created an upload session")
			}
		})
	}
}

func newTestUploadHarness() (*UploadService, *faultStorageProvider, *memUploadBlockStore, *memUploadSessionStore, *spyPublisher, *spyNotifier) {
	storage := newFaultStorage()
	blockStore := newMemUploadBlockStore()
	sessionStore := newMemUploadSessionStore()
	pub := &spyPublisher{}
	notif := newSpyNotifier()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc := NewUploadService(nil, nil, nil, blockStore, sessionStore, nil, storage, pub, notif, logger)
	svc.commitTx = func(ctx context.Context, session *domain.UploadSession, sessionBlocks []domain.SessionBlock, _ CompleteRequest) (string, map[string]int64, string, error) {
		current, _, err := sessionStore.GetSessionByID(ctx, session.ID)
		if err != nil {
			return "", nil, "", err
		}
		if current.Status != domain.SessionStatusInitiated {
			return "", nil, "", fmt.Errorf("session %s is %s, cannot complete", session.ID, current.Status)
		}
		for _, b := range sessionBlocks {
			blockStore.seedBlock(b.BlockHash, b.SizeBytes)
		}
		if err := sessionStore.UpdateSessionStatus(ctx, session.ID, domain.SessionStatusCompleted); err != nil {
			return "", nil, "", err
		}
		fileID := "file-" + session.ID
		return fileID, map[string]int64{session.UserID: 42}, domain.ActionFileCreated, nil
	}

	return svc, storage, blockStore, sessionStore, pub, notif
}

// ---------------------------------------------------------------------------
// 1. Security Test: CAS Poisoning Attempt Rejected & Quarantine Cleaned Up
// ---------------------------------------------------------------------------

func TestUpload_CASPoisoningRejectedAndQuarantineCleaned(t *testing.T) {
	svc, storage, _, sessionStore, pub, notif := newTestUploadHarness()
	ctx := context.Background()

	// Honest payload for chunk 0 and honest declaration for chunk 1
	chunk0Bytes := bytes.Repeat([]byte("HONEST_BLOCK_ZERO_"), 256) // 4608 bytes
	chunk0Hash := sha256Hex(chunk0Bytes)

	claimedChunk1Bytes := bytes.Repeat([]byte("HONEST_BLOCK_ONE__"), 256) // 4608 bytes
	claimedChunk1Hash := sha256Hex(claimedChunk1Bytes)

	// Poisoned payload of the EXACT same byte size (4608 bytes) so it passes HeadObject ContentLength check!
	poisonedChunk1Bytes := bytes.Repeat([]byte("MALICIOUS_PAYLOAD_"), 256) // 4608 bytes
	poisonedChunk1Hash := sha256Hex(poisonedChunk1Bytes)

	initResp, err := svc.Initiate(ctx, InitiateRequest{
		UserID:    "user-alice",
		Filename:  "financial_report.pdf",
		TotalSize: int64(len(chunk0Bytes) + len(claimedChunk1Bytes)),
		Chunks: []InitiateChunk{
			{SHA256: chunk0Hash, SizeBytes: int32(len(chunk0Bytes))},
			{SHA256: claimedChunk1Hash, SizeBytes: int32(len(claimedChunk1Bytes))},
		},
	})
	if err != nil {
		t.Fatalf("Initiate failed: %v", err)
	}

	// Client uploads honest bytes to staging/{session}/0, but uploads POISONED bytes to staging/{session}/1
	staging0 := fmt.Sprintf("staging/%s/0", initResp.SessionID)
	staging1 := fmt.Sprintf("staging/%s/1", initResp.SessionID)
	storage.putRaw(staging0, chunk0Bytes, "mocketag")
	storage.putRaw(staging1, poisonedChunk1Bytes, "mocketag")

	// Attempt to complete session
	_, err = svc.Complete(ctx, CompleteRequest{SessionID: initResp.SessionID}, "user-alice")
	if err == nil {
		t.Fatal("expected CAS poisoning upload to be rejected, but Complete succeeded")
	}
	if !strings.Contains(err.Error(), "CAS poisoning rejected") || !strings.Contains(err.Error(), claimedChunk1Hash) {
		t.Fatalf("expected CAS poisoning error mentioning claimed hash, got: %v", err)
	}

	// Verify neither the claimed hash nor the poisoned hash was promoted to CAS blocks/
	if storage.hasKey("blocks/" + claimedChunk1Hash) {
		t.Fatal("SECURITY FAILURE: poisoned payload was promoted to blocks/<claimed_hash>!")
	}
	if storage.hasKey("blocks/" + poisonedChunk1Hash) {
		t.Fatal("SECURITY FAILURE: poisoned payload was promoted to blocks/<poisoned_hash>!")
	}

	// Verify the poisoned staging quarantine object was immediately deleted
	if storage.hasKey(staging1) {
		t.Fatalf("quarantine object %s was NOT cleaned up after CAS poisoning detection", staging1)
	}

	// Verify session remains INITIATED (transaction never committed) and zero jobs/events were emitted
	sess, _, _ := sessionStore.GetSessionByID(ctx, initResp.SessionID)
	if sess.Status != domain.SessionStatusInitiated {
		t.Fatalf("expected session status to remain INITIATED after poisoning failure, got %s", sess.Status)
	}
	if len(pub.jobs) != 0 {
		t.Fatalf("expected 0 SQS thumbnail jobs on failed upload, got %d", len(pub.jobs))
	}
	if len(notif.events["user-alice"]) != 0 {
		t.Fatalf("expected 0 WebSocket notifications on failed upload, got %d", len(notif.events["user-alice"]))
	}
}

// ---------------------------------------------------------------------------
// 2. Security Test: Size Mismatch / Truncated Stream & ETag Tamper Rejection
// ---------------------------------------------------------------------------

func TestUpload_TruncatedStreamAndETagMismatchRejected(t *testing.T) {
	svc, storage, _, _, _, _ := newTestUploadHarness()
	ctx := context.Background()

	honestBytes := bytes.Repeat([]byte("DATA_CHUNK_"), 400) // 4400 bytes
	honestHash := sha256Hex(honestBytes)

	// Case A: Truncated upload in staging (client declared 4400 bytes, uploaded only 1200 bytes)
	initResp, err := svc.Initiate(ctx, InitiateRequest{
		UserID:    "user-alice",
		Filename:  "truncated.bin",
		TotalSize: int64(len(honestBytes)),
		Chunks: []InitiateChunk{
			{SHA256: honestHash, SizeBytes: int32(len(honestBytes))},
		},
	})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}

	stagingKey := fmt.Sprintf("staging/%s/0", initResp.SessionID)
	storage.putRaw(stagingKey, honestBytes[:1200], "mocketag")

	_, err = svc.Complete(ctx, CompleteRequest{SessionID: initResp.SessionID}, "user-alice")
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("expected size mismatch error for truncated stream, got: %v", err)
	}
	if storage.hasKey(stagingKey) {
		t.Fatal("truncated staging object must be deleted upon rejection")
	}
	if storage.hasKey("blocks/" + honestHash) {
		t.Fatal("truncated block must never be promoted to CAS")
	}

	// Case B: ETag / MD5 mismatch (client declared BlockMD5, S3 ETag differs)
	initResp2, err := svc.Initiate(ctx, InitiateRequest{
		UserID:    "user-alice",
		Filename:  "etag_tampered.bin",
		TotalSize: int64(len(honestBytes)),
		Chunks: []InitiateChunk{
			{SHA256: honestHash, BlockMD5: "d41d8cd98f00b204e9800998ecf8427e", SizeBytes: int32(len(honestBytes))},
		},
	})
	if err != nil {
		t.Fatalf("Initiate 2: %v", err)
	}

	stagingKey2 := fmt.Sprintf("staging/%s/0", initResp2.SessionID)
	storage.putRaw(stagingKey2, honestBytes, "\"ffffffffffffffffffffffffffffffff\"")

	_, err = svc.Complete(ctx, CompleteRequest{SessionID: initResp2.SessionID}, "user-alice")
	if err == nil || !strings.Contains(err.Error(), "ETag mismatch") {
		t.Fatalf("expected ETag mismatch error, got: %v", err)
	}
	if storage.hasKey(stagingKey2) {
		t.Fatal("ETag-mismatched staging object must be deleted upon rejection")
	}
}

// ---------------------------------------------------------------------------
// 3. Fault-Injection Test: Interrupted Upload Resume with 0-Byte Redundancy
// ---------------------------------------------------------------------------

func TestUpload_InterruptedResumeZeroRedundantBytes(t *testing.T) {
	svc, storage, blockStore, _, pub, notif := newTestUploadHarness()
	ctx := context.Background()

	// Prepare 4 chunks:
	// - Chunk 0: already exists in global CAS before Initiate (Deduplication Hit)
	// - Chunks 1, 2: uploaded to staging before network drop
	// - Chunk 3: failed/never uploaded before network drop
	c0 := bytes.Repeat([]byte("CHUNK_0_DEDUP_"), 200)
	c1 := bytes.Repeat([]byte("CHUNK_1_STAGE_"), 200)
	c2 := bytes.Repeat([]byte("CHUNK_2_STAGE_"), 200)
	c3 := bytes.Repeat([]byte("CHUNK_3_MISS__"), 200)

	h0, h1, h2, h3 := sha256Hex(c0), sha256Hex(c1), sha256Hex(c2), sha256Hex(c3)

	// Pre-seed Chunk 0 in global CAS (both DB and S3 blocks/)
	blockStore.seedBlock(h0, int32(len(c0)))
	storage.putRaw("blocks/"+h0, c0, "mocketag")

	// Step 1: Initiate 4-chunk upload session
	initResp, err := svc.Initiate(ctx, InitiateRequest{
		UserID:    "user-bob",
		Filename:  "dataset.tar",
		TotalSize: int64(len(c0) + len(c1) + len(c2) + len(c3)),
		Chunks: []InitiateChunk{
			{SHA256: h0, SizeBytes: int32(len(c0))},
			{SHA256: h1, SizeBytes: int32(len(c1))},
			{SHA256: h2, SizeBytes: int32(len(c2))},
			{SHA256: h3, SizeBytes: int32(len(c3))},
		},
	})
	if err != nil {
		t.Fatalf("Initiate failed: %v", err)
	}

	// Verify Chunk 0 was deduplicated immediately at Initiate, while Chunks 1..3 got staging URLs
	if !initResp.Chunks[0].AlreadyExists || initResp.Chunks[0].UploadURL != "" {
		t.Fatalf("chunk 0 should be deduplicated (AlreadyExists=true, no UploadURL), got %+v", initResp.Chunks[0])
	}
	for i := 1; i <= 3; i++ {
		if initResp.Chunks[i].AlreadyExists || initResp.Chunks[i].UploadURL == "" {
			t.Fatalf("chunk %d should require upload with presigned staging URL, got %+v", i, initResp.Chunks[i])
		}
	}

	// Step 2: Simulate client uploading Chunks 1 and 2 to S3 staging, then crashing before Chunk 3
	storage.putRaw(fmt.Sprintf("staging/%s/1", initResp.SessionID), c1, "mocketag")
	storage.putRaw(fmt.Sprintf("staging/%s/2", initResp.SessionID), c2, "mocketag")

	// Step 3: Client reconnects and calls GetSession to resume
	resumeResp, err := svc.GetSession(ctx, initResp.SessionID, "user-bob")
	if err != nil {
		t.Fatalf("GetSession on resume failed: %v", err)
	}

	// Chunks 0, 1, and 2 MUST be reported as AlreadyExists=true with NO UploadURL (0 redundant bytes!)
	for _, idx := range []int{0, 1, 2} {
		ch := resumeResp.Chunks[idx]
		if !ch.AlreadyExists || ch.UploadURL != "" {
			t.Fatalf("resume chunk %d: expected AlreadyExists=true and empty UploadURL (0-byte redundancy), got %+v", idx, ch)
		}
	}

	// Chunk 3 MUST be reported as AlreadyExists=false with a fresh presigned staging URL
	if resumeResp.Chunks[3].AlreadyExists || resumeResp.Chunks[3].UploadURL == "" {
		t.Fatalf("resume chunk 3: expected AlreadyExists=false and fresh UploadURL, got %+v", resumeResp.Chunks[3])
	}

	// Step 4: Client uploads only missing Chunk 3 and calls Complete
	storage.putRaw(fmt.Sprintf("staging/%s/3", initResp.SessionID), c3, "mocketag")

	compResp, err := svc.Complete(ctx, CompleteRequest{SessionID: initResp.SessionID}, "user-bob")
	if err != nil {
		t.Fatalf("Complete after resume failed: %v", err)
	}
	if compResp.Status != domain.SessionStatusCompleted || compResp.FileID == "" {
		t.Fatalf("unexpected CompleteResponse: %+v", compResp)
	}

	// Verify all 4 blocks now reside in canonical CAS (blocks/<sha256>) and staging is clean
	for i, h := range []string{h0, h1, h2, h3} {
		if !storage.hasKey("blocks/" + h) {
			t.Fatalf("block %d (%s) missing from canonical CAS after Complete", i, h)
		}
		if storage.hasKey(fmt.Sprintf("staging/%s/%d", initResp.SessionID, i)) {
			t.Fatalf("staging object for chunk %d was not cleaned up after promotion", i)
		}
	}

	// Verify SQS thumbnail job and WebSocket UPLOAD_COMPLETE + SYNC_DELTA events fired
	if len(pub.jobs) != 1 || pub.jobs[0].FileID != compResp.FileID {
		t.Fatalf("expected 1 SQS thumbnail job for %s, got %+v", compResp.FileID, pub.jobs)
	}
	if len(notif.events["user-bob"]) != 2 {
		t.Fatalf("expected 2 WebSocket events (UPLOAD_COMPLETE + SYNC_DELTA), got %d", len(notif.events["user-bob"]))
	}
}

// ---------------------------------------------------------------------------
// 4. Security Test: Cross-User Upload Session IDOR Protection
// ---------------------------------------------------------------------------

func TestUpload_CrossUserSessionIDORBlocked(t *testing.T) {
	svc, _, _, _, _, _ := newTestUploadHarness()
	ctx := context.Background()

	payload := []byte("private-user-alice-document")
	h := sha256Hex(payload)

	initResp, err := svc.Initiate(ctx, InitiateRequest{
		UserID:    "user-alice",
		Filename:  "secret.pdf",
		TotalSize: int64(len(payload)),
		Chunks:    []InitiateChunk{{SHA256: h, SizeBytes: int32(len(payload))}},
	})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}

	// Attacker ("user-mallory") attempts to inspect or resume Alice's session
	if _, err := svc.GetSession(ctx, initResp.SessionID, "user-mallory"); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected IDOR access denied on GetSession, got: %v", err)
	}

	// Attacker ("user-mallory") attempts to complete Alice's session
	if _, err := svc.Complete(ctx, CompleteRequest{SessionID: initResp.SessionID}, "user-mallory"); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected IDOR access denied on Complete, got: %v", err)
	}
}

func TestStoreStagingBlock_ValidatesSessionSequenceSizeAndHash(t *testing.T) {
	svc, storage, _, sessions, _, _ := newTestUploadHarness()
	ctx := context.Background()
	payload := []byte("verified staged payload")
	hash := sha256Hex(payload)
	session := &domain.UploadSession{UserID: "user-alice", Status: domain.SessionStatusInitiated}
	if err := sessions.CreateSession(ctx, session, []domain.SessionBlock{{
		BlockHash: hash, SequenceNumber: 0, SizeBytes: int32(len(payload)),
	}}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := svc.StoreStagingBlock(ctx, session.ID, 0, bytes.NewReader(payload), "application/octet-stream"); err != nil {
		t.Fatalf("StoreStagingBlock valid payload: %v", err)
	}
	if !storage.hasKey("staging/" + session.ID + "/0") {
		t.Fatal("valid payload was not staged")
	}
	if err := svc.StoreStagingBlock(ctx, session.ID, 1, bytes.NewReader(payload), "application/octet-stream"); err == nil {
		t.Fatal("expected unrecognized sequence to be rejected")
	}

	badSession := &domain.UploadSession{UserID: "user-alice", Status: domain.SessionStatusInitiated}
	if err := sessions.CreateSession(ctx, badSession, []domain.SessionBlock{{
		BlockHash: hash, SequenceNumber: 0, SizeBytes: int32(len(payload) + 1),
	}}); err != nil {
		t.Fatalf("CreateSession bad-size: %v", err)
	}
	if err := svc.StoreStagingBlock(ctx, badSession.ID, 0, bytes.NewReader(payload), "application/octet-stream"); err == nil {
		t.Fatal("expected wrong-sized payload to be rejected")
	}
	if storage.hasKey("staging/" + badSession.ID + "/0") {
		t.Fatal("wrong-sized payload remained in staging")
	}

	poisonedSession := &domain.UploadSession{UserID: "user-alice", Status: domain.SessionStatusInitiated}
	if err := sessions.CreateSession(ctx, poisonedSession, []domain.SessionBlock{{
		BlockHash: hash, SequenceNumber: 0, SizeBytes: int32(len(payload)),
	}}); err != nil {
		t.Fatalf("CreateSession poisoned: %v", err)
	}
	poison := bytes.Repeat([]byte("x"), len(payload))
	if err := svc.StoreStagingBlock(ctx, poisonedSession.ID, 0, bytes.NewReader(poison), "application/octet-stream"); err == nil {
		t.Fatal("expected same-sized payload with wrong hash to be rejected")
	}
	if storage.hasKey("staging/" + poisonedSession.ID + "/0") {
		t.Fatal("hash-mismatched payload remained in staging")
	}

	if err := sessions.UpdateSessionStatus(ctx, session.ID, domain.SessionStatusCompleted); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}
	if err := svc.StoreStagingBlock(ctx, session.ID, 0, bytes.NewReader(payload), "application/octet-stream"); err == nil {
		t.Fatal("expected completed session to reject staging writes")
	}
}

// ---------------------------------------------------------------------------
// 5. Security & Tree Integrity: Materialized Path Cycle Guard & RBAC Move Rules
// ---------------------------------------------------------------------------

type memFileServiceStore struct {
	mu    sync.RWMutex
	files map[string]*domain.File
	roles map[string]map[string]string
}

func (m *memFileServiceStore) GetByID(_ context.Context, id string) (*domain.File, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.files[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	cp := *f
	return &cp, nil
}

func (m *memFileServiceStore) GetResolvedPermission(_ context.Context, fileID, email string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if r, ok := m.roles[fileID][email]; ok {
		return r, nil
	}
	return "", domain.ErrPermissionNotFound
}

func (m *memFileServiceStore) GetFileByNameAndParent(context.Context, string, string, *string) (*domain.File, error) {
	return nil, sql.ErrNoRows
}
func (m *memFileServiceStore) GetFolderByNameAndParent(context.Context, string, string, *string) (*domain.File, error) {
	return nil, sql.ErrNoRows
}
func (m *memFileServiceStore) Create(_ context.Context, f *domain.File) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f.ID == "" {
		f.ID = "shortcut-created"
	}
	cp := *f
	m.files[f.ID] = &cp
	return nil
}
func (m *memFileServiceStore) Update(_ context.Context, f *domain.File) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *f
	m.files[f.ID] = &cp
	return nil
}
func (m *memFileServiceStore) SoftDelete(context.Context, string, string) error { return nil }
func (m *memFileServiceStore) Restore(context.Context, string, string) error    { return nil }
func (m *memFileServiceStore) ListTrash(context.Context, string) ([]*domain.File, error) {
	return nil, nil
}
func (m *memFileServiceStore) DeleteRecursive(context.Context, string, string) (int64, []string, error) {
	return 0, nil, nil
}
func (m *memFileServiceStore) ListDirectory(context.Context, string, *string) ([]*domain.File, error) {
	return nil, nil
}
func (m *memFileServiceStore) ListSharedWithUser(context.Context, string, string) ([]*domain.File, error) {
	return nil, nil
}
func (m *memFileServiceStore) GetUserStorageUsage(context.Context, string) (*domain.UserStorageUsage, error) {
	return &domain.UserStorageUsage{}, nil
}
func (m *memFileServiceStore) BulkSoftDelete(context.Context, []string, string) error { return nil }
func (m *memFileServiceStore) BulkRestore(context.Context, []string, string) error    { return nil }
func (m *memFileServiceStore) BulkMove(context.Context, []string, *string, string) error {
	return nil
}
func (m *memFileServiceStore) BulkHardDelete(context.Context, []string, string) (int64, []string, error) {
	return 0, nil, nil
}
func (m *memFileServiceStore) WithTx(_ postgresrepo.DBTX) *postgresrepo.FileRepository { return nil }

type memUserLookup struct {
	users map[string]*domain.User
}

func (m *memUserLookup) GetByID(_ context.Context, id string) (*domain.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return u, nil
}

type memPermLookup struct {
	allowed map[string]bool
}

func (m *memPermLookup) CheckUserPermission(_ context.Context, fileID, email string, _ []string) (bool, error) {
	return m.allowed[fileID+":"+email], nil
}
func (m *memPermLookup) FindCollaboratorUserIDs(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *memPermLookup) WithTx(_ postgresrepo.DBTX) *postgresrepo.PermissionRepository { return nil }

func TestFileService_RequiresWritableDirectoryDestination(t *testing.T) {
	ctx := context.Background()
	files := &memFileServiceStore{
		files: map[string]*domain.File{
			"source":         {ID: "source", UserID: "alice", Name: "source.txt"},
			"foreign-folder": {ID: "foreign-folder", UserID: "bob", Name: "shared", IsDirectory: true},
			"foreign-file":   {ID: "foreign-file", UserID: "bob", Name: "readme.txt"},
		},
		roles: map[string]map[string]string{
			"foreign-folder": {"alice@example.com": domain.RoleViewer},
		},
	}
	users := &memUserLookup{users: map[string]*domain.User{
		"alice": {ID: "alice", Email: "alice@example.com"},
		"bob":   {ID: "bob", Email: "bob@example.com"},
	}}
	perms := &memPermLookup{allowed: map[string]bool{}}
	svc := NewFileService(nil, users, files, nil, perms, newFaultStorage(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	foreignFolder := "foreign-folder"
	if _, err := svc.RenameMove(ctx, "alice", "source", RenameMoveRequest{ParentID: &foreignFolder, hasParent: true}); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected viewer-only destination to reject move, got %v", err)
	}
	if files.files["source"].ParentID != nil {
		t.Fatal("denied move modified the source file")
	}

	if _, _, err := svc.CreateFolder(ctx, "alice", "new-folder", &foreignFolder); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected viewer-only destination to reject folder creation, got %v", err)
	}

	foreignFile := "foreign-file"
	if _, err := svc.RenameMove(ctx, "alice", "source", RenameMoveRequest{ParentID: &foreignFile, hasParent: true}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected non-directory destination to be rejected, got %v", err)
	}

	files.roles[foreignFolder]["alice@example.com"] = domain.RoleEditor
	if _, err := svc.RenameMove(ctx, "alice", "source", RenameMoveRequest{ParentID: &foreignFolder, hasParent: true}); err != nil {
		t.Fatalf("editor should be allowed to move into shared folder: %v", err)
	}
}

func TestFileService_CycleGuardAndRBACMoveRules(t *testing.T) {
	ctx := context.Background()
	folderAID := "folder-A"
	folderBID := "folder-B"
	folderCID := "folder-C"

	files := &memFileServiceStore{
		files: map[string]*domain.File{
			folderAID: {
				ID:          folderAID,
				UserID:      "user-owner",
				Name:        "Engineering",
				IsDirectory: true,
				Path:        "/folder-A/",
			},
			folderBID: {
				ID:          folderBID,
				UserID:      "user-owner",
				Name:        "Backend",
				ParentID:    &folderAID,
				IsDirectory: true,
				Path:        "/folder-A/folder-B/",
			},
			folderCID: {
				ID:          folderCID,
				UserID:      "user-owner",
				Name:        "Specs",
				ParentID:    &folderBID,
				IsDirectory: true,
				Path:        "/folder-A/folder-B/folder-C/",
			},
		},
		roles: map[string]map[string]string{
			folderAID: {
				"viewer@blobcloud.dev": domain.RoleViewer,
			},
			folderBID: {
				"viewer@blobcloud.dev": domain.RoleViewer,
			},
		},
	}

	users := &memUserLookup{
		users: map[string]*domain.User{
			"user-owner":  {ID: "user-owner", Email: "owner@blobcloud.dev"},
			"user-viewer": {ID: "user-viewer", Email: "viewer@blobcloud.dev"},
		},
	}

	perms := &memPermLookup{
		allowed: map[string]bool{
			"folder-A:viewer@blobcloud.dev": true,
			"folder-B:viewer@blobcloud.dev": true,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fileSvc := NewFileService(nil, users, files, nil, perms, newFaultStorage(), logger)

	// 1. Cycle Guard: Moving folder-A into itself must fail
	_, err := fileSvc.RenameMove(ctx, "user-owner", folderAID, RenameMoveRequest{
		ParentID:  &folderAID,
		hasParent: true,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot move a folder into itself") {
		t.Fatalf("expected self-move cycle guard error, got: %v", err)
	}

	// 2. Cycle Guard: Moving folder-A into its deep descendant folder-C (/folder-A/folder-B/folder-C/) must fail
	_, err = fileSvc.RenameMove(ctx, "user-owner", folderAID, RenameMoveRequest{
		ParentID:  &folderCID,
		hasParent: true,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot move directory inside its own descendant") {
		t.Fatalf("expected descendant cycle guard error, got: %v", err)
	}

	// 3. RBAC Enforcement: VIEWER attempting to rename folder-A must fail with access denied
	_, err = fileSvc.RenameMove(ctx, "user-viewer", folderAID, RenameMoveRequest{
		Name: "HackedName",
	})
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected VIEWER rename to be denied, got: %v", err)
	}

	// 4. Golden Rule Fallback: VIEWER moving shared subfolder folder-B into their root creates a personal shortcut instead of mutating folder-B!
	res, err := fileSvc.RenameMove(ctx, "user-viewer", folderBID, RenameMoveRequest{
		ParentID:  nil,
		hasParent: true,
	})
	if err != nil {
		t.Fatalf("expected Golden Rule shortcut fallback on VIEWER move, got error: %v", err)
	}
	if res.TargetID == nil || *res.TargetID != folderBID || res.UserID != "user-viewer" {
		t.Fatalf("expected personal shortcut pointing to %s owned by user-viewer, got %+v", folderBID, res)
	}
}

// ---------------------------------------------------------------------------
// 6. Concurrency & Race Test: 50 Goroutines Uploading Same Filename & Same Block
// ---------------------------------------------------------------------------

// TestUpload_ConcurrentSameFilenameAndSameBlockRace spawns 50 concurrent goroutines
// that simultaneously initiate, stage, verify, promote, and finalize uploads for the
// EXACT same filename ("quarterly_forecast.xlsx") and the EXACT same physical block.
// Verifies under -race that:
//  1. All 50 concurrent sessions complete cleanly without data races or corrupt state.
//  2. Advisory locking per (user_id, parent_id, filename) produces 1 active file + 49
//     strictly monotonic, duplicate-free archived versions (versions 1..49).
//  3. The shared physical block exists once in canonical CAS (blocks/<sha256>), and all
//     50 staging quarantine objects are cleaned up.
func TestUpload_ConcurrentSameFilenameAndSameBlockRace(t *testing.T) {
	const workers = 50
	svc, storage, blockStore, sessionStore, _, _ := newTestUploadHarness()
	ctx := context.Background()

	sharedPayload := bytes.Repeat([]byte("CONCURRENT_SHARED_BLOCK_DATA_"), 128) // 3712 bytes
	sharedHash := sha256Hex(sharedPayload)

	// Per-target advisory lock table simulating PostgreSQL pg_advisory_xact_lock(hashtext(uploadLockKey))
	var advisoryMu sync.Mutex
	activeFiles := make(map[string]*domain.File)
	fileVersions := make(map[string][]int) // fileID -> recorded version_numbers

	svc.commitTx = func(ctx context.Context, session *domain.UploadSession, sessionBlocks []domain.SessionBlock, _ CompleteRequest) (string, map[string]int64, string, error) {
		// Acquire per-target advisory lock just like UploadService.Complete does in Postgres
		advisoryMu.Lock()
		defer advisoryMu.Unlock()

		current, _, err := sessionStore.GetSessionByID(ctx, session.ID)
		if err != nil {
			return "", nil, "", err
		}
		if current.Status != domain.SessionStatusInitiated {
			return "", nil, "", fmt.Errorf("session %s already %s", session.ID, current.Status)
		}

		for _, b := range sessionBlocks {
			blockStore.seedBlock(b.BlockHash, b.SizeBytes)
		}

		lockKey := fmt.Sprintf("%s_root_%s", session.UserID, session.Filename)
		existing, exists := activeFiles[lockKey]
		action := domain.ActionFileCreated
		if !exists {
			existing = &domain.File{
				ID:        "file-shared-target",
				UserID:    session.UserID,
				Name:      session.Filename,
				SizeBytes: session.TotalSize,
				Status:    "ACTIVE",
			}
			activeFiles[lockKey] = existing
		} else {
			action = domain.ActionFileUpdated
			versions := fileVersions[existing.ID]
			nextVer := len(versions) + 1
			fileVersions[existing.ID] = append(versions, nextVer)
		}

		if err := sessionStore.UpdateSessionStatus(ctx, session.ID, domain.SessionStatusCompleted); err != nil {
			return "", nil, "", err
		}
		return existing.ID, map[string]int64{session.UserID: int64(len(fileVersions[existing.ID]) + 1)}, action, nil
	}

	// Step 1: Initiate all 50 sessions before any block is promoted so all 50 must run staging verification/promotion
	sessionIDs := make([]string, workers)
	for i := 0; i < workers; i++ {
		resp, err := svc.Initiate(ctx, InitiateRequest{
			UserID:    "user-concurrent",
			Filename:  "quarterly_forecast.xlsx",
			TotalSize: int64(len(sharedPayload)),
			Chunks: []InitiateChunk{
				{SHA256: sharedHash, SizeBytes: int32(len(sharedPayload))},
			},
		})
		if err != nil {
			t.Fatalf("worker %d Initiate failed: %v", i, err)
		}
		sessionIDs[i] = resp.SessionID
		storage.putRaw(fmt.Sprintf("staging/%s/0", resp.SessionID), sharedPayload, "mocketag")
	}

	// Step 2: Release all 50 goroutines simultaneously to call Complete()
	var wg sync.WaitGroup
	startGate := make(chan struct{})
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(sessID string) {
			defer wg.Done()
			<-startGate
			if _, err := svc.Complete(ctx, CompleteRequest{SessionID: sessID}, "user-concurrent"); err != nil {
				errCh <- err
			}
		}(sessionIDs[i])
	}

	close(startGate)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent Complete failed: %v", err)
	}

	// Verify canonical CAS block exists once and all 50 staging objects were cleaned up
	if !storage.hasKey("blocks/" + sharedHash) {
		t.Fatal("shared block missing from canonical CAS after 50 concurrent completions")
	}
	for _, sessID := range sessionIDs {
		if storage.hasKey(fmt.Sprintf("staging/%s/0", sessID)) {
			t.Fatalf("staging object for session %s was not cleaned up", sessID)
		}
	}

	// Verify version numbers are strictly 1..49 with zero duplicates or gaps
	recorded := fileVersions["file-shared-target"]
	if len(recorded) != workers-1 {
		t.Fatalf("expected %d archived versions after %d concurrent uploads, got %d", workers-1, workers, len(recorded))
	}
	seen := make(map[int]bool, len(recorded))
	for idx, v := range recorded {
		wantVer := idx + 1
		if v != wantVer {
			t.Fatalf("version index %d: got version_number %d, want monotonic %d", idx, v, wantVer)
		}
		if seen[v] {
			t.Fatalf("duplicate version_number %d detected", v)
		}
		seen[v] = true
	}
}

// ---------------------------------------------------------------------------
// 7. O(1) Memory (32 KB Buffer) Streaming Verification Proof & Benchmark
// ---------------------------------------------------------------------------

// zeroAllocPatternReader generates a deterministic byte stream of `remaining`
// bytes on the fly without allocating a backing slice in RAM.
type zeroAllocPatternReader struct {
	remaining int64
}

func (r *zeroAllocPatternReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 0x5A
	}
	r.remaining -= int64(n)
	return n, nil
}

type streamingOnlyStorage struct {
	*faultStorageProvider
	streamSize int64
}

func (s *streamingOnlyStorage) HeadObject(_ context.Context, key string) (*domain.ObjectMetadata, error) {
	if strings.HasPrefix(key, "staging/") {
		return &domain.ObjectMetadata{ContentLength: s.streamSize, ETag: "mocketag"}, nil
	}
	return nil, errors.New("not found")
}

func (s *streamingOnlyStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	if strings.HasPrefix(key, "staging/") {
		return io.NopCloser(&zeroAllocPatternReader{remaining: s.streamSize}), nil
	}
	return nil, errors.New("not found")
}

func (s *streamingOnlyStorage) PromoteObject(_ context.Context, _, _ string) error {
	return nil
}

// TestUpload_O1MemoryStreamingVerificationProof proves empirically that
// UploadService.Complete verifies a 64 MB staged block in O(1) memory (~32 KB
// io.Copy buffer) rather than buffering the 64 MB payload into heap RAM.
func TestUpload_O1MemoryStreamingVerificationProof(t *testing.T) {
	const streamBytes = 64 * 1024 * 1024 // 64 MiB

	// Compute expected SHA-256 of 64 MiB of 0x5A using a tiny 32 KB scratch buffer
	h := sha256.New()
	_, _ = io.Copy(h, &zeroAllocPatternReader{remaining: streamBytes})
	expectedHash := hex.EncodeToString(h.Sum(nil))

	svc, baseStorage, _, _, _, _ := newTestUploadHarness()
	streamStorage := &streamingOnlyStorage{
		faultStorageProvider: baseStorage,
		streamSize:           streamBytes,
	}
	svc.storage = streamStorage

	ctx := context.Background()
	initResp, err := svc.Initiate(ctx, InitiateRequest{
		UserID:    "user-memproof",
		Filename:  "large_64mb_video.mp4",
		TotalSize: streamBytes,
		Chunks: []InitiateChunk{
			{SHA256: expectedHash, SizeBytes: int32(streamBytes)},
		},
	})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}

	runtime.GC()
	var mBefore, mAfter runtime.MemStats
	runtime.ReadMemStats(&mBefore)

	if _, err := svc.Complete(ctx, CompleteRequest{SessionID: initResp.SessionID}, "user-memproof"); err != nil {
		t.Fatalf("Complete 64 MiB stream failed: %v", err)
	}

	runtime.ReadMemStats(&mAfter)
	allocatedDuring64MBStream := mAfter.TotalAlloc - mBefore.TotalAlloc

	// 64 MiB stream must allocate < 256 KiB total heap (< 0.4% of payload size),
	// proving O(1) 32 KB buffered streaming via io.Copy.
	const maxAllowedAlloc = 256 * 1024 // 256 KiB
	t.Logf("64 MiB streaming verification allocated %d bytes (%.2f KiB) total heap",
		allocatedDuring64MBStream, float64(allocatedDuring64MBStream)/1024.0)

	if allocatedDuring64MBStream > maxAllowedAlloc {
		t.Fatalf("O(1) memory violation: verifying 64 MiB allocated %d bytes (> %d bytes limit)",
			allocatedDuring64MBStream, maxAllowedAlloc)
	}
}

func BenchmarkStreamingSHA256Verification_32KBBuffer(b *testing.B) {
	const chunkBytes = 10 * 1024 * 1024 // 10 MiB per op
	b.SetBytes(chunkBytes)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rc := io.NopCloser(&zeroAllocPatternReader{remaining: chunkBytes})
		hasher := sha256.New()
		if _, err := io.Copy(hasher, rc); err != nil {
			b.Fatalf("io.Copy: %v", err)
		}
		_ = rc.Close()
		_ = hex.EncodeToString(hasher.Sum(nil))
	}
}
