// Package dbutil provides shared helpers for database query construction
// and transaction lifecycle management.
package dbutil

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// RowScanner is the subset of *sql.Row / *sql.Rows that scan-helpers depend
// on. Defined here once so per-table scan functions across packages can share
// the same single-row / multi-row abstraction without redeclaring it.
type RowScanner interface {
	Scan(dest ...any) error
}

// RowQuerier is satisfied by both *sql.DB and *sql.Tx, so single-row query
// helpers can run standalone or inside a transaction.
type RowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Placeholders returns a comma-separated string of PostgreSQL positional
// placeholders ($1, $2, …, $n) for use in SQL IN clauses.
func Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(parts, ", ")
}

// WithTx runs fn inside a database transaction. Commits on success; rolls
// back on error or panic. The committed flag guards against calling Rollback
// after a successful Commit (which lib/pq surfaces as ErrTxDone).
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}
