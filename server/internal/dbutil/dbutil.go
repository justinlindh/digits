// Package dbutil provides shared helpers for database query construction
// and transaction lifecycle management.
package dbutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
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

const (
	renumberFenceKey       int64 = 4920542366917519361
	renumberFenceIOTimeout       = 2 * time.Second
)

var (
	ErrRenumberFenceLost      = errors.New("renumber read fence session lost")
	ErrRenumberFenceUncertain = errors.New("renumber blocked by uncertain in-flight work")
)

type renumberFenceContextKey struct{}

type renumberFence struct {
	gate      sync.RWMutex
	mu        sync.Mutex
	refs      int
	tx        *sql.Tx
	conn      *sql.Conn
	token     string
	uncertain bool
	txCancel  context.CancelFunc
}

var renumberFences sync.Map

func fenceFor(db *sql.DB) *renumberFence {
	fence, _ := renumberFences.LoadOrStore(db, &renumberFence{})
	return fence.(*renumberFence)
}

// WithRenumberReadFence holds one process-shared PostgreSQL advisory lock for
// all concurrent identity-bound socket actions. Sharing one lease avoids
// consuming one pool connection per frame. The local RWMutex gives queued
// renumber writers priority. A context marker makes nested reads reuse the
// outer lease instead of deadlocking behind a queued writer.
func WithRenumberReadFence(ctx context.Context, db *sql.DB, fn func(context.Context) error) (retErr error) {
	fence := fenceFor(db)
	if ctx.Value(renumberFenceContextKey{}) == fence {
		return fn(ctx)
	}
	fence.gate.RLock()
	defer fence.gate.RUnlock()

	fence.mu.Lock()
	if fence.uncertain {
		fence.mu.Unlock()
		return ErrRenumberFenceLost
	}
	if fence.refs == 0 {
		tokenBytes := make([]byte, 16)
		if _, err := rand.Read(tokenBytes); err != nil {
			fence.mu.Unlock()
			return fmt.Errorf("create renumber read guard token: %w", err)
		}
		token := hex.EncodeToString(tokenBytes)
		if _, err := db.ExecContext(ctx, `INSERT INTO renumber_inflight_guards (token) VALUES ($1)`, token); err != nil {
			fence.mu.Unlock()
			return fmt.Errorf("create renumber read guard: %w", err)
		}
		cleanupGuard := func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), renumberFenceIOTimeout)
			defer cleanupCancel()
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM renumber_inflight_guards WHERE token = $1`, token)
		}
		acquireCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		conn, err := db.Conn(acquireCtx)
		cancel()
		if err != nil {
			cleanupGuard()
			fence.mu.Unlock()
			return fmt.Errorf("reserve renumber read fence connection: %w", err)
		}
		leaseCtx, leaseCancel := context.WithCancel(context.Background())
		tx, err := conn.BeginTx(leaseCtx, nil)
		if err != nil {
			leaseCancel()
			_ = conn.Close()
			cleanupGuard()
			fence.mu.Unlock()
			return fmt.Errorf("begin renumber read fence: %w", err)
		}
		lockCtx, cancelLock := context.WithTimeout(ctx, 5*time.Second)
		_, lockErr := tx.ExecContext(lockCtx, `SET LOCAL lock_timeout = $$5s$$`)
		if lockErr == nil {
			_, lockErr = tx.ExecContext(lockCtx, `SELECT pg_advisory_xact_lock_shared($1)`, renumberFenceKey)
		}
		cancelLock()
		if lockErr != nil {
			leaseCancel()
			_ = tx.Rollback()
			_ = conn.Close()
			cleanupGuard()
			fence.mu.Unlock()
			return fmt.Errorf("lock renumber read fence: %w", lockErr)
		}
		fence.tx = tx
		fence.conn = conn
		fence.token = token
		fence.txCancel = leaseCancel
	}
	fence.refs++
	fence.mu.Unlock()

	defer func() {
		fence.mu.Lock()
		probeCtx, probeCancel := context.WithTimeout(context.Background(), renumberFenceIOTimeout)
		_, probeErr := fence.tx.ExecContext(probeCtx, `SELECT 1`)
		probeCancel()
		if probeErr != nil {
			fence.uncertain = true
		}
		fence.refs--
		if fence.refs == 0 {
			if !fence.uncertain {
				deleteCtx, deleteCancel := context.WithTimeout(context.Background(), renumberFenceIOTimeout)
				_, deleteErr := fence.tx.ExecContext(deleteCtx, `DELETE FROM renumber_inflight_guards WHERE token = $1`, fence.token)
				deleteCancel()
				if deleteErr != nil {
					fence.uncertain = true
				} else {
					commitTimer := time.AfterFunc(renumberFenceIOTimeout, fence.txCancel)
					if err := fence.tx.Commit(); err != nil {
						fence.uncertain = true
					}
					commitTimer.Stop()
				}
				fence.txCancel()
			}
			if fence.txCancel != nil {
				fence.txCancel()
			}
			if fence.uncertain {
				_ = fence.tx.Rollback()
			}
			_ = fence.conn.Close()
			fence.tx = nil
			fence.conn = nil
			fence.token = ""
			fence.txCancel = nil
		}
		lost := fence.uncertain
		fence.mu.Unlock()
		if lost {
			retErr = errors.Join(retErr, ErrRenumberFenceLost)
		}
	}()
	return fn(context.WithValue(ctx, renumberFenceContextKey{}, fence))
}

// WithRenumberWriteFence runs fn in a transaction while holding the matching
// process-exclusive and PostgreSQL-exclusive fences.
func WithRenumberWriteFence(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	fence := fenceFor(db)
	fence.gate.Lock()
	defer fence.gate.Unlock()
	return WithTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, renumberFenceKey); err != nil {
			return fmt.Errorf("lock renumber write fence: %w", err)
		}
		var guarded bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM renumber_inflight_guards)`).Scan(&guarded); err != nil {
			return fmt.Errorf("check renumber in-flight guards: %w", err)
		}
		if guarded {
			return ErrRenumberFenceUncertain
		}
		return fn(tx)
	})
}
