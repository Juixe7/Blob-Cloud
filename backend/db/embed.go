// Package db holds assets compiled into the binary via Go's embed package.
//
// Migration SQL files live under db/migrations/ and are embedded so the
// resulting binary is self-contained: a single artifact can ship to EC2 and
// initialise its own database without requiring .sql files on disk.
package db

import "embed"

// MigrationFiles is the embedded filesystem containing all *.sql migration
// scripts. The migration runner in internal/database reads from this.
//
//go:embed migrations/*.sql
var MigrationFiles embed.FS
