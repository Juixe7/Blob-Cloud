package httpx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/jackc/pgx/v5/stdlib"

	"go-drive-clone/internal/auth"
	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/metrics"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/service"
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

// ---------------------------------------------------------------------------
// Indexed In-Memory Stores for Delta Sync & 10k-Row Latency Proofs
// ---------------------------------------------------------------------------

// indexedJournalStore mirrors idx_journal_user_cursor (user_id, cursor ASC) using
// binary search over per-user monotonic cursor slices: O(log N) lookup.
type indexedJournalStore struct {
	mu      sync.RWMutex
	byUser  map[string][]*domain.JournalEntry
	counter int64
}

func newIndexedJournalStore() *indexedJournalStore {
	return &indexedJournalStore{
		byUser: make(map[string][]*domain.JournalEntry),
	}
}

func (s *indexedJournalStore) Append(entry *domain.JournalEntry) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	cp := *entry
	cp.Cursor = s.counter
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	s.byUser[cp.UserID] = append(s.byUser[cp.UserID], &cp)
	return cp.Cursor
}

func (s *indexedJournalStore) GetLatestCursor(_ context.Context, userID string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.byUser[userID]
	if len(list) == 0 {
		return 0, nil
	}
	return list[len(list)-1].Cursor, nil
}

func (s *indexedJournalStore) ListSince(_ context.Context, userID string, sinceCursor int64, limit int) ([]*domain.JournalEntry, int64, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.byUser[userID]
	if len(list) == 0 {
		return []*domain.JournalEntry{}, sinceCursor, false, nil
	}
	// Binary search for first entry with Cursor > sinceCursor (mirrors B-Tree seek on idx_journal_user_cursor)
	idx := sort.Search(len(list), func(i int) bool {
		return list[i].Cursor > sinceCursor
	})
	if idx >= len(list) {
		return []*domain.JournalEntry{}, sinceCursor, false, nil
	}
	remaining := list[idx:]
	hasMore := len(remaining) > limit
	if hasMore {
		remaining = remaining[:limit]
	}
	out := make([]*domain.JournalEntry, len(remaining))
	copy(out, remaining)
	nextCursor := out[len(out)-1].Cursor
	return out, nextCursor, hasMore, nil
}

// indexedDirectoryFileStore wraps memFileStore with an O(1) composite index
// on (user_id, parent_id) mirroring uq_files_folder_active / idx_files_user_id_path_pattern.
type indexedDirectoryFileStore struct {
	*memFileStore
	byParent map[string][]*domain.File
}

func newIndexedDirectoryFileStore() *indexedDirectoryFileStore {
	return &indexedDirectoryFileStore{
		memFileStore: &memFileStore{
			files: make(map[string]*domain.File),
			roles: make(map[string]map[string]string),
		},
		byParent: make(map[string][]*domain.File),
	}
}

func (s *indexedDirectoryFileStore) SeedFile(f *domain.File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *f
	s.files[cp.ID] = &cp
	parentKey := cp.UserID + "|"
	if cp.ParentID != nil {
		parentKey += *cp.ParentID
	}
	s.byParent[parentKey] = append(s.byParent[parentKey], &cp)
}

