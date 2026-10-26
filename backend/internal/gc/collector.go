// Package gc implements the orphaned-block garbage collector for Blob-Cloud.
//
// An "orphaned block" is a binary object that exists in physical storage
// (S3 / local disk) but has no active file, version, or upload-session reference.
// These accumulate when a client initiates an upload, pushes blocks directly
// to storage, then abandons the session before calling /api/upload/complete.
// Because the commit transaction never ran, no block record was written, yet
// the bytes sit in storage forever accruing cost.
//
// Algorithm (DB-authoritative):
//  1. Enumerate all block keys in storage under the "blocks/" prefix.
//  2. Fetch active block references from PostgreSQL.
//  3. Filter candidates by age, then take a per-hash DB lock and recheck refs.
//  4. Keep the lock through metadata/object deletion; dry-run uses the same check.
//
// The two narrow interfaces (BlockLister, DBBlockHashes) are declared here so
// the unit tests can supply in-process fakes — no DB or S3 dependency needed.
// The wiring of real implementations lives entirely in cmd/gc/main.go.
package gc

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go-drive-clone/internal/domain"
)

// BlockLister enumerates physical block keys present in the storage backend.
// For the local driver this walks the filesystem; for S3 it pages
// ListObjectsV2. Implementations must only return keys under "blocks/".
type BlockLister interface {
	ListBlockKeys(ctx context.Context) ([]string, error)
}

// ObjectLister provides storage modification times. GC fails closed when a
// backend cannot provide age metadata and MinBlockAge is positive.
type ObjectLister interface {
	ListBlockObjects(ctx context.Context) ([]domain.StorageObject, error)
	ListStagingObjects(ctx context.Context) ([]domain.StorageObject, error)
}

// StagingLister enumerates temporary keys under "staging/".
// Opt-in interface: when supported, abandoned staging uploads are swept during GC.
type StagingLister interface {
	ListStagingKeys(ctx context.Context) ([]string, error)
}

// DBBlockHashes fetches the canonical set of block sha256 hashes from the DB.
type DBBlockHashes interface {
	AllBlockHashes(ctx context.Context) ([]string, error)
}

// CandidateChecker revalidates candidates against live database state just
// before deletion. It protects blocks used by active sessions after the initial
// inventory snapshot.
type CandidateChecker interface {
	ExpireStaleUploadSessions(ctx context.Context, dryRun bool) (int64, error)
	WithBlockCandidate(ctx context.Context, hash string, dryRun bool, deleteObject func() error) (bool, error)
	WithStagingCandidate(ctx context.Context, sessionID string, dryRun bool, deleteObject func() error) (bool, error)
}

// Deleter removes a single storage object.
// Satisfied by domain.StorageProvider.DeleteObject.
type Deleter interface {
	DeleteObject(ctx context.Context, key string) error
}

// Result summarises one GC run.
type Result struct {
	StorageKeysScanned   int
	DBHashesLoaded       int
	OrphansFound         int
	OrphansDeleted       int // always 0 in dry-run
	StagingFound         int
	StagingDeleted       int
	StaleSessionsFound   int64
	StaleSessionsAborted int64
	Errors               int
}

