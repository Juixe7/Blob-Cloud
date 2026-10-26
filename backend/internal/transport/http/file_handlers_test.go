package httpx

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mrand "math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"go-drive-clone/internal/auth"
	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/service"
)

func testDSN() string {
	if v := os.Getenv("DB_DSN"); v != "" {
		return v
	}
	return "postgres://postgres:postgres@localhost:5432/godrive?sslmode=disable"
}

func TestFilterReadableFilesUsesCurrentAuthorization(t *testing.T) {
	files := []*domain.File{
		{ID: "owned"},
		{ID: "revoked-share"},
		{ID: "inherited-share"},
		nil,
		{ID: ""},
	}
	got := filterReadableFiles(context.Background(), "viewer", files, func(_ context.Context, userID, fileID string) error {
		if userID != "viewer" {
			t.Fatalf("authorization called for user %q", userID)
		}
		if fileID == "revoked-share" {
			return errors.New("access denied")
		}
		return nil
	})
	if len(got) != 2 || got[0].ID != "owned" || got[1].ID != "inherited-share" {
		t.Fatalf("filterReadableFiles() = %#v, want only currently authorized files", got)
	}
}

func TestDownloadRangeRequest(t *testing.T) {
	// Connect to database
	db, err := sql.Open("pgx", testDSN())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Skip("Postgres unavailable, skipping download range request test")
	}

	ctx := context.Background()

	// Initialise repositories
	users := postgresrepo.NewUserRepository(db)
	files := postgresrepo.NewFileRepository(db)
	blocks := postgresrepo.NewBlockRepository(db)
	perms := postgresrepo.NewPermissionRepository(db)

	// Create test user
	email := fmt.Sprintf("range-tester-%d@example.com", time.Now().UnixNano())
	user := &domain.User{
		Email:        email,
		PasswordHash: "hash",
		IsVerified:   true,
	}
	if err := users.Create(ctx, user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create dummy S3 blocks. S3 block size is 4MB.
	// We want to test range seek across block boundaries.
	// Let's create two blocks. First block: 4MB of 'A', second block: 4MB of 'B'.
	block1Data := bytes.Repeat([]byte("A"), 4194304)
	block2Data := bytes.Repeat([]byte("B"), 4194304)

	block1Hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	block2Hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Mock Storage Provider
	memStorage := &testMemStorage{
		objects: map[string][]byte{
			"blocks/" + block1Hash: block1Data,
			"blocks/" + block2Hash: block2Data,
		},
	}

	b1 := &domain.Block{SHA256: block1Hash, SizeBytes: 4194304}
	b2 := &domain.Block{SHA256: block2Hash, SizeBytes: 4194304}
	if err := blocks.GetOrCreate(ctx, b1); err != nil {
		t.Fatalf("failed to create block1: %v", err)
	}
	if err := blocks.GetOrCreate(ctx, b2); err != nil {
		t.Fatalf("failed to create block2: %v", err)
	}

	// Create test file (8MB total)
	file := &domain.File{
		UserID:      user.ID,
		Name:        "range_test_file.txt",
		IsDirectory: false,
		SizeBytes:   8388608,
	}
	if err := files.Create(ctx, file); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	// Link blocks to file
	err = blocks.LinkBlocksToFile(ctx, file.ID, []domain.BlockSequence{
		{BlockID: b1.ID, SequenceNumber: 0},
		{BlockID: b2.ID, SequenceNumber: 1},
	})
	if err != nil {
		t.Fatalf("failed to link blocks: %v", err)
	}

	// Setup Server
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(memStorage, logger)
	srv.jwtSecret = "abcdefghijklmnopqrstuvwxyz012345"

	fileSvc := service.NewFileService(db, users, files, blocks, perms, memStorage, logger)
	zipSvc := service.NewZipService(files, blocks, memStorage)
	srv = srv.WithFileOperations(fileSvc, files, blocks).WithZipOperations(zipSvc)

	// Generate Auth Token
	token, err := auth.CreateToken(srv.jwtSecret, user.ID)
	if err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	// Test 1: Full download (No Range Header)
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/files/%s/download?token=%s", file.ID, token), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", file.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	srv.HandleDownload(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Length") != "8388608" {
		t.Errorf("expected Content-Length 8388608, got %s", resp.Header.Get("Content-Length"))
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 8388608 {
		t.Errorf("expected 8388608 bytes body, got %d", len(body))
	}

	// Test 2: Range download starting from 5000000 bytes (Range: bytes=5000000-)
	// Start byte: 5000000
	// File size: 8388608
	// Expected range: 5000000 - 8388607
	// Expected length: 3388608 bytes
	// startBlockIndex = 5000000 / 4194304 = 1
	// blockOffset = 5000000 % 4194304 = 805696
	startIdx, offset := srv.fileOps.CalculateRangeBlockOffset(5000000)
	if startIdx != 1 || offset != 805696 {
		t.Errorf("CalculateRangeBlockOffset(5000000): expected (1, 805696), got (%d, %d)", startIdx, offset)
	}

	reqRange := httptest.NewRequest("GET", fmt.Sprintf("/api/files/%s/download?token=%s", file.ID, token), nil)
	reqRange.Header.Set("Range", "bytes=5000000-")
	rctxRange := chi.NewRouteContext()
	rctxRange.URLParams.Add("id", file.ID)
	reqRange = reqRange.WithContext(context.WithValue(reqRange.Context(), chi.RouteCtxKey, rctxRange))
	wRange := httptest.NewRecorder()
	srv.HandleDownload(wRange, reqRange)

	respRange := wRange.Result()
	if respRange.StatusCode != http.StatusPartialContent {
		t.Errorf("expected 206 Partial Content, got %d", respRange.StatusCode)
	}
	expectedContentRange := "bytes 5000000-8388607/8388608"
	if respRange.Header.Get("Content-Range") != expectedContentRange {
		t.Errorf("expected Content-Range %q, got %q", expectedContentRange, respRange.Header.Get("Content-Range"))
	}
	if respRange.Header.Get("Content-Length") != "3388608" {
		t.Errorf("expected Content-Length 3388608, got %s", respRange.Header.Get("Content-Length"))
	}
	bodyRange, _ := io.ReadAll(respRange.Body)
	if len(bodyRange) != 3388608 {
		t.Errorf("expected 3388608 bytes body, got %d", len(bodyRange))
	}
	// The body should consist entirely of 'B' since it starts inside block 2 (index 1)
	for i, b := range bodyRange {
		if b != 'B' {
			t.Errorf("byte %d: expected 'B', got %c", i, b)
			break
		}
	}

	// Test 3: Encrypted file should ignore Range header, return 200 OK with total ciphertext size
	encFile := &domain.File{
		UserID:      user.ID,
		Name:        "secret_vault.bin",
		IsDirectory: false,
		SizeBytes:   8388608,
		IsEncrypted: true,
	}
	if err := files.Create(ctx, encFile); err != nil {
		t.Fatalf("failed to create encrypted file: %v", err)
	}
	err = blocks.LinkBlocksToFile(ctx, encFile.ID, []domain.BlockSequence{
		{BlockID: b1.ID, SequenceNumber: 0},
		{BlockID: b2.ID, SequenceNumber: 1},
	})
	if err != nil {
		t.Fatalf("failed to link blocks to encrypted file: %v", err)
	}

	reqEncRange := httptest.NewRequest("GET", fmt.Sprintf("/api/files/%s/download?token=%s", encFile.ID, token), nil)
	reqEncRange.Header.Set("Range", "bytes=5000000-")
	rctxEnc := chi.NewRouteContext()
	rctxEnc.URLParams.Add("id", encFile.ID)
	reqEncRange = reqEncRange.WithContext(context.WithValue(reqEncRange.Context(), chi.RouteCtxKey, rctxEnc))
	wEncRange := httptest.NewRecorder()
	srv.HandleDownload(wEncRange, reqEncRange)

	respEnc := wEncRange.Result()
	if respEnc.StatusCode != http.StatusOK {
		t.Errorf("encrypted download with Range: expected 200 OK, got %d", respEnc.StatusCode)
	}
	if respEnc.Header.Get("Accept-Ranges") != "none" {
		t.Errorf("encrypted download: expected Accept-Ranges 'none', got %q", respEnc.Header.Get("Accept-Ranges"))
	}
	if respEnc.Header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("encrypted download: expected Content-Type 'application/octet-stream', got %q", respEnc.Header.Get("Content-Type"))
	}
	bodyEnc, _ := io.ReadAll(respEnc.Body)
	if len(bodyEnc) != 8388608 {
		t.Errorf("encrypted download: expected 8388608 bytes (full ciphertext), got %d", len(bodyEnc))
	}
}

