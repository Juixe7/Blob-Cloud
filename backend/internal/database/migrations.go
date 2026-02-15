package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"go-drive-clone/db"
)

// migrationFile describes one embedded .sql script discovered under
// db/migrations/.
type migrationFile struct {
	name string // e.g. "000001_init_schema"
	path string // fs path, e.g. "migrations/000001_init_schema.up.sql"
	kind string // "up" or "down"
	body string // file contents
}

// RunMigrations applies all embedded *.up.sql migrations in lexical order,
// tracking applied versions in a `schema_migrations` bookkeeping table.
//
// The version table is created if missing; each migration runs in its own
// transaction so a partially-applied file is rolled back cleanly.
func RunMigrations(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	if err := ensureSchemaMigrationsTable(ctx, db); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}

	files, err := loadMigrationFiles()
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}

	for _, mf := range files {
		if mf.kind != "up" {
			continue
		}
		if applied[mf.name] {
			continue
		}

		if err := runOne(ctx, db, mf); err != nil {
			return fmt.Errorf("apply migration %s: %w", mf.name, err)
		}
		log.Info("migration applied", "version", mf.name)
		applied[mf.name] = true
	}

	log.Info("migrations complete", "total_applied", len(applied))
	return nil
}

// ensureSchemaMigrationsTable creates the bookkeeping table that records which
// migration versions have already run. Idempotent.
func ensureSchemaMigrationsTable(ctx context.Context, db *sql.DB) error {
	const q = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(255) PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
	_, err := db.ExecContext(ctx, q)
	return err
}

// appliedVersions returns the set of migration names already recorded.
func appliedVersions(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

// runOne executes a single migration inside a transaction and records its
// version on success.
func runOne(ctx context.Context, db *sql.DB, mf migrationFile) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	if _, err := tx.ExecContext(ctx, mf.body); err != nil {
		return fmt.Errorf("exec %s: %w", mf.path, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, mf.name); err != nil {
		return fmt.Errorf("record version %s: %w", mf.name, err)
	}
	return tx.Commit()
}

// loadMigrationFiles reads every embedded .sql file and returns the "up" and
// "down" entries sorted by version name.
func loadMigrationFiles() ([]migrationFile, error) {
	entries, err := db.MigrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, err
	}

	var files []migrationFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		path := "migrations/" + e.Name()
		body, err := db.MigrationFiles.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		name, kind, ok := splitMigrationName(e.Name())
		if !ok {
			continue // ignore non-migration .sql files
		}
		files = append(files, migrationFile{
			name: name,
			path: path,
			kind: kind,
			body: string(body),
		})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, nil
}

// splitMigrationName parses "000001_init_schema.up.sql" into
// ("000001_init_schema", "up", true). Unrecognised shapes return ok=false.
func splitMigrationName(filename string) (name, kind string, ok bool) {
	base := strings.TrimSuffix(filename, ".sql")
	switch {
	case strings.HasSuffix(base, ".up"):
		return strings.TrimSuffix(base, ".up"), "up", true
	case strings.HasSuffix(base, ".down"):
		return strings.TrimSuffix(base, ".down"), "down", true
	default:
		return "", "", false
	}
}
