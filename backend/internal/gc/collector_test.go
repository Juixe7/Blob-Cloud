package gc_test

import (
	"context"
	"errors"
	"log/slog"
	"io"
	"testing"
	"time"

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
	cfg := gc.Config{DryRun: dryRun, MinBlockAge: time.Hour}
	return gc.New(lister, db, deleter, cfg, silentLog())
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestCollector_NoOrphans verifies that when every storage key has a matching
// DB hash, Run reports 0 orphans and deletes nothing.
func TestCollector_NoOrphans(t *testing.T) {
	lister  := &fakeBlockLister{keys: []string{"blocks/aaa", "blocks/bbb"}}
	db      := &fakeDBHashes{hashes: []string{"aaa", "bbb"}}
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
	lister  := &fakeBlockLister{keys: []string{"blocks/known", "blocks/orphan1", "blocks/orphan2"}}
	db      := &fakeDBHashes{hashes: []string{"known"}}
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
	lister  := &fakeBlockLister{keys: []string{"blocks/known", "blocks/orphan"}}
	db      := &fakeDBHashes{hashes: []string{"known"}}
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
	lister  := &fakeBlockLister{keys: []string{"blocks/orphan1", "blocks/orphan2"}}
	db      := &fakeDBHashes{hashes: []string{}}
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
	lister  := &fakeBlockLister{keys: []string{}}
	db      := &fakeDBHashes{hashes: []string{"some_hash"}}
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
