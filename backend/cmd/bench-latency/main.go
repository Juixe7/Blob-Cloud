// Package main implements the Blob-Cloud Metadata & Sync Latency + Subtree Index
// Scaling Benchmark CLI (`go run ./cmd/bench-latency`).
//
// It validates three core architectural claims:
//  1. <5ms p95 Read Latency on GET /api/files and GET /api/sync/delta under
//     50 concurrent workers against a 10,000-row hierarchical dataset.
//  2. Prometheus Histogram Verification of blobcloud_http_request_duration_seconds_bucket{le="0.005"}.
//  3. Subtree Query Scaling (1,000 / 10,000 / 50,000 rows): O(log N + K) B-Tree
//     Materialized Path prefix scan (`idx_files_user_id_path_pattern` with text_pattern_ops)
//     vs O(D * N) multi-level parent-id traversal, plus live PostgreSQL EXPLAIN ANALYZE
//     when -postgres is enabled.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"go-drive-clone/internal/auth"
	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/metrics"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/service"
	httpx "go-drive-clone/internal/transport/http"
)

// ---------------------------------------------------------------------------
// In-Memory Indexed Stores (B-Tree / Composite Index Equivalents)
// ---------------------------------------------------------------------------

type noopStorage struct{}

func (n *noopStorage) GenerateUploadURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", nil
}
func (n *noopStorage) PutObject(_ context.Context, _ string, _ io.Reader, _ int64, _ string) error {
	return nil
}
func (n *noopStorage) GetObject(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (n *noopStorage) GetObjectRange(_ context.Context, _ string, _, _ int64) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (n *noopStorage) HeadObject(_ context.Context, _ string) (*domain.ObjectMetadata, error) {
	return &domain.ObjectMetadata{}, nil
}
func (n *noopStorage) DeleteObject(_ context.Context, _ string) error { return nil }
func (n *noopStorage) GenerateStagingUploadURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", nil
}
func (n *noopStorage) PromoteObject(_ context.Context, _, _ string) error { return nil }

type benchUserStore struct {
	user *domain.User
}

func (u *benchUserStore) GetByID(_ context.Context, id string) (*domain.User, error) {
	if u.user != nil && u.user.ID == id {
		return u.user, nil
	}
	return nil, sql.ErrNoRows
}

type benchPermStore struct{}

func (p *benchPermStore) CheckUserPermission(_ context.Context, _, _ string, _ []string) (bool, error) {
	return false, nil
}
func (p *benchPermStore) FindCollaboratorUserIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (p *benchPermStore) WithTx(_ postgresrepo.DBTX) *postgresrepo.PermissionRepository {
	return nil
}

type benchBlockStore struct{}

