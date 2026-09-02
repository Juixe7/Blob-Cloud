// Command gc is the orphaned-block garbage collector for Blob-Cloud.
//
// It compares every physical object under "blocks/" in the configured storage
// backend against the Postgres blocks table, and deletes any key that has no
// corresponding row (i.e. blocks left behind by aborted upload sessions).
//
// Usage:
//
//	gc [--dry-run] [--min-age DURATION] [--env-file PATH]
//
// Flags:
//
//	--dry-run        Log orphans without deleting (default: true — safe by design).
//	                 Pass --no-dry-run to enable live deletion.
//	--min-age        Minimum object age before GC considers it orphaned.
//	                 Prevents racing with in-flight uploads. Default: 1h.
//	--env-file       Path to a .env file to load before reading env vars.
//	                 Defaults to ".env" in the current directory.
//
// Environment variables:
//   The binary reads the same env vars as the API server (DB_DSN, STORAGE_PROVIDER,
//   AWS_*, LOCAL_STORAGE_DIR, etc.) so a single .env file covers both.
//
// LP alignment:
//   Ownership    — we identified the storage-leak gap in our own README and
//                  closed it rather than leaving a known issue open.
//   Frugality    — every orphaned block left in S3/R2 costs money; this job
//                  makes storage cost proportional to actual data stored.
//   Dive Deep    — the GC distinguishes aborted uploads from legitimate blocks
//                  using the DB as the authoritative source of truth.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/joho/godotenv"

	"go-drive-clone/internal/config"
	"go-drive-clone/internal/database"
	"go-drive-clone/internal/gc"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	"go-drive-clone/internal/storage"
)

func main() {
	// ── flags ───────────────────────────────────────────────────────────────
	dryRun  := flag.Bool("dry-run", true, "log orphans without deleting (safe default)")
	minAge  := flag.Duration("min-age", time.Hour, "minimum object age to be considered an orphan")
	envFile := flag.String("env-file", ".env", "path to .env file (optional)")
	flag.Parse()

	// ── logger ──────────────────────────────────────────────────────────────
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// ── load environment ────────────────────────────────────────────────────
	if err := godotenv.Load(*envFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Error("failed to load .env file", "path", *envFile, "err", err)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	mode := "dry-run"
	if !*dryRun {
		mode = "live (WILL DELETE)"
	}
	log.Info("gc starting",
		"mode", mode,
		"min_age", minAge.String(),
		"storage_provider", cfg.StorageProvider,
	)

	ctx := context.Background()

	// ── database ────────────────────────────────────────────────────────────
	db, err := database.New(ctx, cfg, log)
	if err != nil {
		log.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("closing database pool", "err", err)
		}
	}()

	blockRepo := postgresrepo.NewBlockRepository(db)

	// ── storage backend ─────────────────────────────────────────────────────
	// The GC needs a BlockLister (to enumerate storage keys) and a Deleter
	// (to remove orphans). Both are satisfied by the concrete storage drivers.
	var lister  gc.BlockLister
	var deleter gc.Deleter

	switch cfg.StorageProvider {
	case "s3":
		s3stor, err := storage.NewS3Storage(ctx, cfg, log)
		if err != nil {
			log.Error("failed to initialise S3 storage", "err", err)
			os.Exit(1)
		}
		lister  = s3stor
		deleter = s3stor

	default: // "local" or empty
		localStor, err := storage.NewLocalStore(cfg.LocalStorageDir, cfg.BaseURL, log)
		if err != nil {
			log.Error("failed to initialise local storage", "err", err)
			os.Exit(1)
		}
		lister  = localStor
		deleter = localStor
	}

	// ── run GC ──────────────────────────────────────────────────────────────
	gcCfg := gc.Config{
		DryRun:      *dryRun,
		MinBlockAge: *minAge,
	}
	collector := gc.New(lister, blockRepo, deleter, gcCfg, log)

	result, err := collector.Run(ctx)
	if err != nil {
		log.Error("gc run failed", "err", err)
		os.Exit(1)
	}

	log.Info("gc finished",
		"mode",             mode,
		"storage_scanned",  result.StorageKeysScanned,
		"db_hashes",        result.DBHashesLoaded,
		"orphans_found",    result.OrphansFound,
		"orphans_deleted",  result.OrphansDeleted,
		"errors",           result.Errors,
	)

	// Exit non-zero if any deletions failed so cron can alert on partial runs.
	if result.Errors > 0 {
		os.Exit(2)
	}
}
