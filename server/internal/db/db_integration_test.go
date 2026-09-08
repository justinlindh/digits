//go:build integration

package db

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return dsn
}

// rawDB is an unmigrated connection for inspecting catalogs and holding
// locks from outside Open.
func rawDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return raw
}

func resetSchema(t *testing.T, raw *sql.DB) {
	t.Helper()
	if _, err := raw.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
}

func mustOpen(t *testing.T, dsn string) {
	t.Helper()
	d, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = d.Close()
}

// TestMigrateIsIdempotent locks the core contract: opening an already
// migrated database must be a no-op, not an error.
func TestMigrateIsIdempotent(t *testing.T) {
	dsn := testDSN(t)
	resetSchema(t, rawDB(t, dsn))
	mustOpen(t, dsn)
	mustOpen(t, dsn)
}

// TestMigrateRecordsEveryVersion: a fresh database ends up with every
// version in the migrations list recorded, so nothing re-runs on the next
// start.
func TestMigrateRecordsEveryVersion(t *testing.T) {
	dsn := testDSN(t)
	raw := rawDB(t, dsn)
	resetSchema(t, raw)
	mustOpen(t, dsn)
	d := &Database{DB: raw}
	applied, err := d.appliedVersions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if !applied[m.version] {
			t.Errorf("version %d not recorded in schema_version", m.version)
		}
	}
}

// TestMigrateAdoptsLegacyDatabase: a database migrated before versions
// were recorded in Go (only the versions that used to carry a DO-block gate
// are present in schema_version) is brought up to date by running the
// unrecorded versions once, and then behaves as fully migrated.
func TestMigrateAdoptsLegacyDatabase(t *testing.T) {
	dsn := testDSN(t)
	raw := rawDB(t, dsn)
	resetSchema(t, raw)
	mustOpen(t, dsn)
	if _, err := raw.Exec(`DELETE FROM schema_version WHERE version NOT IN (10,15,17,20,21,22,25,26,27,28)`); err != nil {
		t.Fatal(err)
	}
	mustOpen(t, dsn)
	var missing int
	if err := raw.QueryRow(`SELECT count(*) FROM generate_series(1, 31) v
		WHERE v <> 24 AND NOT EXISTS (SELECT 1 FROM schema_version WHERE version = v)`).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Fatalf("%d versions still unrecorded after adopting a legacy database", missing)
	}
	var phones bool
	if err := raw.QueryRow(`SELECT to_regclass('phones') IS NOT NULL`).Scan(&phones); err != nil {
		t.Fatal(err)
	}
	if phones {
		t.Fatal("legacy phones table left behind")
	}
}

// TestMigrateConcurrentStarts: several processes opening the same database
// at once (a fresh rollout, or pods rescheduled after node loss) must all
// succeed, on a fresh database and on a migrated one.
func TestMigrateConcurrentStarts(t *testing.T) {
	dsn := testDSN(t)
	raw := rawDB(t, dsn)
	openN := func(label string) {
		const n = 3
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				d, err := Open(dsn)
				if d != nil {
					_ = d.Close()
				}
				errs[i] = err
			}()
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("%s: concurrent Open %d: %v", label, i, err)
			}
		}
	}
	for range 3 {
		resetSchema(t, raw)
		openN("fresh")
	}
	for range 3 {
		openN("migrated")
	}
}

// TestMigrateSteadyStateTakesNoTableLocks: starting against a migrated
// database must not wait on any application table. A long transaction on a
// serving replica would otherwise block the new pod, and the pod's queued
// exclusive lock would in turn block every other query on that table.
func TestMigrateSteadyStateTakesNoTableLocks(t *testing.T) {
	dsn := testDSN(t)
	raw := rawDB(t, dsn)
	resetSchema(t, raw)
	mustOpen(t, dsn)

	ctx := context.Background()
	holder, err := raw.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	tx, err := holder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var tables []string
	if err := tx.QueryRow(`SELECT array_agg(tablename::text) FROM pg_tables
		WHERE schemaname = 'public' AND tablename <> 'schema_version'`).Scan(pq.Array(&tables)); err != nil {
		t.Fatal(err)
	}
	if len(tables) < 10 {
		t.Fatalf("expected the full schema, found %d tables", len(tables))
	}
	for _, name := range tables {
		if _, err := tx.Exec(`LOCK TABLE ` + name + ` IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan error, 1)
	go func() {
		d, err := Open(dsn)
		if d != nil {
			_ = d.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Open with every table locked: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open blocked on a locked application table")
	}
}

// TestMigrateDoesNotLeakColumnSlots: repeated starts must not grow the
// count of dropped attributes. Postgres caps a table at 1600 attributes
// including dropped ones, so an add/drop pair that re-runs on every start
// eventually makes the server unable to boot.
func TestMigrateDoesNotLeakColumnSlots(t *testing.T) {
	dsn := testDSN(t)
	raw := rawDB(t, dsn)
	resetSchema(t, raw)
	mustOpen(t, dsn)
	dropped := func() int {
		var n int
		if err := raw.QueryRow(`SELECT count(*) FROM pg_attribute a
			JOIN pg_class c ON c.oid = a.attrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public' AND a.attisdropped`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := dropped()
	for range 3 {
		mustOpen(t, dsn)
	}
	if after := dropped(); after != before {
		t.Fatalf("dropped attribute count grew from %d to %d across restarts", before, after)
	}
}
