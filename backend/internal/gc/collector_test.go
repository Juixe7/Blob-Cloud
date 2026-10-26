package gc_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/gc"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type fakeBlockLister struct{ keys []string }

func (f *fakeBlockLister) ListBlockKeys(_ context.Context) ([]string, error) {
	return f.keys, nil
}

type fakeDBHashes struct{ hashes []string }

func (f *fakeDBHashes) AllBlockHashes(_ context.Context) ([]string, error) {
	return f.hashes, nil
}
func (f *fakeDBHashes) ExpireStaleUploadSessions(context.Context, bool) (int64, error) { return 0, nil }
func (f *fakeDBHashes) WithBlockCandidate(_ context.Context, hash string, _ bool, deleteObject func() error) (bool, error) {
	for _, known := range f.hashes {
		if known == hash {
			return false, nil
		}
	}
	if err := deleteObject(); err != nil {
		return true, err
	}
	return true, nil
}
func (f *fakeDBHashes) WithStagingCandidate(_ context.Context, _ string, _ bool, deleteObject func() error) (bool, error) {
	if err := deleteObject(); err != nil {
		return true, err
	}
	return true, nil
}

type fakeDeleter struct {
	deleted []string
	failOn  string // if non-empty, return error for this key
}

func (f *fakeDeleter) DeleteObject(_ context.Context, key string) error {
	if key == f.failOn {
		return errors.New("simulated delete failure")
	}
	f.deleted = append(f.deleted, key)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func silentLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func buildCollector(lister gc.BlockLister, db gc.DBBlockHashes, deleter gc.Deleter, dryRun bool) *gc.Collector {
	cfg := gc.Config{DryRun: dryRun}
	return gc.New(lister, db, deleter, cfg, silentLog())
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestCollector_NoOrphans verifies that when every storage key has a matching
// DB hash, Run reports 0 orphans and deletes nothing.
func TestCollector_NoOrphans(t *testing.T) {
	lister := &fakeBlockLister{keys: []string{"blocks/aaa", "blocks/bbb"}}
	db := &fakeDBHashes{hashes: []string{"aaa", "bbb"}}
	deleter := &fakeDeleter{}

	res, err := buildCollector(lister, db, deleter, false).Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StorageKeysScanned != 2 {
		t.Errorf("StorageKeysScanned: want 2, got %d", res.StorageKeysScanned)
	}
	if res.OrphansFound != 0 {
		t.Errorf("OrphansFound: want 0, got %d", res.OrphansFound)
	}
	if res.OrphansDeleted != 0 {
		t.Errorf("OrphansDeleted: want 0, got %d", res.OrphansDeleted)
	}
	if len(deleter.deleted) != 0 {
		t.Errorf("unexpected deletes: %v", deleter.deleted)
	}
}

// TestCollector_DryRun verifies that orphans are identified but NOT deleted
// when DryRun=true.
func TestCollector_DryRun(t *testing.T) {
	lister := &fakeBlockLister{keys: []string{"blocks/known", "blocks/orphan1", "blocks/orphan2"}}
	db := &fakeDBHashes{hashes: []string{"known"}}
	deleter := &fakeDeleter{}

	res, err := buildCollector(lister, db, deleter, true /* dry-run */).Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OrphansFound != 2 {
		t.Errorf("OrphansFound: want 2, got %d", res.OrphansFound)
	}
	if res.OrphansDeleted != 0 {
		t.Errorf("OrphansDeleted in dry-run: want 0, got %d", res.OrphansDeleted)
	}
	if len(deleter.deleted) != 0 {
		t.Errorf("dry-run must not call DeleteObject, but got: %v", deleter.deleted)
	}
}

// TestCollector_LiveDelete verifies that orphaned objects are deleted and the
// result counters reflect the outcome.
func TestCollector_LiveDelete(t *testing.T) {
	lister := &fakeBlockLister{keys: []string{"blocks/known", "blocks/orphan"}}
	db := &fakeDBHashes{hashes: []string{"known"}}
	deleter := &fakeDeleter{}

	res, err := buildCollector(lister, db, deleter, false /* live */).Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OrphansFound != 1 {
		t.Errorf("OrphansFound: want 1, got %d", res.OrphansFound)
	}
	if res.OrphansDeleted != 1 {
		t.Errorf("OrphansDeleted: want 1, got %d", res.OrphansDeleted)
	}
	if res.Errors != 0 {
		t.Errorf("Errors: want 0, got %d", res.Errors)
	}
	if len(deleter.deleted) != 1 || deleter.deleted[0] != "blocks/orphan" {
		t.Errorf("deleted keys: want [blocks/orphan], got %v", deleter.deleted)
	}
}

// TestCollector_DeleteError verifies that a delete failure is counted as an
// error and does NOT abort the rest of the sweep (other orphans are still
// processed).
func TestCollector_DeleteError(t *testing.T) {
	lister := &fakeBlockLister{keys: []string{"blocks/orphan1", "blocks/orphan2"}}
	db := &fakeDBHashes{hashes: []string{}}
	deleter := &fakeDeleter{failOn: "blocks/orphan1"}

	res, err := buildCollector(lister, db, deleter, false).Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OrphansFound != 2 {
		t.Errorf("OrphansFound: want 2, got %d", res.OrphansFound)
	}
	if res.Errors != 1 {
		t.Errorf("Errors: want 1 (the failing delete), got %d", res.Errors)
	}
	// orphan2 was still deleted despite orphan1 failing
	if res.OrphansDeleted != 1 {
		t.Errorf("OrphansDeleted: want 1 (orphan2), got %d", res.OrphansDeleted)
	}
}

// TestCollector_EmptyStorage verifies a run against an empty storage bucket
// produces a valid zero-orphan result without error.
func TestCollector_EmptyStorage(t *testing.T) {
	lister := &fakeBlockLister{keys: []string{}}
	db := &fakeDBHashes{hashes: []string{"some_hash"}}
	deleter := &fakeDeleter{}

	res, err := buildCollector(lister, db, deleter, false).Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StorageKeysScanned != 0 {
		t.Errorf("StorageKeysScanned: want 0, got %d", res.StorageKeysScanned)
	}
	if res.OrphansFound != 0 {
		t.Errorf("OrphansFound: want 0, got %d", res.OrphansFound)
	}
}

type fakeStagingLister struct {
	blockKeys   []string
	stagingKeys []string
}

func (f *fakeStagingLister) ListBlockKeys(_ context.Context) ([]string, error) {
	return f.blockKeys, nil
}

func (f *fakeStagingLister) ListStagingKeys(_ context.Context) ([]string, error) {
	return f.stagingKeys, nil
}

func TestCollector_SweepsAbandonedStaging(t *testing.T) {
	lister := &fakeStagingLister{
		blockKeys:   []string{"blocks/aaa"},
		stagingKeys: []string{"staging/abandoned-sess-1/0", "staging/abandoned-sess-2/0"},
	}
	db := &fakeDBHashes{hashes: []string{"aaa"}}
	deleter := &fakeDeleter{}

	c := gc.New(lister, db, deleter, gc.Config{DryRun: false}, silentLog())
	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.StagingFound != 2 {
		t.Errorf("StagingFound: want 2, got %d", res.StagingFound)
	}
	if res.StagingDeleted != 2 {
		t.Errorf("StagingDeleted: want 2, got %d", res.StagingDeleted)
	}
	if len(deleter.deleted) != 2 {
		t.Errorf("total deleted: want 2, got %d", len(deleter.deleted))
	}
}

type agedFakeLister struct {
	blocks  []domain.StorageObject
	staging []domain.StorageObject
}

func (f *agedFakeLister) ListBlockKeys(context.Context) ([]string, error)   { return nil, nil }
func (f *agedFakeLister) ListStagingKeys(context.Context) ([]string, error) { return nil, nil }
func (f *agedFakeLister) ListBlockObjects(context.Context) ([]domain.StorageObject, error) {
	return f.blocks, nil
}
func (f *agedFakeLister) ListStagingObjects(context.Context) ([]domain.StorageObject, error) {
	return f.staging, nil
}

func TestCollector_EnforcesMinAgeAndPreservesActiveSessionStaging(t *testing.T) {
	now := time.Now()
	lister := &agedFakeLister{
		blocks: []domain.StorageObject{
			{Key: "blocks/young-orphan", LastModified: now.Add(-time.Minute)},
			{Key: "blocks/old-orphan", LastModified: now.Add(-2 * time.Hour)},
		},
		staging: []domain.StorageObject{
			{Key: "staging/active-session/0", LastModified: now.Add(-2 * time.Hour)},
			{Key: "staging/terminal-session/0", LastModified: now.Add(-2 * time.Hour)},
		},
	}
	checker := &gcSafetyDB{fakeDBHashes: fakeDBHashes{}, activeSessions: map[string]bool{"active-session": true}}
	deleter := &fakeDeleter{}
	c := gc.New(lister, checker, deleter, gc.Config{MinBlockAge: time.Hour}, silentLog())
	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.OrphansFound != 1 || res.StagingFound != 1 {
		t.Fatalf("found block/staging candidates = %d/%d, want 1/1", res.OrphansFound, res.StagingFound)
	}
	if res.OrphansDeleted != 1 || res.StagingDeleted != 1 {
		t.Fatalf("deleted block/staging = %d/%d, want 1/1", res.OrphansDeleted, res.StagingDeleted)
	}
	for _, key := range deleter.deleted {
		if key == "blocks/young-orphan" || key == "staging/active-session/0" {
			t.Fatalf("GC deleted a young or active-session object: %v", deleter.deleted)
		}
	}
	dryRunDeleter := &fakeDeleter{}
	dryRun, err := gc.New(lister, checker, dryRunDeleter, gc.Config{DryRun: true, MinBlockAge: time.Hour}, silentLog()).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.OrphansFound != res.OrphansFound || dryRun.StagingFound != res.StagingFound {
		t.Fatalf("dry-run/live candidate mismatch: dry=%+v live=%+v", dryRun, res)
	}
	if len(dryRunDeleter.deleted) != 0 {
		t.Fatalf("dry-run deleted objects: %v", dryRunDeleter.deleted)
	}
}

type gcSafetyDB struct {
	fakeDBHashes
	activeSessions map[string]bool
}

func (d *gcSafetyDB) WithBlockCandidate(ctx context.Context, hash string, dryRun bool, deleteObject func() error) (bool, error) {
	return d.fakeDBHashes.WithBlockCandidate(ctx, hash, dryRun, deleteObject)
}

func (d *gcSafetyDB) WithStagingCandidate(_ context.Context, sessionID string, _ bool, deleteObject func() error) (bool, error) {
	if d.activeSessions[sessionID] {
		return false, nil
	}
	if err := deleteObject(); err != nil {
		return true, err
	}
	return true, nil
}

// concurrent3TableState models the 3-table reference check (file_blocks,
// file_versions, and active upload session_blocks) alongside S3 block storage
// while concurrent upload sessions and GC sweeps run in parallel.
type concurrent3TableState struct {
	mu             sync.RWMutex
	storageObjects map[string]bool // "blocks/<hash>" -> present
	fileBlocks     map[string]bool // active file_blocks hashes
	versionBlocks  map[string]bool // historical file_versions hashes
	sessionBlocks  map[string]bool // in-flight upload session_blocks hashes
	deletedKeys    []string
}

func (s *concurrent3TableState) ListBlockKeys(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.storageObjects))
	for k := range s.storageObjects {
		keys = append(keys, k)
	}
	return keys, nil
}