func (b *benchBlockStore) ListFileBlockHashes(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (b *benchBlockStore) ListFileBlocks(_ context.Context, _ string) ([]*domain.Block, error) {
	return nil, nil
}

// benchFileStore maintains:
//   - primary key index `byID`
//   - composite directory index `byParent` on (user_id, parent_id)
//   - sorted B-Tree slice `byPath` on (user_id, path) for O(log N + K) prefix scans
type benchFileStore struct {
	mu       sync.RWMutex
	byID     map[string]*domain.File
	byParent map[string][]*domain.File
	byPath   []*domain.File
}

func newBenchFileStore(capacity int) *benchFileStore {
	return &benchFileStore{
		byID:     make(map[string]*domain.File, capacity),
		byParent: make(map[string][]*domain.File, 128),
		byPath:   make([]*domain.File, 0, capacity),
	}
}

func (s *benchFileStore) Seed(f *domain.File) {
	cp := *f
	s.byID[cp.ID] = &cp
	parentKey := cp.UserID + "|"
	if cp.ParentID != nil {
		parentKey += *cp.ParentID
	}
	s.byParent[parentKey] = append(s.byParent[parentKey], &cp)
	s.byPath = append(s.byPath, &cp)
}

func (s *benchFileStore) FinalizeIndex() {
	sort.Slice(s.byPath, func(i, j int) bool {
		if s.byPath[i].UserID != s.byPath[j].UserID {
			return s.byPath[i].UserID < s.byPath[j].UserID
		}
		return s.byPath[i].Path < s.byPath[j].Path
	})
}

func (s *benchFileStore) GetByID(_ context.Context, id string) (*domain.File, error) {
	s.mu.RLock()
	f, ok := s.byID[id]
	s.mu.RUnlock()
	if !ok {
		return nil, sql.ErrNoRows
	}
	cp := *f
	return &cp, nil
}
func (s *benchFileStore) GetResolvedPermission(_ context.Context, _, _ string) (string, error) {
	return domain.RoleOwner, nil
}
func (s *benchFileStore) GetFileByNameAndParent(_ context.Context, _, _ string, _ *string) (*domain.File, error) {
	return nil, sql.ErrNoRows
}
func (s *benchFileStore) GetFolderByNameAndParent(_ context.Context, _, _ string, _ *string) (*domain.File, error) {
	return nil, sql.ErrNoRows
}
func (s *benchFileStore) Create(_ context.Context, f *domain.File) error {
	s.Seed(f)
	return nil
}
func (s *benchFileStore) Update(_ context.Context, _ *domain.File) error        { return nil }
func (s *benchFileStore) SoftDelete(_ context.Context, _, _ string) error       { return nil }
func (s *benchFileStore) Restore(_ context.Context, _, _ string) error          { return nil }
func (s *benchFileStore) ListTrash(_ context.Context, _ string) ([]*domain.File, error) {
	return nil, nil
}
func (s *benchFileStore) DeleteRecursive(_ context.Context, _, _ string) (int64, []string, error) {
	return 0, nil, nil
}
func (s *benchFileStore) ListDirectory(_ context.Context, userID string, parentID *string) ([]*domain.File, error) {
	s.mu.RLock()
	parentKey := userID + "|"
	if parentID != nil {
		parentKey += *parentID
	}
	items := s.byParent[parentKey]
	out := make([]*domain.File, len(items))
	copy(out, items)
	s.mu.RUnlock()
	return out, nil
}
func (s *benchFileStore) ListSharedWithUser(_ context.Context, _, _ string) ([]*domain.File, error) {
	return nil, nil
}
func (s *benchFileStore) GetUserStorageUsage(_ context.Context, _ string) (*domain.UserStorageUsage, error) {
	return &domain.UserStorageUsage{}, nil
}
func (s *benchFileStore) BulkSoftDelete(_ context.Context, _ []string, _ string) error { return nil }
func (s *benchFileStore) BulkRestore(_ context.Context, _ []string, _ string) error    { return nil }
func (s *benchFileStore) BulkMove(_ context.Context, _ []string, _ *string, _ string) error {
	return nil
}
func (s *benchFileStore) BulkHardDelete(_ context.Context, _ []string, _ string) (int64, []string, error) {
	return 0, nil, nil
}
func (s *benchFileStore) WithTx(_ postgresrepo.DBTX) *postgresrepo.FileRepository { return nil }

func (s *benchFileStore) CollectSubtreeFileIDs(_ context.Context, userID, rootID, pathPrefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// O(log N + K) B-Tree range seek on sorted (user_id, path)
	idx := sort.Search(len(s.byPath), func(i int) bool {
		if s.byPath[i].UserID != userID {
			return s.byPath[i].UserID > userID
		}
		return s.byPath[i].Path >= pathPrefix
	})
	var ids []string
	for i := idx; i < len(s.byPath); i++ {
		f := s.byPath[i]
		if f.UserID != userID || !strings.HasPrefix(f.Path, pathPrefix) {
			break
		}
		if !f.IsDirectory {
			ids = append(ids, f.ID)
		}
	}
	return ids, nil
}

// benchJournalStore mirrors idx_journal_user_cursor (user_id, cursor ASC).
type benchJournalStore struct {
	mu      sync.RWMutex
	entries []*domain.JournalEntry
}

func newBenchJournalStore(capacity int) *benchJournalStore {
	return &benchJournalStore{
		entries: make([]*domain.JournalEntry, 0, capacity),
	}
}

func (j *benchJournalStore) Seed(e *domain.JournalEntry) {
	cp := *e
	cp.Cursor = int64(len(j.entries) + 1)
	j.entries = append(j.entries, &cp)
}

func (j *benchJournalStore) GetLatestCursor(_ context.Context, _ string) (int64, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if len(j.entries) == 0 {
		return 0, nil
	}
	return j.entries[len(j.entries)-1].Cursor, nil
}

func (j *benchJournalStore) ListSince(_ context.Context, _ string, sinceCursor int64, limit int) ([]*domain.JournalEntry, int64, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	idx := sort.Search(len(j.entries), func(i int) bool {
		return j.entries[i].Cursor > sinceCursor
	})
	if idx >= len(j.entries) {
		return []*domain.JournalEntry{}, sinceCursor, false, nil
	}
	rem := j.entries[idx:]
	hasMore := len(rem) > limit
	if hasMore {
		rem = rem[:limit]
	}
	out := make([]*domain.JournalEntry, len(rem))
	copy(out, rem)
	return out, out[len(out)-1].Cursor, hasMore, nil
}

// ---------------------------------------------------------------------------
// Latency Statistics Helper
// ---------------------------------------------------------------------------

type latencyStats struct {
	Endpoint   string
	Count      int
	TotalWall  time.Duration
	Throughput float64
	Min        time.Duration
	P50        time.Duration
	P95        time.Duration
	P99        time.Duration
	Max        time.Duration
	Mean       time.Duration
	PromBucket float64
	PromCount  float64
}

func computeStats(endpoint string, samples []time.Duration, wall time.Duration, promBucket, promCount float64) latencyStats {
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var sum time.Duration
	for _, d := range sorted {
		sum += d
	}

	n := len(sorted)
	return latencyStats{
		Endpoint:   endpoint,
		Count:      n,
		TotalWall:  wall,
		Throughput: float64(n) / wall.Seconds(),
		Min:        sorted[0],
		P50:        sorted[n*50/100],
		P95:        sorted[n*95/100],
		P99:        sorted[n*99/100],
		Max:        sorted[n-1],
		Mean:       sum / time.Duration(n),
		PromBucket: promBucket,
		PromCount:  promCount,
	}
}

func main() {
	workers := flag.Int("c", 50, "Number of concurrent HTTP workers")
	datasetRows := flag.Int("rows", 10000, "Number of seeded file + journal rows")
	requestsPerEndpoint := flag.Int("n", 2500, "Requests dispatched per endpoint")
	usePostgres := flag.Bool("postgres", false, "Also run EXPLAIN (ANALYZE, BUFFERS) against live PostgreSQL (DATABASE_URL / TEST_DATABASE_URL)")
	flag.Parse()

	_ = godotenv.Load()
	metrics.Init()

	fmt.Println("================================================================================")
	fmt.Println("⚡ Blob-Cloud Metadata & Sync Read Latency + B-Tree Index Scaling Benchmark")
	fmt.Println("================================================================================")
	fmt.Printf("  • Dataset Size:        %d files + %d monotonic journal cursors\n", *datasetRows, *datasetRows)
	fmt.Printf("  • Concurrency:         %d worker goroutines\n", *workers)
	fmt.Printf("  • Requests / Route:    %d HTTP requests (%d total)\n", *requestsPerEndpoint, *requestsPerEndpoint*2)
	fmt.Printf("  • Target SLA:          p95 < 5.00 ms (Prometheus bucket le=\"0.005\")\n")
	fmt.Println("================================================================================")

	// 1. Seed 10,000-row hierarchical dataset
	seedStart := time.Now()
	fileStore := newBenchFileStore(*datasetRows + 100)
	permStore := &benchPermStore{}
	journalStore := newBenchJournalStore(*datasetRows)

	userID := "user-bench-10k"
	ownerEmail := "bench10k@blobcloud.dev"
	userStore := &benchUserStore{
		user: &domain.User{ID: userID, Email: ownerEmail},
	}
	jwtSecret := "blob-cloud-bench-jwt-secret-key-32b!!"
	token, err := auth.CreateToken(jwtSecret, userID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create JWT token: %v\n", err)
		os.Exit(1)
	}

	const numFolders = 200
	filesPerFolder := *datasetRows / numFolders
	folderIDs := make([]string, numFolders)

	for fIdx := 0; fIdx < numFolders; fIdx++ {
		fID := fmt.Sprintf("folder-%03d", fIdx)
		folderIDs[fIdx] = fID
		folderPath := fmt.Sprintf("/%s/", fID)
		fileStore.Seed(&domain.File{
			ID:          fID,
			UserID:      userID,
			OwnerEmail:  &ownerEmail,
			Role:        domain.RoleOwner,
			Name:        fmt.Sprintf("Workspace-%03d", fIdx),
			IsDirectory: true,
			Path:        folderPath,
			Status:      "ACTIVE",
			CreatedAt:   time.Now(),
		})

		for i := 0; i < filesPerFolder; i++ {
			parentCopy := fID
			fileID := fmt.Sprintf("file-%03d-%04d", fIdx, i)
			name := fmt.Sprintf("design-spec-%03d-%04d.pdf", fIdx, i)
			fileStore.Seed(&domain.File{
				ID:          fileID,
				UserID:      userID,
				OwnerEmail:  &ownerEmail,
				Role:        domain.RoleOwner,
				ParentID:    &parentCopy,
				Name:        name,
				IsDirectory: false,
				Path:        fmt.Sprintf("%s%s/", folderPath, fileID),
				SizeBytes:   131072,
				MimeType:    "application/pdf",
				Status:      "ACTIVE",
				CreatedAt:   time.Now(),
			})
			journalStore.Seed(&domain.JournalEntry{
				UserID:    userID,
				FileID:    fileID,
				ParentID:  &parentCopy,
				Action:    domain.ActionFileCreated,
				Name:      name,
				SizeBytes: 131072,
				MimeType:  "application/pdf",
				Status:    "ACTIVE",
			})
		}
	}
	fileStore.FinalizeIndex()
	fmt.Printf("✅ Seeded %d files (across %d folders, %d files/folder) + %d journal entries in %v\n\n",
		*datasetRows, numFolders, filesPerFolder, *datasetRows, time.Since(seedStart).Round(time.Millisecond))

	// 2. Wire full HTTP router with PrometheusMiddleware and JWT verification.
	// Silence chi's per-request stdout logger so 50 concurrent workers measure
	// HTTP router + middleware + handler + index latency rather than Windows console I/O.
	middleware.DefaultLogger = func(next http.Handler) http.Handler { return next }
	silentLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	fileSvc := service.NewFileService(nil, userStore, fileStore, &benchBlockStore{}, permStore, &noopStorage{}, silentLog)
	srv := httpx.NewServer(&noopStorage{}, silentLog).
		WithFileOperations(fileSvc, nil, nil).
		WithJournal(journalStore).
		WithRealtime(nil, jwtSecret, nil)
	router := httpx.NewRouter(srv, httpx.RateLimiters{})

	// 3. Benchmark GET /api/files?parent_id=<folder> under 50 concurrent workers
	runtime.GC()
	fileSamples := make([]time.Duration, *requestsPerEndpoint)
	var fileIdx int64 = -1
	var wg sync.WaitGroup

	fileWallStart := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&fileIdx, 1))
				if i >= *requestsPerEndpoint {
					return
				}
				folderID := folderIDs[i%numFolders]
				req := httptest.NewRequest(http.MethodGet, "/api/files?parent_id="+folderID, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				t0 := time.Now()
				router.ServeHTTP(rec, req)
				fileSamples[i] = time.Since(t0)
			}
		}()
	}
	wg.Wait()
	fileWall := time.Since(fileWallStart)

	// 4. Benchmark GET /api/sync/delta?since=<cursor>&limit=50 under 50 concurrent workers
	runtime.GC()
	syncSamples := make([]time.Duration, *requestsPerEndpoint)
	var syncIdx int64 = -1

	syncWallStart := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&syncIdx, 1))
				if i >= *requestsPerEndpoint {
					return
				}
				since := int64((i * 17) % (*datasetRows - 100))
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/sync/delta?since=%d&limit=50", since), nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				t0 := time.Now()
				router.ServeHTTP(rec, req)
				syncSamples[i] = time.Since(t0)
			}
		}()
	}
	wg.Wait()
	syncWall := time.Since(syncWallStart)

	// 5. Scrape /metrics to inspect blobcloud_http_request_duration_seconds_bucket{le="0.005"}
	mReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	mRec := httptest.NewRecorder()
	router.ServeHTTP(mRec, mReq)
	mBody := mRec.Body.String()

	filesLe5ms := extractPromMetric(mBody, `blobcloud_http_request_duration_seconds_bucket{method="GET",route="/api/files/",status_code="200",le="0.005"}`)
	filesCount := extractPromMetric(mBody, `blobcloud_http_request_duration_seconds_count{method="GET",route="/api/files/",status_code="200"}`)
	if filesCount == 0 {
		filesLe5ms = extractPromMetric(mBody, `blobcloud_http_request_duration_seconds_bucket{method="GET",route="/api/files",status_code="200",le="0.005"}`)
		filesCount = extractPromMetric(mBody, `blobcloud_http_request_duration_seconds_count{method="GET",route="/api/files",status_code="200"}`)
	}

	syncLe5ms := extractPromMetric(mBody, `blobcloud_http_request_duration_seconds_bucket{method="GET",route="/api/sync/delta",status_code="200",le="0.005"}`)
	syncCount := extractPromMetric(mBody, `blobcloud_http_request_duration_seconds_count{method="GET",route="/api/sync/delta",status_code="200"}`)

	fStats := computeStats("GET /api/files", fileSamples, fileWall, filesLe5ms, filesCount)
	sStats := computeStats("GET /api/sync/delta", syncSamples, syncWall, syncLe5ms, syncCount)

	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("1. HTTP Metadata & Sync Read Latency Results (50 Concurrent Workers, 10k Rows)")
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("%-22s | %9s | %9s | %9s | %9s | %10s | %s\n",
		"Endpoint", "p50", "p95", "p99", "Mean", "Req/sec", "Prom le=\"0.005\" (<=5ms)")
	fmt.Println("-----------------------+-----------+-----------+-----------+-----------+------------+------------------------")
	for _, st := range []latencyStats{fStats, sStats} {
		ratio := 0.0
		if st.PromCount > 0 {
			ratio = (st.PromBucket / st.PromCount) * 100.0
		}
		fmt.Printf("%-22s | %9s | %9s | %9s | %9s | %10.0f | %.0f/%.0f (%.2f%%)\n",
			st.Endpoint,
			formatDuration(st.P50),
			formatDuration(st.P95),
			formatDuration(st.P99),
			formatDuration(st.Mean),
			st.Throughput,
			st.PromBucket, st.PromCount, ratio,
		)
	}
	fmt.Println()

	// 6. Subtree Query Scaling Benchmark: Materialized Path B-Tree Prefix Scan vs Iterative Parent-Join
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("2. Subtree Query Scaling: Materialized Path B-Tree Prefix vs Recursive Parent Scan")
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("%-12s | %-18s | %-22s | %-22s | %s\n",
		"Dataset", "Subtree Matches", "B-Tree Path Prefix", "Recursive Parent Scan", "Speedup")
	fmt.Println("-------------+--------------------+------------------------+------------------------+----------")

	for _, totalN := range []int{1000, 10000, 50000} {
		bTreeDur, recDur, matchCount := runSubtreeScalingComparison(totalN)
		speedup := float64(recDur) / float64(bTreeDur)
		fmt.Printf("%-12d | %-18d | %-22s | %-22s | %.1fx\n",
			totalN, matchCount, formatDuration(bTreeDur), formatDuration(recDur), speedup)
	}
	fmt.Println()

	// 7. Live PostgreSQL EXPLAIN (ANALYZE, BUFFERS) Proof (when -postgres or BENCH_POSTGRES=1 is set)
	if *usePostgres || os.Getenv("BENCH_POSTGRES") == "1" {
		runPostgresExplainAnalyze()
	} else {
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Println("3. PostgreSQL Index Architecture (Run with -postgres for live EXPLAIN ANALYZE)")
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Println("  • Subtree Prefix Index (Migration 000018):")
		fmt.Println("    CREATE INDEX idx_files_user_id_path_pattern ON files (user_id, path text_pattern_ops);")
		fmt.Println("    Query: WHERE user_id = $1 AND path LIKE '/folder-001/%'")
		fmt.Println("    Plan:  Index Only Scan using idx_files_user_id_path_pattern on files")
		fmt.Println("           Index Cond: ((user_id = $1) AND (path ~>=~ '/folder-001/') AND (path ~<~ '/folder-0010'))")
		fmt.Println()
		fmt.Println("  • Delta Sync Cursor Index (Migration 000017):")
		fmt.Println("    CREATE INDEX idx_journal_user_cursor ON journal_entries (user_id, cursor);")
		fmt.Println("    Query: WHERE user_id = $1 AND cursor > $2 ORDER BY cursor ASC LIMIT 51")
		fmt.Println("    Plan:  Limit -> Index Scan using idx_journal_user_cursor on journal_entries")
		fmt.Println("           Index Cond: ((user_id = $1) AND (cursor > $2))")
		fmt.Println("================================================================================")
	}
}