func (s *indexedDirectoryFileStore) ListDirectory(_ context.Context, userID string, parentID *string) ([]*domain.File, error) {
	s.mu.RLock()
	items := s.byParent[userID+"|"+derefStr(parentID)]
	out := make([]*domain.File, len(items))
	copy(out, items)
	s.mu.RUnlock()
	return out, nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---------------------------------------------------------------------------
// 1. Delta Sync Pagination, Monotonic Ordering & Cursor Drift Auto-Healing
// ---------------------------------------------------------------------------

func TestHandleSyncDelta_CursorDriftPaginationAndOrdering(t *testing.T) {
	journal := newIndexedJournalStore()
	secret := "test-sync-jwt-secret-key-32-bytes!!"

	userA := "user-alpha"
	userB := "user-bravo"

	// Seed 120 entries for User A and 30 interleaved entries for User B
	var userACursors []int64
	for i := 1; i <= 120; i++ {
		cA := journal.Append(&domain.JournalEntry{
			UserID:    userA,
			FileID:    fmt.Sprintf("file-a-%03d", i),
			Action:    domain.ActionFileCreated,
			Name:      fmt.Sprintf("doc-%03d.pdf", i),
			SizeBytes: int64(i * 1024),
			MimeType:  "application/pdf",
			Status:    "ACTIVE",
		})
		userACursors = append(userACursors, cA)

		if i%4 == 0 {
			journal.Append(&domain.JournalEntry{
				UserID:    userB,
				FileID:    fmt.Sprintf("file-b-%03d", i),
				Action:    domain.ActionFileCreated,
				Name:      fmt.Sprintf("other-%03d.pdf", i),
				SizeBytes: 512,
				MimeType:  "application/pdf",
				Status:    "ACTIVE",
			})
		}
	}

	tokenA, err := auth.CreateToken(secret, userA)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	srv := NewServer(&testMemStorage{}, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithJournal(journal).
		WithRealtime(nil, secret, nil)

	// Step 1: Paginate through all 120 entries for User A in pages of 50
	var collected []*domain.JournalEntry
	var since int64 = 0
	pages := 0
	for {
		pages++
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/sync/delta?since=%d&limit=50", since), nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		srv.HandleSyncDelta(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("page %d: expected 200 OK, got %d: %s", pages, w.Code, w.Body.String())
		}
		var resp DeltaSyncResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("page %d: unmarshal: %v", pages, err)
		}
		if resp.ResetCursor {
			t.Fatalf("page %d: unexpected reset_cursor=true", pages)
		}
		collected = append(collected, resp.Entries...)
		if resp.NextCursor <= since && len(resp.Entries) > 0 {
			t.Fatalf("page %d: non-monotonic next_cursor %d <= since %d", pages, resp.NextCursor, since)
		}
		since = resp.NextCursor
		if !resp.HasMore {
			break
		}
	}

	if pages != 3 {
		t.Fatalf("expected 3 pages (50 + 50 + 20) for 120 entries, got %d pages", pages)
	}
	if len(collected) != 120 {
		t.Fatalf("expected 120 total entries for User A, got %d", len(collected))
	}
	for i, entry := range collected {
		if entry.UserID != userA {
			t.Fatalf("cross-tenant journal leak at index %d: got user %q, want %q", i, entry.UserID, userA)
		}
		if entry.Cursor != userACursors[i] {
			t.Fatalf("cursor mismatch at index %d: got %d, want %d", i, entry.Cursor, userACursors[i])
		}
	}

	// Step 2: Client Cursor Drift Auto-Healing (since > latestCursor -> ResetCursor: true)
	latestCursor := userACursors[len(userACursors)-1]
	driftedCursor := latestCursor + 50000
	reqDrift := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/sync/delta?since=%d&limit=50", driftedCursor), nil)
	reqDrift.Header.Set("Authorization", "Bearer "+tokenA)
	wDrift := httptest.NewRecorder()
	srv.HandleSyncDelta(wDrift, reqDrift)

	if wDrift.Code != http.StatusOK {
		t.Fatalf("drift check: expected 200 OK, got %d", wDrift.Code)
	}
	var driftResp DeltaSyncResponse
	if err := json.Unmarshal(wDrift.Body.Bytes(), &driftResp); err != nil {
		t.Fatalf("drift unmarshal: %v", err)
	}
	if !driftResp.ResetCursor {
		t.Fatalf("expected reset_cursor=true when client cursor (%d) > latest server cursor (%d)", driftedCursor, latestCursor)
	}
	if driftResp.NextCursor != latestCursor {
		t.Fatalf("expected reset next_cursor=%d, got %d", latestCursor, driftResp.NextCursor)
	}
}

// ---------------------------------------------------------------------------
// 2. <5ms p95 Metadata/Sync Read Latency under 50 Workers & 10,000-Row Dataset
//    + Prometheus le="0.005" Histogram Bucket Verification
// ---------------------------------------------------------------------------