func TestCalculateDynamicRangeBlockOffset(t *testing.T) {
	// Simulate FastCDC variable chunks: 1.5MB, 2.5MB, 1.2MB
	blocks := []*domain.Block{
		{SHA256: "hash1", SizeBytes: 1500000},
		{SHA256: "hash2", SizeBytes: 2500000},
		{SHA256: "hash3", SizeBytes: 1200000},
	}

	tests := []struct {
		startByte int64
		wantIdx   int
		wantOff   int64
	}{
		{startByte: 0, wantIdx: 0, wantOff: 0},
		{startByte: 500000, wantIdx: 0, wantOff: 500000},
		{startByte: 1499999, wantIdx: 0, wantOff: 1499999},
		{startByte: 1500000, wantIdx: 1, wantOff: 0},
		{startByte: 2000000, wantIdx: 1, wantOff: 500000},
		{startByte: 3999999, wantIdx: 1, wantOff: 2499999},
		{startByte: 4000000, wantIdx: 2, wantOff: 0},
		{startByte: 4500000, wantIdx: 2, wantOff: 500000},
	}

	for _, tc := range tests {
		idx, off := service.CalculateDynamicRangeBlockOffset(blocks, tc.startByte)
		if idx != tc.wantIdx || off != tc.wantOff {
			t.Errorf("CalculateDynamicRangeBlockOffset(%d): got (%d, %d), want (%d, %d)",
				tc.startByte, idx, off, tc.wantIdx, tc.wantOff)
		}
	}
}