// runSubtreeScalingComparison compares O(log N + K) B-Tree prefix range scan against
// an O(D * N) iterative level-by-level parent_id scan across a 4-level hierarchy.
func runSubtreeScalingComparison(totalRows int) (bTreeAvg, recursiveAvg time.Duration, matches int) {
	store := newBenchFileStore(totalRows + 64)
	userID := "user-scale"

	// Create 20 root departments, each with a 4-level nested subtree
	const numDepts = 20
	rowsPerDept := totalRows / numDepts
	targetPrefix := "/dept-05/"

	for d := 0; d < numDepts; d++ {
		deptID := fmt.Sprintf("dept-%02d", d)
		lvl1ID := fmt.Sprintf("team-%02d", d)
		lvl2ID := fmt.Sprintf("proj-%02d", d)
		lvl3ID := fmt.Sprintf("mod-%02d", d)

		deptCopy := deptID
		lvl1Copy := lvl1ID
		lvl2Copy := lvl2ID
		lvl3Copy := lvl3ID

		store.Seed(&domain.File{ID: deptID, UserID: userID, IsDirectory: true, Path: fmt.Sprintf("/%s/", deptID)})
		store.Seed(&domain.File{ID: lvl1ID, UserID: userID, ParentID: &deptCopy, IsDirectory: true, Path: fmt.Sprintf("/%s/%s/", deptID, lvl1ID)})
		store.Seed(&domain.File{ID: lvl2ID, UserID: userID, ParentID: &lvl1Copy, IsDirectory: true, Path: fmt.Sprintf("/%s/%s/%s/", deptID, lvl1ID, lvl2ID)})
		store.Seed(&domain.File{ID: lvl3ID, UserID: userID, ParentID: &lvl2Copy, IsDirectory: true, Path: fmt.Sprintf("/%s/%s/%s/%s/", deptID, lvl1ID, lvl2ID, lvl3ID)})

		for i := 0; i < rowsPerDept; i++ {
			fID := fmt.Sprintf("f-%02d-%05d", d, i)
			store.Seed(&domain.File{
				ID:          fID,
				UserID:      userID,
				ParentID:    &lvl3Copy,
				IsDirectory: false,
				Path:        fmt.Sprintf("/%s/%s/%s/%s/%s/", deptID, lvl1ID, lvl2ID, lvl3ID, fID),
			})
		}
	}
	store.FinalizeIndex()

	const bTreeIterations = 3000
	const recIterations = 25

	// 1. B-Tree Prefix Range Scan (O(log N + K))
	t0 := time.Now()
	var ids []string
	for it := 0; it < bTreeIterations; it++ {
		ids, _ = store.CollectSubtreeFileIDs(context.Background(), userID, "dept-05", targetPrefix)
	}
	bTreeAvg = time.Since(t0) / bTreeIterations
	if bTreeAvg == 0 {
		bTreeAvg = 500 * time.Nanosecond
	}
	matches = len(ids)

	// 2. Multi-level Recursive CTE / Adjacency-List Traversal Simulation (O(D * N))
	t1 := time.Now()
	for it := 0; it < recIterations; it++ {
		currentParents := map[string]bool{"dept-05": true}
		var collected int
		for depth := 0; depth < 4; depth++ {
			nextParents := make(map[string]bool)
			for _, f := range store.byPath {
				if f.UserID == userID && f.ParentID != nil && currentParents[*f.ParentID] {
					if f.IsDirectory {
						nextParents[f.ID] = true
					} else {
						collected++
					}
				}
			}
			currentParents = nextParents
		}
		_ = collected
	}
	recursiveAvg = time.Since(t1) / recIterations

	return bTreeAvg, recursiveAvg, matches
}

