//go:build integration

package admin

import (
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func TestAdminDBMigrate(t *testing.T) {
	dsn := os.Getenv("TEST_ADMIN_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_ADMIN_DATABASE_URL not set")
	}
	db, err := OpenAdmin(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	err = db.DB.QueryRow("SELECT COUNT(*) FROM admin_users").Scan(&count)
	if err != nil {
		t.Fatalf("query admin_users: %v", err)
	}

	err = db.DB.QueryRow("SELECT COUNT(*) FROM admin_sessions").Scan(&count)
	if err != nil {
		t.Fatalf("query admin_sessions: %v", err)
	}
}