type testMemStorage struct {
	objects map[string][]byte
}

func (s *testMemStorage) PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	return nil
}
func (s *testMemStorage) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *testMemStorage) GetObjectRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
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
func (s *testMemStorage) HeadObject(ctx context.Context, key string) (*domain.ObjectMetadata, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return &domain.ObjectMetadata{
		ContentLength: int64(len(b)),
		ETag:          "mocketag",
	}, nil
}
func (s *testMemStorage) DeleteObject(ctx context.Context, key string) error {
	delete(s.objects, key)
	return nil
}
func (s *testMemStorage) GenerateStagingUploadURL(ctx context.Context, stagingKey string, expires time.Duration) (string, error) {
	return "", nil
}
func (s *testMemStorage) PromoteObject(ctx context.Context, srcKey, destKey string) error {
	b, ok := s.objects[srcKey]
	if !ok {
		return errors.New("not found: " + srcKey)
	}
	s.objects[destKey] = b
	delete(s.objects, srcKey)
	return nil
}

func TestParseRangeHeader(t *testing.T) {
	fileSize := int64(1000)

	tests := []struct {
		name      string
		header    string
		fileSize  int64
		wantStart int64
		wantEnd   int64
		wantOk    bool
	}{
		{"Standard range 0-499", "bytes=0-499", fileSize, 0, 499, true},
		{"Open-ended range 500-", "bytes=500-", fileSize, 500, 999, true},
		{"Suffix range -500 (RFC 7233)", "bytes=-500", fileSize, 500, 999, true},
		{"Suffix range larger than file -1500", "bytes=-1500", fileSize, 0, 999, true},
		{"Single last byte -1", "bytes=-1", fileSize, 999, 999, true},
		{"Invalid empty header", "", fileSize, 0, 0, false},
		{"Invalid prefix", "characters=0-500", fileSize, 0, 0, false},
		{"Invalid negative start", "bytes=-50-100", fileSize, 0, 0, false},
		{"Invalid start > end", "bytes=600-200", fileSize, 0, 0, false},
		{"Invalid start >= fileSize", "bytes=1000-1050", fileSize, 0, 0, false},
		{"Invalid suffix -0", "bytes=-0", fileSize, 0, 0, false},
		{"Empty range -", "bytes=-", fileSize, 0, 0, false},
		{"Zero file size", "bytes=0-100", 0, 0, 0, false},
		{"Zero file size suffix", "bytes=-100", 0, 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotEnd, gotOk := parseRangeHeader(tt.header, tt.fileSize)
			if gotOk != tt.wantOk {
				t.Fatalf("parseRangeHeader(%q) ok = %v, want %v", tt.header, gotOk, tt.wantOk)
			}
			if gotOk {
				if gotStart != tt.wantStart || gotEnd != tt.wantEnd {
					t.Fatalf("parseRangeHeader(%q) = (%d, %d), want (%d, %d)", tt.header, gotStart, gotEnd, tt.wantStart, tt.wantEnd)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// In-Memory Fakes for Deterministic Security, IDOR & Streaming Revocation Tests
// ---------------------------------------------------------------------------

type memUserStore struct {
	mu    sync.RWMutex
	users map[string]*domain.User
}

func (m *memUserStore) GetByID(_ context.Context, id string) (*domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	cp := *u
	return &cp, nil
}

type memFileStore struct {
	mu    sync.RWMutex
	files map[string]*domain.File
	roles map[string]map[string]string // fileID -> email -> role
}

func (m *memFileStore) GetByID(_ context.Context, id string) (*domain.File, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.files[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	cp := *f
	return &cp, nil
}

func (m *memFileStore) GetResolvedPermission(_ context.Context, fileID string, userEmail string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if byEmail, ok := m.roles[fileID]; ok {
		if role, exists := byEmail[userEmail]; exists && role != "" {
			return role, nil
		}
	}
	return "", domain.ErrPermissionNotFound
}

func (m *memFileStore) GetFileByNameAndParent(context.Context, string, string, *string) (*domain.File, error) {
	return nil, sql.ErrNoRows
}
func (m *memFileStore) GetFolderByNameAndParent(context.Context, string, string, *string) (*domain.File, error) {
	return nil, sql.ErrNoRows
}
func (m *memFileStore) Create(context.Context, *domain.File) error                { return nil }
func (m *memFileStore) Update(context.Context, *domain.File) error                { return nil }
func (m *memFileStore) SoftDelete(context.Context, string, string) error          { return nil }
func (m *memFileStore) Restore(context.Context, string, string) error             { return nil }
func (m *memFileStore) ListTrash(context.Context, string) ([]*domain.File, error) { return nil, nil }
func (m *memFileStore) DeleteRecursive(context.Context, string, string) (int64, []string, error) {
	return 0, nil, nil
}
func (m *memFileStore) ListDirectory(context.Context, string, *string) ([]*domain.File, error) {
	return nil, nil
}
func (m *memFileStore) ListSharedWithUser(context.Context, string, string) ([]*domain.File, error) {
	return nil, nil
}
func (m *memFileStore) GetUserStorageUsage(context.Context, string) (*domain.UserStorageUsage, error) {
	return &domain.UserStorageUsage{}, nil
}
func (m *memFileStore) BulkSoftDelete(context.Context, []string, string) error { return nil }
func (m *memFileStore) BulkRestore(context.Context, []string, string) error    { return nil }
func (m *memFileStore) BulkMove(context.Context, []string, *string, string) error {
	return nil
}
func (m *memFileStore) BulkHardDelete(context.Context, []string, string) (int64, []string, error) {
	return 0, nil, nil
}
func (m *memFileStore) WithTx(_ postgresrepo.DBTX) *postgresrepo.FileRepository { return nil }

type memBlockStore struct {
	blocks map[string][]*domain.Block
}

func (m *memBlockStore) ListFileBlockHashes(_ context.Context, fileID string) ([]string, error) {
	var hashes []string
	for _, b := range m.blocks[fileID] {
		hashes = append(hashes, b.SHA256)
	}
	return hashes, nil
}

func (m *memBlockStore) ListFileBlocks(_ context.Context, fileID string) ([]*domain.Block, error) {
	return m.blocks[fileID], nil
}

type memPermStore struct{}

func (m *memPermStore) CheckUserPermission(context.Context, string, string, []string) (bool, error) {
	return false, nil
}
func (m *memPermStore) FindCollaboratorUserIDs(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *memPermStore) WithTx(_ postgresrepo.DBTX) *postgresrepo.PermissionRepository { return nil }

type hookMemStorage struct {
	*testMemStorage
	onGetObject func(key string)
}

func (h *hookMemStorage) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	if h.onGetObject != nil {
		h.onGetObject(key)
	}
	return h.testMemStorage.GetObject(ctx, key)
}

// TestDownload_MidStreamRevocationAt16BlockBoundary proves that revoking a user's
// read permission (or quarantining/trashing the file) while a multi-block download is
// actively streaming cuts off the HTTP stream at the 16-block check window.
func TestDownload_MidStreamRevocationAt16BlockBoundary(t *testing.T) {
	const totalBlocks = 25
	const blockSize = 1024

	userStore := &memUserStore{
		users: map[string]*domain.User{
			"user-owner":  {ID: "user-owner", Email: "owner@blobcloud.dev"},
			"user-viewer": {ID: "user-viewer", Email: "viewer@blobcloud.dev"},
		},
	}

	fileStore := &memFileStore{
		files: map[string]*domain.File{
			"file-stream-25": {
				ID:          "file-stream-25",
				UserID:      "user-owner",
				Name:        "confidential_archive.bin",
				IsDirectory: false,
				SizeBytes:   int64(totalBlocks * blockSize),
				Status:      "ACTIVE",
			},
		},
		roles: map[string]map[string]string{
			"file-stream-25": {
				"viewer@blobcloud.dev": domain.RoleViewer,
			},
		},
	}

	baseStorage := &testMemStorage{objects: make(map[string][]byte)}
	var fileBlocks []*domain.Block
	for i := 0; i < totalBlocks; i++ {
		hash := fmt.Sprintf("block-hash-%02d", i)
		baseStorage.objects["blocks/"+hash] = bytes.Repeat([]byte{byte('A' + i)}, blockSize)
		fileBlocks = append(fileBlocks, &domain.Block{
			ID:        fmt.Sprintf("blk-%d", i),
			SHA256:    hash,
			SizeBytes: blockSize,
		})
	}

	blockStore := &memBlockStore{
		blocks: map[string][]*domain.Block{
			"file-stream-25": fileBlocks,
		},
	}

	// Revoke user-viewer's VIEWER permission while block 5 is streaming!
	storage := &hookMemStorage{
		testMemStorage: baseStorage,
		onGetObject: func(key string) {
			if key == "blocks/block-hash-05" {
				fileStore.mu.Lock()
				delete(fileStore.roles["file-stream-25"], "viewer@blobcloud.dev")
				fileStore.mu.Unlock()
			}
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fileSvc := service.NewFileService(nil, userStore, fileStore, blockStore, &memPermStore{}, storage, logger)
	zipSvc := service.NewZipService(nil, nil, storage)

	srv := NewServer(storage, logger)
	srv.jwtSecret = "abcdefghijklmnopqrstuvwxyz012345"
	srv = srv.WithFileOperations(fileSvc, nil, nil).WithZipOperations(zipSvc)

	viewerToken, err := auth.CreateToken(srv.jwtSecret, "user-viewer")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/files/file-stream-25/download?token="+viewerToken, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "file-stream-25")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	srv.HandleDownload(w, req)

	streamedBytes := w.Body.Bytes()
	expectedCutoffBytes := 16 * blockSize // Blocks 0..15 streamed; cut off at idx == 16!
	if len(streamedBytes) != expectedCutoffBytes {
		t.Fatalf("expected stream to cut off at 16-block window (%d bytes), but streamed %d bytes (out of %d)",
			expectedCutoffBytes, len(streamedBytes), totalBlocks*blockSize)
	}
}

// TestDownload_IDORAndRBACBoundaries verifies that User B cannot download or access
// thumbnails for User A's private file (IDOR -> 403 Forbidden), and that a VIEWER
// can read a shared file (200 OK) but cannot overwrite its thumbnail (RBAC -> 403 Forbidden).
func TestDownload_IDORAndRBACBoundaries(t *testing.T) {
	userStore := &memUserStore{
		users: map[string]*domain.User{
			"user-alice":   {ID: "user-alice", Email: "alice@blobcloud.dev"},
			"user-mallory": {ID: "user-mallory", Email: "mallory@blobcloud.dev"},
			"user-viewer":  {ID: "user-viewer", Email: "viewer@blobcloud.dev"},
		},
	}

	fileStore := &memFileStore{
		files: map[string]*domain.File{
			"alice-private-file": {
				ID:          "alice-private-file",
				UserID:      "user-alice",
				Name:        "tax_return.pdf",
				IsDirectory: false,
				SizeBytes:   512,
				Status:      "ACTIVE",
			},
		},
		roles: map[string]map[string]string{
			"alice-private-file": {
				"viewer@blobcloud.dev": domain.RoleViewer,
			},
		},
	}

	storage := &testMemStorage{
		objects: map[string][]byte{
			"blocks/hash-private":               bytes.Repeat([]byte("SECRET_TAX_DATA_"), 32), // 512 bytes
			"thumbnails/alice-private-file.png": []byte("private-thumbnail"),
		},
	}
	blockStore := &memBlockStore{
		blocks: map[string][]*domain.Block{
			"alice-private-file": {{ID: "b1", SHA256: "hash-private", SizeBytes: 512}},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fileSvc := service.NewFileService(nil, userStore, fileStore, blockStore, &memPermStore{}, storage, logger)
	zipSvc := service.NewZipService(nil, nil, storage)

	srv := NewServer(storage, logger)
	srv.jwtSecret = "abcdefghijklmnopqrstuvwxyz012345"
	srv = srv.WithFileOperations(fileSvc, nil, nil).WithZipOperations(zipSvc)

	malloryTok, _ := auth.CreateToken(srv.jwtSecret, "user-mallory")
	viewerTok, _ := auth.CreateToken(srv.jwtSecret, "user-viewer")

	// 1. IDOR Attack: Mallory attempts to download Alice's file -> 403 Forbidden
	reqDownload := httptest.NewRequest(http.MethodGet, "/api/files/alice-private-file/download?token="+malloryTok, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "alice-private-file")
	reqDownload = reqDownload.WithContext(context.WithValue(reqDownload.Context(), chi.RouteCtxKey, rctx))
	wDownload := httptest.NewRecorder()
	srv.HandleDownload(wDownload, reqDownload)
	if wDownload.Code != http.StatusForbidden {
		t.Fatalf("IDOR download by Mallory: expected 403 Forbidden, got %d", wDownload.Code)
	}

	// 2. IDOR Attack: Mallory attempts to fetch Alice's thumbnail -> 403 Forbidden
	reqThumb := httptest.NewRequest(http.MethodGet, "/api/files/alice-private-file/thumbnail?token="+malloryTok, nil)
	reqThumb = reqThumb.WithContext(context.WithValue(reqThumb.Context(), chi.RouteCtxKey, rctx))
	wThumb := httptest.NewRecorder()
	srv.HandleGetThumbnail(wThumb, reqThumb)
	if wThumb.Code != http.StatusForbidden {
		t.Fatalf("IDOR thumbnail read by Mallory: expected 403 Forbidden, got %d", wThumb.Code)
	}

	// 3. RBAC Check: Viewer can download shared file (200 OK), but cannot upload/overwrite thumbnail (403 Forbidden)
	reqViewerDL := httptest.NewRequest(http.MethodGet, "/api/files/alice-private-file/download?token="+viewerTok, nil)
	reqViewerDL = reqViewerDL.WithContext(context.WithValue(reqViewerDL.Context(), chi.RouteCtxKey, rctx))
	wViewerDL := httptest.NewRecorder()
	srv.HandleDownload(wViewerDL, reqViewerDL)
	if wViewerDL.Code != http.StatusOK || wViewerDL.Body.Len() != 512 {
		t.Fatalf("Viewer download: expected 200 OK with 512 bytes, got status %d (%d bytes)", wViewerDL.Code, wViewerDL.Body.Len())
	}
	viewerThumb := httptest.NewRequest(http.MethodGet, "/api/files/alice-private-file/thumbnail?token="+viewerTok, nil)
	viewerThumb = viewerThumb.WithContext(context.WithValue(viewerThumb.Context(), chi.RouteCtxKey, rctx))
	wViewerThumb := httptest.NewRecorder()
	srv.HandleGetThumbnail(wViewerThumb, viewerThumb)
	if wViewerThumb.Code != http.StatusOK || wViewerThumb.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Viewer thumbnail: status=%d cache-control=%q; want private, no-store", wViewerThumb.Code, wViewerThumb.Header().Get("Cache-Control"))
	}

	reqViewerWrite := httptest.NewRequest(http.MethodPut, "/api/files/alice-private-file/thumbnail", bytes.NewReader([]byte("fake-image")))
	reqViewerWrite.Header.Set("Authorization", "Bearer "+viewerTok)
	reqViewerWrite = reqViewerWrite.WithContext(context.WithValue(reqViewerWrite.Context(), chi.RouteCtxKey, rctx))
	wViewerWrite := httptest.NewRecorder()
	srv.HandleUploadThumbnail(wViewerWrite, reqViewerWrite)
	if wViewerWrite.Code != http.StatusForbidden {
		t.Fatalf("RBAC viewer write attempt: expected 403 Forbidden, got %d", wViewerWrite.Code)
	}
}

// ---------------------------------------------------------------------------
// Property-Based Range Streaming Over Irregular FastCDC Blocks & Fuzzing
// ---------------------------------------------------------------------------

// TestRangeStreaming_PropertyRandomBlockLayoutsVsByteSliceReference generates a
// file split into 12 irregular FastCDC-style variable-length blocks, then runs 100
// randomized HTTP Range requests (closed ranges, open-ended ranges, and RFC 7233
// suffix ranges) through HandleDownload and asserts byte-for-byte equality with
// plain originalData[start:end+1] slicing.
func TestRangeStreaming_PropertyRandomBlockLayoutsVsByteSliceReference(t *testing.T) {
	rng := mrand.New(mrand.NewSource(99887766))

	// Create 12 variable-length blocks (between 3 KB and 29 KB each)
	blockSizes := []int{3100, 14250, 7890, 28400, 512, 19333, 11005, 4096, 22111, 8765, 15432, 6010}
	var originalData []byte
	storage := &testMemStorage{objects: make(map[string][]byte)}
	var blocks []*domain.Block

	for i, sz := range blockSizes {
		chunk := make([]byte, sz)
		_, _ = rng.Read(chunk)
		hash := fmt.Sprintf("var-block-%02d", i)
		storage.objects["blocks/"+hash] = chunk
		originalData = append(originalData, chunk...)
		blocks = append(blocks, &domain.Block{
			ID:        fmt.Sprintf("vb-%d", i),
			SHA256:    hash,
			SizeBytes: int32(sz),
		})
	}
	totalSize := int64(len(originalData))

	userStore := &memUserStore{
		users: map[string]*domain.User{
			"user-prop": {ID: "user-prop", Email: "prop@blobcloud.dev"},
		},
	}
	fileStore := &memFileStore{
		files: map[string]*domain.File{
			"file-var-blocks": {
				ID:          "file-var-blocks",
				UserID:      "user-prop",
				Name:        "video_stream.mp4",
				IsDirectory: false,
				SizeBytes:   totalSize,
				Status:      "ACTIVE",
			},
		},
	}
	blockStore := &memBlockStore{
		blocks: map[string][]*domain.Block{
			"file-var-blocks": blocks,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fileSvc := service.NewFileService(nil, userStore, fileStore, blockStore, &memPermStore{}, storage, logger)
	zipSvc := service.NewZipService(nil, nil, storage)
	srv := NewServer(storage, logger)
	srv.jwtSecret = "abcdefghijklmnopqrstuvwxyz012345"
	srv = srv.WithFileOperations(fileSvc, nil, nil).WithZipOperations(zipSvc)

	tok, _ := auth.CreateToken(srv.jwtSecret, "user-prop")

	// Run 100 randomized range requests straddling variable block boundaries
	for trial := 0; trial < 100; trial++ {
		var rangeHdr string
		var wantStart, wantEnd int64

		switch trial % 3 {
		case 0:
			// Closed range: bytes=start-end
			wantStart = int64(rng.Intn(int(totalSize)))
			wantEnd = wantStart + int64(rng.Intn(int(totalSize-wantStart)))
			rangeHdr = fmt.Sprintf("bytes=%d-%d", wantStart, wantEnd)
		case 1:
			// Open-ended range: bytes=start-
			wantStart = int64(rng.Intn(int(totalSize)))
			wantEnd = totalSize - 1
			rangeHdr = fmt.Sprintf("bytes=%d-", wantStart)
		case 2:
			// RFC 7233 Suffix range: bytes=-suffixLen
			suffixLen := int64(1 + rng.Intn(int(totalSize)))
			wantStart = totalSize - suffixLen
			wantEnd = totalSize - 1
			rangeHdr = fmt.Sprintf("bytes=-%d", suffixLen)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/files/file-var-blocks/download?token="+tok, nil)
		req.Header.Set("Range", rangeHdr)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "file-var-blocks")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		srv.HandleDownload(w, req)

		if w.Code != http.StatusPartialContent {
			t.Fatalf("trial %d (%s): expected 206 Partial Content, got %d", trial, rangeHdr, w.Code)
		}

		expectedSlice := originalData[wantStart : wantEnd+1]
		if !bytes.Equal(w.Body.Bytes(), expectedSlice) {
			t.Fatalf("trial %d (%s): range response bytes mismatch reference slice (got %d bytes, want %d bytes)",
				trial, rangeHdr, w.Body.Len(), len(expectedSlice))
		}
	}
}

// FuzzParseRangeHeader uses Go's native fuzzing engine to verify parseRangeHeader
// never panics on arbitrary malformed headers and always returns valid bounds when ok==true.
func FuzzParseRangeHeader(f *testing.F) {
	seeds := []string{
		"",
		"bytes=0-0",
		"bytes=0-499",
		"bytes=500-",
		"bytes=-500",
		"bytes=-1",
		"bytes=-999999999",
		"bytes=999999999999999999999999999-0",
		"bytes=--10",
		"bytes=10-5",
		"bytes=0-10-20",
		"bytes=\x00\xff-\xfe",
		"items=0-10",
	}
	for _, s := range seeds {
		f.Add(s, int64(1000))
	}

	f.Fuzz(func(t *testing.T, header string, fileSize int64) {
		start, end, ok := parseRangeHeader(header, fileSize)
		if !ok {
			return
		}
		if fileSize <= 0 {
			t.Fatalf("parseRangeHeader(%q, %d) returned ok=true for non-positive fileSize", header, fileSize)
		}
		if start < 0 || end < start || end >= fileSize {
			t.Fatalf("parseRangeHeader(%q, %d) returned out-of-bounds [%d, %d]", header, fileSize, start, end)
		}
	})
}

// FuzzSanitiseFilename verifies SanitiseFilename never panics and strips all path separators.
func FuzzSanitiseFilename(f *testing.F) {
	for _, s := range []string{"report.pdf", "../../etc/passwd", `C:\Windows\System32\cmd.exe`, "file\x00name.txt", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		out := service.SanitiseFilename(input)
		if strings.ContainsAny(out, `/\`) {
			t.Fatalf("SanitiseFilename(%q) left path separator in %q", input, out)
		}
	})
}