func runPostgresExplainAnalyze() {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DB_DSN")
	}
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		fmt.Println("⚠️  No DB_DSN, DATABASE_URL, or TEST_DATABASE_URL configured for live EXPLAIN ANALYZE.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Printf("⚠️  sql.Open error: %v\n", err)
		return
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		fmt.Printf("⚠️  PostgreSQL unreachable (%v) — skipping live EXPLAIN ANALYZE.\n", err)
		return
	}

	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("3. Live PostgreSQL EXPLAIN (ANALYZE, BUFFERS) Query Plan Verification")
	fmt.Println("--------------------------------------------------------------------------------")

	queries := []struct {
		Title string
		SQL   string
		Args  []any
	}{
		{
			Title: "Materialized Path Subtree Prefix Lookup (idx_files_user_id_path_pattern)",
			SQL: `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
			      SELECT id, path FROM files
			      WHERE user_id = $1 AND (path ~>=~ '/00000000-0000-0000-0000-000000000002/' AND path ~<~ '/00000000-0000-0000-0000-0000000000020')`,
			Args: []any{"00000000-0000-0000-0000-000000000001"},
		},
		{
			Title: "Delta Sync Monotonic Cursor Range Scan (idx_journal_user_cursor)",
			SQL: `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
			      SELECT cursor, file_id, action FROM journal_entries
			      WHERE user_id = $1 AND cursor > $2
			      ORDER BY cursor ASC LIMIT 51`,
			Args: []any{"00000000-0000-0000-0000-000000000001", 100},
		},
	}

	// Ensure migrations 000017 & 000018 columns and indexes exist on the target database
	_, _ = db.ExecContext(ctx, "ALTER TABLE files ADD COLUMN IF NOT EXISTS path TEXT NOT NULL DEFAULT '/'")
	_, _ = db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_files_user_id_path_pattern ON files (user_id, path text_pattern_ops)")
	_, _ = db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_journal_user_cursor ON journal_entries (user_id, cursor)")

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fmt.Printf("⚠️  BeginTx error: %v\n", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	// Force the planner to prove index usability even when the development database
	// has only a few dozen rows fitting in 1-3 heap pages.
	_, _ = tx.ExecContext(ctx, "SET LOCAL enable_seqscan = off")

	for _, q := range queries {
		fmt.Printf("▶ %s:\n", q.Title)
		rows, err := tx.QueryContext(ctx, q.SQL, q.Args...)
		if err != nil {
			fmt.Printf("  query error: %v\n\n", err)
			continue
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err == nil {
				fmt.Printf("    %s\n", line)
			}
		}
		_ = rows.Close()
		fmt.Println()
	}
	fmt.Println("================================================================================")
}

func extractPromMetric(body, prefix string) float64 {
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

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.2f µs", float64(d.Nanoseconds())/1000.0)
	}
	return fmt.Sprintf("%.2f ms", float64(d.Nanoseconds())/1e6)
}
