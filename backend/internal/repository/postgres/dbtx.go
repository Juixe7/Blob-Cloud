// Package postgresrepo provides concrete, database/sql-backed implementations
// of the domain repository interfaces using raw SQL over the pgx driver.
//
// Repositories depend on the DBTX interface (defined here) rather than directly
// on *sql.DB. DBTX is satisfied by both *sql.DB and *sql.Tx, so the same
// repository code runs unchanged whether it is operating against the connection
// pool or inside a caller-owned transaction. This is what lets the upload
// service execute the multi-step "complete" flow as a single atomic unit.
package postgresrepo

import (
	"context"
	"database/sql"
)

// DBTX is the minimal subset of database/sql used by every repository. Both
// *sql.DB and *sql.Tx satisfy it, so a repository struct holding a DBTX can be
// pointed at either.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// txCommitter is the optional commit/rollback capability a repository checks
// for when deciding whether it owns the transaction lifecycle.
type txCommitter interface {
	Commit() error
	Rollback() error
}

// runInTx executes fn with every repository re-pointed at a fresh transaction.
// If fn returns an error (or panics) the transaction is rolled back; otherwise
// it is committed. The receiver db must be the pool (*sql.DB).
//
// Usage from the service layer:
//
//	err := postgresrepo.RunInTx(ctx, db, func(tx postgresrepo.DBTX) error {
//	    if err := blocks.WithTx(tx).GetOrCreate(ctx, ...); err != nil { return err }
//	    return files.WithTx(tx).Create(ctx, ...)
//	})
func RunInTx(ctx context.Context, db *sql.DB, fn func(tx DBTX) error) (err error) {
	tx, beginErr := db.BeginTx(ctx, nil)
	if beginErr != nil {
		return beginErr
	}
	// Defer rollback. If commit runs first, this is a no-op; if we panic or
	// return an error, it ensures the tx cannot commit a partial result.
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
