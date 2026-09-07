//go:build integration

package dbutil

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	appdb "github.com/justinlindh/digits/server/internal/db"
)

func openFenceTestDB(t *testing.T, maxOpen int) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	migrated, err := appdb.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	migrated.DB.SetMaxOpenConns(maxOpen)
	t.Cleanup(func() { _ = migrated.Close() })
	return migrated.DB
}

func TestRenumberReadFenceSharesOneLeaseWithSmallPool(t *testing.T) {
	db := openFenceTestDB(t, 2)
	const readers = 8
	entered := make(chan struct{}, readers)
	release := make(chan struct{})
	errs := make(chan error, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- WithRenumberReadFence(context.Background(), db, func(ctx context.Context) error {
				var one int
				if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
					return err
				}
				entered <- struct{}{}
				<-release
				return nil
			})
		}()
	}
	for range readers {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("read-fence callbacks exhausted the two-connection pool")
		}
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNestedRenumberReadFenceDoesNotDeadlockQueuedWriter(t *testing.T) {
	db := openFenceTestDB(t, 2)
	outerEntered := make(chan context.Context, 1)
	allowNested := make(chan struct{})
	outerDone := make(chan error, 1)
	go func() {
		outerDone <- WithRenumberReadFence(context.Background(), db, func(ctx context.Context) error {
			outerEntered <- ctx
			<-allowNested
			return WithRenumberReadFence(ctx, db, func(context.Context) error { return nil })
		})
	}()
	<-outerEntered
	writerStarted := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		close(writerStarted)
		writerDone <- WithRenumberWriteFence(context.Background(), db, func(*sql.Tx) error { return nil })
	}()
	<-writerStarted
	time.Sleep(50 * time.Millisecond)
	close(allowNested)
	select {
	case err := <-outerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nested read fence deadlocked behind queued writer")
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
}

func TestRenumberReadFenceDoesNotRunCallbackWithoutDatabaseLock(t *testing.T) {
	db := openFenceTestDB(t, 3)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, renumberFenceKey); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	called := false
	err = WithRenumberReadFence(ctx, db, func(context.Context) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("read fence succeeded without acquiring PostgreSQL lock")
	}
	if called {
		t.Fatal("read-fence callback ran without PostgreSQL lock")
	}
}

func TestRenumberFenceSessionLossBlocksWriterFailClosed(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	migrated, err := appdb.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrated.Close() })
	db := migrated.DB

	callbackEntered := make(chan struct{})
	releaseCallback := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		readDone <- WithRenumberReadFence(context.Background(), db, func(context.Context) error {
			close(callbackEntered)
			<-releaseCallback
			return nil
		})
	}()
	select {
	case <-callbackEntered:
	case err := <-readDone:
		t.Fatalf("read fence returned before callback: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("read fence callback did not start")
	}
	released := false
	defer func() {
		if !released {
			close(releaseCallback)
			released = true
		}
	}()

	fence := fenceFor(db)
	fence.mu.Lock()
	var backendPID int
	err = fence.tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&backendPID)
	fence.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var terminated bool
	if err := db.QueryRow(`SELECT pg_terminate_backend($1)`, backendPID).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate shared fence backend: terminated=%t err=%v", terminated, err)
	}

	remoteDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remoteDB.Close() })
	writerCallbackRan := false
	writerErr := WithRenumberWriteFence(context.Background(), remoteDB, func(*sql.Tx) error {
		writerCallbackRan = true
		return nil
	})
	if writerErr == nil {
		t.Fatal("writer succeeded after shared fence session loss")
	}
	if writerCallbackRan {
		t.Fatal("writer callback ran while an uncertain read callback remained in flight")
	}

	close(releaseCallback)
	released = true
	if err := <-readDone; err == nil {
		t.Fatal("read fence reported success after its PostgreSQL session was lost")
	}
	// Test-only operator recovery after the owning callback has stopped.
	if _, err := db.Exec(`DELETE FROM renumber_inflight_guards`); err != nil {
		t.Fatal(err)
	}
}

func TestRenumberReadFencePanicReleasesGuard(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	migrated, err := appdb.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrated.Close() })
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("read callback did not panic")
			}
		}()
		_ = WithRenumberReadFence(context.Background(), migrated.DB, func(context.Context) error {
			panic("boom")
		})
	}()
	if err := WithRenumberWriteFence(context.Background(), migrated.DB, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatalf("writer blocked after panicking callback cleanup: %v", err)
	}
}