// Config controls GC behaviour.
type Config struct {
	// DryRun logs orphans without deleting. Safe to run in production to
	// audit before enabling live deletion. Defaults to true in DefaultConfig.
	DryRun bool

	// MinBlockAge is the minimum age a storage object must have before it is
	// considered a GC candidate. Guards against racing with an in-flight upload
	// that pushed blocks but hasn''t committed the session transaction yet.
	// This grace period limits cleanup of recently written objects. Correctness
	// comes from the shared PostgreSQL hash lock and final reference check.
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
	checker, ok := c.db.(CandidateChecker)
	if !ok {
		return res, fmt.Errorf("database backend does not provide coordinated GC candidate checks")
	}
	var err error
	res.StaleSessionsFound, err = checker.ExpireStaleUploadSessions(ctx, c.cfg.DryRun)
	if err != nil {
		return res, fmt.Errorf("expire stale upload sessions: %w", err)
	}
	if !c.cfg.DryRun {
		res.StaleSessionsAborted = res.StaleSessionsFound
	}

	// 1. Enumerate all block keys currently in storage.
	var storageKeys []string
	objectAges := map[string]time.Time{}
	if aged, ok := c.lister.(ObjectLister); ok {
		objects, listErr := aged.ListBlockObjects(ctx)
		if listErr != nil {
			return res, fmt.Errorf("list storage block metadata: %w", listErr)
		}
		storageKeys = make([]string, 0, len(objects))
		for _, obj := range objects {
			if obj.LastModified.IsZero() {
				return res, fmt.Errorf("storage backend returned no modification time for %q", obj.Key)
			}
			storageKeys = append(storageKeys, obj.Key)
			objectAges[obj.Key] = obj.LastModified
		}
	} else {
		storageKeys, err = c.lister.ListBlockKeys(ctx)
		if err != nil {
			return res, fmt.Errorf("list storage block keys: %w", err)
		}
		if c.cfg.MinBlockAge > 0 {
			return res, fmt.Errorf("storage backend does not provide object ages required by MinBlockAge")
		}
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
		if c.cfg.MinBlockAge > 0 && time.Since(objectAges[key]) < c.cfg.MinBlockAge {
			continue
		}
		eligible, checkErr := checker.WithBlockCandidate(ctx, hash, c.cfg.DryRun, func() error {
			if c.cfg.DryRun {
				return nil
			}
			return c.deleter.DeleteObject(ctx, key)
		})
		if eligible {
			res.OrphansFound++
		}
		if checkErr != nil {
			c.log.Error("gc: coordinated block candidate check failed", "key", key, "err", checkErr)
			res.Errors++
			continue
		}
		if !eligible {
			continue
		}
		if c.cfg.DryRun {
			c.log.Info("gc[dry-run]: would delete orphaned block", "key", key)
			continue
		}
		res.OrphansDeleted++
		c.log.Info("gc: deleted orphaned block", "key", key)
	}

	// 5. Sweep abandoned staging objects if lister implements StagingLister.
	if sLister, ok := c.lister.(StagingLister); ok {
		var stagingKeys []string
		var sErr error
		stagingAges := map[string]time.Time{}
		if aged, ok := c.lister.(ObjectLister); ok {
			objects, listErr := aged.ListStagingObjects(ctx)
			if listErr != nil {
				return res, fmt.Errorf("list staging object metadata: %w", listErr)
			}
			stagingKeys = make([]string, 0, len(objects))
			for _, obj := range objects {
				if obj.LastModified.IsZero() {
					return res, fmt.Errorf("storage backend returned no modification time for staging object %q", obj.Key)
				}
				stagingKeys = append(stagingKeys, obj.Key)
				stagingAges[obj.Key] = obj.LastModified
			}
		} else {
			stagingKeys, sErr = sLister.ListStagingKeys(ctx)
			if c.cfg.MinBlockAge > 0 {
				return res, fmt.Errorf("storage backend does not provide staging object ages required by MinBlockAge")
			}
		}
		if sErr != nil {
			return res, fmt.Errorf("list staging storage keys: %w", sErr)
		}
		if sErr == nil {
			for _, sKey := range stagingKeys {
				if c.cfg.MinBlockAge > 0 && time.Since(stagingAges[sKey]) < c.cfg.MinBlockAge {
					continue
				}
				sessionID := stagingSessionID(sKey)
				if sessionID == "" {
					continue
				}
				eligible, checkErr := checker.WithStagingCandidate(ctx, sessionID, c.cfg.DryRun, func() error {
					if c.cfg.DryRun {
						return nil
					}
					return c.deleter.DeleteObject(ctx, sKey)
				})
				if eligible {
					res.StagingFound++
				}
				if checkErr != nil {
					c.log.Error("gc: coordinated staging candidate check failed", "key", sKey, "err", checkErr)
					res.Errors++
					continue
				}
				if !eligible {
					continue
				}
				if c.cfg.DryRun {
					c.log.Info("gc[dry-run]: would delete abandoned staging object", "key", sKey)
					continue
				}
				res.StagingDeleted++
				c.log.Info("gc: deleted abandoned staging object", "key", sKey)
			}
		}
	}

	if c.cfg.DryRun {
		c.log.Info("gc[dry-run]: sweep complete (no objects deleted)",
			"scanned", res.StorageKeysScanned,
			"db_hashes", res.DBHashesLoaded,
			"orphans_found", res.OrphansFound,
			"staging_found", res.StagingFound,
			"stale_sessions_aborted", res.StaleSessionsAborted,
			"stale_sessions_would_abort", res.StaleSessionsFound,
		)
	} else {
		c.log.Info("gc: sweep complete",
			"scanned", res.StorageKeysScanned,
			"db_hashes", res.DBHashesLoaded,
			"orphans_found", res.OrphansFound,
			"orphans_deleted", res.OrphansDeleted,
			"staging_found", res.StagingFound,
			"staging_deleted", res.StagingDeleted,
			"stale_sessions_aborted", res.StaleSessionsAborted,
			"stale_sessions_found", res.StaleSessionsFound,
			"errors", res.Errors,
		)
	}
	return res, nil
}

func stagingSessionID(key string) string {
	const prefix = "staging/"
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

// hashFromKey extracts the sha256 portion from a "blocks/<sha256>" key.
func hashFromKey(key string) string {
	const prefix = "blocks/"
	if len(key) > len(prefix) {
		return key[len(prefix):]
	}
	return key
}
