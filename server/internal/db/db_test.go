//go:build integration

package db

import (
	"os"
	"testing"
)

func TestOpenAndMigrate(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	d, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = d.Close() }()

	// Verify all tables exist
	tables := []string{"phones", "calls", "settings", "users", "sessions", "magic_links", "schema_version"}
	for _, table := range tables {
		var count int
		err := d.DB.QueryRow(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1",
			table,
		).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("table %s not created: count=%d err=%v", table, count, err)
		}
	}
}
