// Package database wires the PostgreSQL connection pool and runs schema
// migrations at startup. It depends on the pgx driver (exposed through the
// standard database/sql interface) so callers stay on the stdlib *sql.DB API.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	// Register the pgx driver under the "pgx" name usable by database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"

	"go-drive-clone/internal/config"
)

// New opens a PostgreSQL connection pool configured from cfg and returns the
// *sql.DB. The pool is sized to the configured max open/idle limits and
// connections are recycled after DBConnMaxLifetime to avoid stale sockets.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*sql.DB, error) {
	db, err := sql.Open("pgx", cfg.DBDSN)
	if err != nil {
		return nil, fmt.Errorf("open pgx pool: %w", err)
	}

	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

	// Verify the DSN actually reaches a server before we declare success.
	if err := Ping(ctx, db, 10*time.Second); err != nil {
		_ = db.Close()
		return nil, err
	}

	log.Info("postgres pool ready",
		"max_open", cfg.DBMaxOpenConns,
		"max_idle", cfg.DBMaxIdleConns,
		"max_lifetime", cfg.DBConnMaxLifetime.String(),
	)
	return db, nil
}

// Ping verifies the connection with a bounded deadline. database/sql.Ping
// itself honours the context; the timeout protects callers when the pool would
// otherwise block.
func Ping(ctx context.Context, db *sql.DB, timeout time.Duration) error {
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	return nil
}
