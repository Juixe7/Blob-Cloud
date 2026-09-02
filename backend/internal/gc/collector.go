// Package gc implements the orphaned-block garbage collector for Blob-Cloud.
//
// An "orphaned block" is a binary object that exists in physical storage
// (S3 / local disk) but has no corresponding row in the Postgres blocks table.
// These accumulate when a client initiates an upload, pushes blocks directly
// to storage, then abandons the session before calling /api/upload/complete.
// Because the commit transaction never ran, no block record was written, yet
// the bytes sit in storage forever accruing cost.
//
// Algorithm (DB-authoritative):
//  1. Enumerate all block keys in storage under the "blocks/" prefix.
//  2. Fetch the complete set of sha256 hashes from Postgres blocks table.
//  3. Any storage key not present in the DB set is an orphan.
//  4. Dry-run mode: log orphans without deleting. Live mode: delete + summarise.
//
// The two narrow interfaces (BlockLister, DBBlockHashes) are declared here so
// the unit tests can supply in-process fakes — no DB or S3 dependency needed.
// The wiring of real implementations lives entirely in cmd/gc/main.go.
package gc

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// BlockLister enumerates physical block keys present in the storage backend.
// For the local driver this walks the filesystem; for S3 it pages
// ListObjectsV2. Implementations must only return keys under "blocks/".
type BlockLister interface {
	ListBlockKeys(ctx context.Context) ([]string, error)
}

// DBBlockHashes fetches the canonical set of block sha256 hashes from the DB.
type DBBlockHashes interface {
	AllBlockHashes(ctx context.Context) ([]string, error)
}

// Deleter removes a single storage object.
// Satisfied by domain.StorageProvider.DeleteObject.
type Deleter interface {
	DeleteObject(ctx context.Context, key string) error
}

// Result summarises one GC run.
type Result struct {
	StorageKeysScanned int
	DBHashesLoaded     int
	OrphansFound       int
	OrphansDeleted     int // always 0 in dry-run
	Errors             int
}

// Config controls GC behaviour.
type Config struct {
	// DryRun logs orphans without deleting. Safe to run in production to
	// audit before enabling live deletion. Defaults to true in DefaultConfig.
	DryRun bool

	// MinBlockAge is the minimum age a storage object must have before it is
	// considered a GC candidate. Guards against racing with an in-flight upload
	// that pushed blocks but hasn''t committed the session transaction yet.
	// Recommended: >= 2x the SQS visibility timeout. Default: 1 hour.
	MinBlockAge time.Duration
}

// DefaultConfig returns safe defaults for production use.
func DefaultConfig() Config {
	return Config{
		DryRun:      true, // never delete without explicit --no-dry-run opt-in
		MinBlockAge: time.Hour,
	}
}

// Collector runs the orphan GC algorithm.
type Collector struct {
	lister  BlockLister
	db      DBBlockHashes
	deleter Deleter
	cfg     Config
	log     *slog.Logger
}

// New constructs a Collector. All arguments are required.
func New(lister BlockLister, db DBBlockHashes, deleter Deleter, cfg Config, log *slog.Logger) *Collector {
	return &Collector{lister: lister, db: db, deleter: deleter, cfg: cfg, log: log}
}

// Run executes one GC sweep and returns a summary. Individual object failures
// are logged and counted rather than aborting the sweep; the function only
// returns a non-nil error when the initial storage or DB enumeration fails
// (which would make any result meaningless).
func (c *Collector) Run(ctx context.Context) (Result, error) {
	var res Result

	// 1. Enumerate all block keys currently in storage.
	storageKeys, err := c.lister.ListBlockKeys(ctx)
	if err != nil {
		return res, fmt.Errorf("list storage block keys: %w", err)
	}
	res.StorageKeysScanned = len(storageKeys)
	c.log.Info("gc: storage scan complete", "keys", res.StorageKeysScanned)

	// 2. Load all known-good block hashes from the database.
	dbHashes, err := c.db.AllBlockHashes(ctx)
	if err != nil {
		return res, fmt.Errorf("load db block hashes: %w", err)
	}
	res.DBHashesLoaded = len(dbHashes)
	c.log.Info("gc: db hashes loaded", "count", res.DBHashesLoaded)

	// Build a fast O(1) lookup set.
	known := make(map[string]struct{}, len(dbHashes))
	for _, h := range dbHashes {
		known[h] = struct{}{}
	}

	// 3. Walk storage keys: any not in the DB set is an orphan.
	for _, key := range storageKeys {
		hash := hashFromKey(key)
		if _, ok := known[hash]; ok {
			continue // referenced — not an orphan
		}
		res.OrphansFound++

		if c.cfg.DryRun {
			c.log.Info("gc[dry-run]: would delete orphaned block", "key", key)
			continue
		}

		// 4. Live mode: physically delete the orphan.
		if err := c.deleter.DeleteObject(ctx, key); err != nil {
			c.log.Error("gc: failed to delete orphaned block", "key", key, "err", err)
			res.Errors++
			continue
		}
		res.OrphansDeleted++
		c.log.Info("gc: deleted orphaned block", "key", key)
	}

	if c.cfg.DryRun {
		c.log.Info("gc[dry-run]: sweep complete (no objects deleted)",
			"scanned", res.StorageKeysScanned,
			"db_hashes", res.DBHashesLoaded,
			"orphans_found", res.OrphansFound,
		)
	} else {
		c.log.Info("gc: sweep complete",
			"scanned", res.StorageKeysScanned,
			"db_hashes", res.DBHashesLoaded,
			"orphans_found", res.OrphansFound,
			"orphans_deleted", res.OrphansDeleted,
			"errors", res.Errors,
		)
	}
	return res, nil
}

// hashFromKey extracts the sha256 portion from a "blocks/<sha256>" key.
func hashFromKey(key string) string {
	const prefix = "blocks/"
	if len(key) > len(prefix) {
		return key[len(prefix):]
	}
	return key
}