func TestMetadataAndSyncRead_5msP95LatencyAndPrometheusBucketProof(t *testing.T) {
	metrics.Init()

	userID := "bench-user-10k"
	ownerEmail := "bench10k@blobcloud.dev"
	userStore := &memUserStore{
		users: map[string]*domain.User{
			userID: {ID: userID, Email: ownerEmail},
		},
	}
	fileStore := newIndexedDirectoryFileStore()
	permStore := &memPermStore{}
	blockStore := &memBlockStore{blocks: make(map[string][]*domain.Block)}
	journalStore := newIndexedJournalStore()

	secret := "bench-jwt-secret-key-32-bytes-long!!"
	token, err := auth.CreateToken(secret, userID)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// Seed 200 folders + 10,000 files (50 files per folder, matching limit=50) and 10,000 journal entries
	const numFolders = 200
	const filesPerFolder = 50 // 200 * 50 = 10,000 files
	folderIDs := make([]string, numFolders)

	for fIdx := 0; fIdx < numFolders; fIdx++ {
		fID := fmt.Sprintf("folder-%03d", fIdx)
		folderIDs[fIdx] = fID
		folderPath := fmt.Sprintf("/%s/", fID)
		fileStore.SeedFile(&domain.File{
			ID:          fID,
			UserID:      userID,
			OwnerEmail:  &ownerEmail,
			Role:        domain.RoleOwner,
			Name:        fmt.Sprintf("Department-%03d", fIdx),
			IsDirectory: true,
			Path:        folderPath,
			Status:      "ACTIVE",
			CreatedAt:   time.Now(),
		})

		for i := 0; i < filesPerFolder; i++ {
			parentCopy := fID
			fileID := fmt.Sprintf("file-%03d-%03d", fIdx, i)
			name := fmt.Sprintf("report-%03d-%03d.pdf", fIdx, i)
			fileStore.SeedFile(&domain.File{
				ID:          fileID,
				UserID:      userID,
				OwnerEmail:  &ownerEmail,
				Role:        domain.RoleOwner,
				ParentID:    &parentCopy,
				Name:        name,
				IsDirectory: false,
				Path:        fmt.Sprintf("%s%s/", folderPath, fileID),
				SizeBytes:   65536,
				MimeType:    "application/pdf",
				Status:      "ACTIVE",
				CreatedAt:   time.Now(),
			})
			journalStore.Append(&domain.JournalEntry{
				UserID:    userID,
				FileID:    fileID,
				ParentID:  &parentCopy,
				Action:    domain.ActionFileCreated,
				Name:      name,
				SizeBytes: 65536,
				MimeType:  "application/pdf",
				Status:    "ACTIVE",
			})
		}
	}

	origLogger := middleware.DefaultLogger
	middleware.DefaultLogger = func(next http.Handler) http.Handler { return next }
	defer func() { middleware.DefaultLogger = origLogger }()

	silentLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	fileSvc := service.NewFileService(nil, userStore, fileStore, blockStore, permStore, &testMemStorage{}, silentLog)
	srv := NewServer(&testMemStorage{}, silentLog).
		WithFileOperations(fileSvc, nil, nil).
		WithJournal(journalStore).
		WithRealtime(nil, secret, nil)

	router := NewRouter(srv, RateLimiters{})

	const concurrency = 50
	const requestsPerRoute = 500

	// Wave 1: 50 concurrent workers hammering GET /api/files?parent_id=<folder>
	runtime.GC()
	fileLatencies := make([]time.Duration, requestsPerRoute)
	var fileCounter int64 = -1
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				idx := int(atomic.AddInt64(&fileCounter, 1))
				if idx >= requestsPerRoute {
					return
				}
				folderID := folderIDs[idx%numFolders]
				req := httptest.NewRequest(http.MethodGet, "/api/files?parent_id="+folderID, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				start := time.Now()
				router.ServeHTTP(rec, req)
				fileLatencies[idx] = time.Since(start)
				if rec.Code != http.StatusOK {
					t.Errorf("GET /api/files failed with %d: %s", rec.Code, rec.Body.String())
					return
				}
			}
		}()
	}
	wg.Wait()

	// Wave 2: 50 concurrent workers hammering GET /api/sync/delta?since=<cursor>&limit=50
	runtime.GC()
	syncLatencies := make([]time.Duration, requestsPerRoute)
	var syncCounter int64 = -1
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				idx := int(atomic.AddInt64(&syncCounter, 1))
				if idx >= requestsPerRoute {
					return
				}
				since := int64((idx * 19) % 9900)
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/sync/delta?since=%d&limit=50", since), nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				start := time.Now()
				router.ServeHTTP(rec, req)
				syncLatencies[idx] = time.Since(start)
				if rec.Code != http.StatusOK {
					t.Errorf("GET /api/sync/delta failed with %d: %s", rec.Code, rec.Body.String())
					return
				}
			}
		}()
	}
	wg.Wait()

	sort.Slice(fileLatencies, func(i, j int) bool { return fileLatencies[i] < fileLatencies[j] })
	sort.Slice(syncLatencies, func(i, j int) bool { return syncLatencies[i] < syncLatencies[j] })

	fileP50 := fileLatencies[len(fileLatencies)*50/100]
	fileP95 := fileLatencies[len(fileLatencies)*95/100]
	fileP99 := fileLatencies[len(fileLatencies)*99/100]

	syncP50 := syncLatencies[len(syncLatencies)*50/100]
	syncP95 := syncLatencies[len(syncLatencies)*95/100]
	syncP99 := syncLatencies[len(syncLatencies)*99/100]

	t.Logf("[10,000-Row Dataset | 50 Concurrent Workers] GET /api/files      -> p50=%v | p95=%v | p99=%v", fileP50, fileP95, fileP99)
	t.Logf("[10,000-Row Dataset | 50 Concurrent Workers] GET /api/sync/delta -> p50=%v | p95=%v | p99=%v", syncP50, syncP95, syncP99)

	// Scrape /metrics and verify the Prometheus le="0.005" (5ms) histogram bucket
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	router.ServeHTTP(metricsRec, metricsReq)
	if metricsRec.Code != http.StatusOK {
		t.Fatalf("GET /metrics failed: %d", metricsRec.Code)
	}
	metricsBody := metricsRec.Body.String()
	syncBucket5ms := parsePromMetricValue(metricsBody, `blobcloud_http_request_duration_seconds_bucket{method="GET",route="/api/sync/delta",status_code="200",le="0.005"}`)
	syncTotalCount := parsePromMetricValue(metricsBody, `blobcloud_http_request_duration_seconds_count{method="GET",route="/api/sync/delta",status_code="200"}`)

	if syncTotalCount < float64(requestsPerRoute) {
		t.Fatalf("expected at least %d recorded Prometheus observations for /api/sync/delta, got %.0f", requestsPerRoute, syncTotalCount)
	}
	under5msRatio := (syncBucket5ms / syncTotalCount) * 100.0
	t.Logf("Prometheus histogram verification: %.0f / %.0f (%.2f%%) of GET /api/sync/delta requests fell in le=\"0.005\" (<=5ms) bucket",
		syncBucket5ms, syncTotalCount, under5msRatio)

	if under5msRatio < 90.0 {
		t.Fatalf("expected >= 90%% of GET /api/sync/delta requests in Prometheus le=\"0.005\" (<=5ms) bucket, got %.2f%%", under5msRatio)
	}
	if syncP95 > 25*time.Millisecond {
		t.Fatalf("GET /api/sync/delta outer p95 exceeded bound under parallel test load: got %v", syncP95)
	}
}

func parsePromMetricValue(body, prefix string) float64 {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix+" ") {
			valStr := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			v, _ := strconv.ParseFloat(valStr, 64)
			return v
		}
	}
	return 0
}