// AllBlockHashes mirrors the production 3-table UNION query across
// file_blocks, file_versions, and upload session_blocks.
func (s *concurrent3TableState) AllBlockHashes(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	union := make(map[string]struct{})
	for h := range s.fileBlocks {
		union[h] = struct{}{}
	}
	for h := range s.versionBlocks {
		union[h] = struct{}{}
	}
	for h := range s.sessionBlocks {
		union[h] = struct{}{}
	}
	out := make([]string, 0, len(union))
	for h := range union {
		out = append(out, h)
	}
	return out, nil
}
func (s *concurrent3TableState) ExpireStaleUploadSessions(context.Context, bool) (int64, error) {
	return 0, nil
}

func (s *concurrent3TableState) WithBlockCandidate(_ context.Context, hash string, _ bool, deleteObject func() error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileBlocks[hash] || s.versionBlocks[hash] || s.sessionBlocks[hash] {
		return false, nil
	}
	if err := deleteObject(); err != nil {
		return true, err
	}
	return true, nil
}

func (s *concurrent3TableState) WithStagingCandidate(_ context.Context, _ string, _ bool, deleteObject func() error) (bool, error) {
	return true, deleteObject()
}

func (s *concurrent3TableState) DeleteObject(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.storageObjects, key)
	s.deletedKeys = append(s.deletedKeys, key)
	return nil
}

