package dbutil

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// txRecorder tracks transaction lifecycle calls for one fake database and
// lets tests inject Begin/Commit failures.
type txRecorder struct {
	mu         sync.Mutex
	begun      int
	committed  int
	rolledBack int
	beginErr   error
	commitErr  error
}

type fakeDriver struct {
	mu        sync.Mutex
	recorders map[string]*txRecorder
}

var testDriver = &fakeDriver{recorders: map[string]*txRecorder{}}

func init() {
	sql.Register("dbutil-fake", testDriver)
}

// openFake returns a *sql.DB backed by the fake driver plus the recorder
// observing its transactions. Each test gets an isolated recorder via a
// unique DSN.
func openFake(t *testing.T, name string) (*sql.DB, *txRecorder) {
	t.Helper()
	rec := &txRecorder{}
	testDriver.mu.Lock()
	testDriver.recorders[name] = rec
	testDriver.mu.Unlock()
	db, err := sql.Open("dbutil-fake", name)
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, rec
}

func (d *fakeDriver) Open(name string) (driver.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.recorders[name]
	if !ok {
		return nil, fmt.Errorf("unknown fake dsn %q", name)
	}
	return &fakeConn{rec: rec}, nil
}

type fakeConn struct{ rec *txRecorder }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (c *fakeConn) Close() error                        { return nil }

func (c *fakeConn) Begin() (driver.Tx, error) {
	c.rec.mu.Lock()
	defer c.rec.mu.Unlock()
	if c.rec.beginErr != nil {
		return nil, c.rec.beginErr
	}
	c.rec.begun++
	return &fakeTx{rec: c.rec}, nil
}

type fakeTx struct{ rec *txRecorder }

func (t *fakeTx) Commit() error {
	t.rec.mu.Lock()
	defer t.rec.mu.Unlock()
	if t.rec.commitErr != nil {
		return t.rec.commitErr
	}
	t.rec.committed++
	return nil
}

func (t *fakeTx) Rollback() error {
	t.rec.mu.Lock()
	defer t.rec.mu.Unlock()
	t.rec.rolledBack++
	return nil
}

func TestWithTxCommitsOnSuccess(t *testing.T) {
	db, rec := openFake(t, t.Name())
	if err := WithTx(context.Background(), db, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatalf("WithTx: %v", err)
	}
	if rec.committed != 1 || rec.rolledBack != 0 {
		t.Errorf("committed=%d rolledBack=%d, want 1/0", rec.committed, rec.rolledBack)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	db, rec := openFake(t, t.Name())
	sentinel := errors.New("boom")
	err := WithTx(context.Background(), db, func(*sql.Tx) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx error = %v, want sentinel passed through", err)
	}
	if rec.committed != 0 || rec.rolledBack != 1 {
		t.Errorf("committed=%d rolledBack=%d, want 0/1", rec.committed, rec.rolledBack)
	}
}

func TestWithTxRollsBackOnPanic(t *testing.T) {
	db, rec := openFake(t, t.Name())
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic to propagate")
		}
		if rec.committed != 0 || rec.rolledBack != 1 {
			t.Errorf("committed=%d rolledBack=%d, want 0/1", rec.committed, rec.rolledBack)
		}
	}()
	_ = WithTx(context.Background(), db, func(*sql.Tx) error { panic("boom") })
}

func TestWithTxWrapsBeginError(t *testing.T) {
	db, rec := openFake(t, t.Name())
	rec.beginErr = errors.New("no conn")
	err := WithTx(context.Background(), db, func(*sql.Tx) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "begin tx") {
		t.Fatalf("WithTx error = %v, want wrapped begin error", err)
	}
}

func TestWithTxWrapsCommitError(t *testing.T) {
	db, rec := openFake(t, t.Name())
	rec.commitErr = errors.New("disk full")
	err := WithTx(context.Background(), db, func(*sql.Tx) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "commit tx") {
		t.Fatalf("WithTx error = %v, want wrapped commit error", err)
	}
	// The deferred rollback fires because committed stayed false; the driver
	// records it, which is fine. The important part is no double-commit.
	if rec.committed != 0 {
		t.Errorf("committed=%d, want 0", rec.committed)
	}
}
