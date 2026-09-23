package httpx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"go-drive-clone/internal/auth"
	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
)

func TestHandleSyncEndpoints_Unauthenticated(t *testing.T) {
	srv := NewServer(&testMemStorage{}, slog.Default())

	// Without journal configured -> 503
	req := httptest.NewRequest("GET", "/api/sync/delta", nil)
	w := httptest.NewRecorder()
	srv.HandleSyncDelta(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", w.Code)
	}

	// With dummy journal, with jwtSecret configured, missing token -> 401
	srv.WithJournal(&postgresrepo.JournalRepository{}).WithRealtime(nil, "testsecret", nil)
	req = httptest.NewRequest("GET", "/api/sync/delta", nil)
	w = httptest.NewRecorder()
	srv.HandleSyncDelta(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", w.Code)
	}
}

func TestHandleSyncEndpoints_Integration(t *testing.T) {
	db, err := sql.Open("pgx", testDSN())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Skip("Postgres unavailable, skipping sync handler integration test")
	}

	ctx := context.Background()
	users := postgresrepo.NewUserRepository(db)
	journal := postgresrepo.NewJournalRepository(db)

	email := fmt.Sprintf("sync-tester-%d@example.com", time.Now().UnixNano())
	user := &domain.User{
		Email:        email,
		PasswordHash: "hash",
		IsVerified:   true,
	}
	if err := users.Create(ctx, user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	token, err := auth.CreateToken("supersecretjwtkeythatislongenough123456", user.ID)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	srv := NewServer(&testMemStorage{}, slog.Default()).
		WithJournal(journal).
		WithRealtime(nil, "supersecretjwtkeythatislongenough123456", nil)

	// Test GET /api/sync/cursor initially (should be 0 or current max)
	reqCursor := httptest.NewRequest("GET", "/api/sync/cursor", nil)
	reqCursor.Header.Set("Authorization", "Bearer "+token)
	wCursor := httptest.NewRecorder()
	srv.HandleSyncCursor(wCursor, reqCursor)

	if wCursor.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from HandleSyncCursor, got %d: %s", wCursor.Code, wCursor.Body.String())
	}
	var cursorResp map[string]int64
	if err := json.Unmarshal(wCursor.Body.Bytes(), &cursorResp); err != nil {
		t.Fatalf("failed to parse cursor json: %v", err)
	}
	initialCursor := cursorResp["cursor"]

	// Record a journal entry
	fileId := fmt.Sprintf("file-%d", time.Now().UnixNano())
	c1, err := journal.Record(ctx, &domain.JournalEntry{
		UserID:      user.ID,
		FileID:      fileId,
		Action:      domain.ActionFileCreated,
		Name:        "test-delta-file.txt",
		IsDirectory: false,
		SizeBytes:   1024,
		MimeType:    "text/plain",
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("failed to record journal entry: %v", err)
	}

	// Test GET /api/sync/delta?since=initialCursor
	reqDelta := httptest.NewRequest("GET", fmt.Sprintf("/api/sync/delta?since=%d", initialCursor), nil)
	reqDelta.Header.Set("Authorization", "Bearer "+token)
	wDelta := httptest.NewRecorder()
	srv.HandleSyncDelta(wDelta, reqDelta)

	if wDelta.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from HandleSyncDelta, got %d: %s", wDelta.Code, wDelta.Body.String())
	}

	var deltaResp DeltaSyncResponse
	if err := json.Unmarshal(wDelta.Body.Bytes(), &deltaResp); err != nil {
		t.Fatalf("failed to parse delta response: %v", err)
	}

	if len(deltaResp.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(deltaResp.Entries))
	}
	if deltaResp.Entries[0].Cursor != c1 {
		t.Errorf("expected cursor %d, got %d", c1, deltaResp.Entries[0].Cursor)
	}
	if deltaResp.NextCursor != c1 {
		t.Errorf("expected next_cursor %d, got %d", c1, deltaResp.NextCursor)
	}
}
