//go:build integration

package db

import (
	"os"
	"testing"
)

// TestMigrateIsIdempotent locks the core contract the whole migrate
// mechanism rests on: running migrations against a database that has
// already been fully migrated must be a no-op, not an error. Open runs
// migrate internally, so opening the same database twice exercises the
// second migrate pass over an already-migrated schema.
//
// Skips when TEST_DATABASE_URL is unset, matching the convention in the
// other integration tests so `go test -tags=integration` is safe without
// a Postgres handy.
func TestMigrateIsIdempotent(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	first, err := Open(dsn)
	if err != nil {
		t.Fatalf("first Open (initial migrate): %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	// Second Open over the same, already-migrated database. This is the
	// assertion and it mirrors the production failure mode exactly: a
	// fresh process booting against an existing DB re-runs every migration
	// statement, which must be a no-op (IF NOT EXISTS guards,
	// schema_version gates) rather than an error.
	second, err := Open(dsn)
	if err != nil {
		t.Fatalf("second Open (re-running migrations must be idempotent): %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
}