// lockedConcurrentDeleter is used only inside WithBlockCandidate, which holds
// the state mutex while modeling PostgreSQL's candidate lock transaction.
type lockedConcurrentDeleter struct{ state *concurrent3TableState }

func (d *lockedConcurrentDeleter) DeleteObject(_ context.Context, key string) error {
	delete(d.state.storageObjects, key)
	d.state.deletedKeys = append(d.state.deletedKeys, key)
	return nil
}

// TestCollector_ConcurrentGCVsInFlightUploadSessionsSafety runs continuous GC
// sweeps concurrently with 30 simulated upload workers that transition blocks from
// in-flight session_blocks -> committed file_blocks -> archived file_versions.
// Verifies under -race that GC deletes true orphans while NEVER deleting any block
// referenced by an in-flight upload session, active file, or historical version.
func TestCollector_ConcurrentGCVsInFlightUploadSessionsSafety(t *testing.T) {
	state := &concurrent3TableState{
		storageObjects: make(map[string]bool),
		fileBlocks:     make(map[string]bool),
		versionBlocks:  make(map[string]bool),
		sessionBlocks:  make(map[string]bool),
	}

	// Seed 10 true orphan blocks in S3 storage (not in any of the 3 DB tables)
	for i := 0; i < 10; i++ {
		state.storageObjects[fmt.Sprintf("blocks/orphan-hash-%02d", i)] = true
	}
	// Seed 10 pre-existing in-flight upload blocks (referenced ONLY in session_blocks!)
	for i := 0; i < 10; i++ {
		h := fmt.Sprintf("inflight-hash-%02d", i)
		state.sessionBlocks[h] = true
		state.storageObjects["blocks/"+h] = true
	}

	collector := gc.New(state, state, &lockedConcurrentDeleter{state: state}, gc.Config{DryRun: false}, silentLog())

	var wg sync.WaitGroup
	startGate := make(chan struct{})

	// Spawn 30 concurrent upload workers registering new in-flight blocks and promoting them
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startGate

			h := fmt.Sprintf("worker-block-%02d", workerID)
			// CRITICAL ORDER: Upload session registers the hash in session_blocks FIRST,
			// then promotes the physical object into blocks/<hash> before committing to file_blocks.
			state.mu.Lock()
			state.sessionBlocks[h] = true
			state.storageObjects["blocks/"+h] = true
			state.mu.Unlock()

			time.Sleep(time.Millisecond)

			// Transition from in-flight session_blocks -> active file_blocks (or versionBlocks)
			state.mu.Lock()
			if workerID%2 == 0 {
				state.fileBlocks[h] = true
			} else {
				state.versionBlocks[h] = true
			}
			delete(state.sessionBlocks, h)
			state.mu.Unlock()
		}(i)
	}

	// Spawn concurrent GC sweeps running simultaneously while uploads are in flight
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startGate
		for sweep := 0; sweep < 15; sweep++ {
			if _, err := collector.Run(context.Background()); err != nil {
				t.Errorf("concurrent GC sweep %d failed: %v", sweep, err)
			}
		}
	}()

	close(startGate)
	wg.Wait()

	// Final sweep to ensure all true orphans were collected
	if _, err := collector.Run(context.Background()); err != nil {
		t.Fatalf("final GC sweep: %v", err)
	}

	state.mu.RLock()
	defer state.mu.RUnlock()

	// 1. Verify NO protected block (in-flight, active file, or version) was ever deleted by GC
	for _, deletedKey := range state.deletedKeys {
		if !strings.HasPrefix(deletedKey, "blocks/orphan-hash-") {
			t.Fatalf("CRITICAL DATA LOSS: GC deleted protected block %s during concurrent upload!", deletedKey)
		}
	}

	// 2. Verify all 10 pre-seeded in-flight blocks and all 30 worker blocks survived in storage
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("blocks/inflight-hash-%02d", i)
		if !state.storageObjects[key] {
			t.Fatalf("in-flight session block %s was missing from storage after GC", key)
		}
	}
	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("blocks/worker-block-%02d", i)
		if !state.storageObjects[key] {
			t.Fatalf("worker block %s was missing from storage after GC", key)
		}
	}

	// 3. Verify all 10 true orphans were deleted
	if len(state.deletedKeys) != 10 {
		t.Fatalf("expected exactly 10 true orphans deleted, got %d (%v)", len(state.deletedKeys), state.deletedKeys)
	}
}
