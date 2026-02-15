package httpx

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
}

type testMemStorage struct {
	objects map[string][]byte
}

func (s *testMemStorage) GenerateUploadURL(ctx context.Context, blockHash string, expires time.Duration) (string, error) {
	return "", nil
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
	return nil
}
