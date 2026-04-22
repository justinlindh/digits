//go:build integration

package calls_test

import (
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/db"
)

// openTestDB opens the test database via TEST_DATABASE_URL and registers
// cleanup to delete calls/conference rows and close the connection.
// Skips the test if the env var is unset.
func openTestDB(t *testing.T) *db.Database {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.DB.Exec("DELETE FROM conference_members")
		_, _ = d.DB.Exec("DELETE FROM conferences")
		_, _ = d.DB.Exec("DELETE FROM calls")
		_ = d.Close()
	})
	return d
}
