//go:build integration

package calls_test

import (
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/db"
)

// openTestDB opens the test database via TEST_DATABASE_URL and registers
// cleanup that removes every calls/conference row and closes the connection.
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
		// calls.originating_conference_id and conferences.originating_call_id
		// reference each other, so break the cycle before deleting either side.
		for _, q := range []string{
			"UPDATE calls SET originating_conference_id = NULL WHERE originating_conference_id IS NOT NULL",
			"DELETE FROM conference_members",
			"DELETE FROM conferences",
			"DELETE FROM calls",
		} {
			if _, err := d.DB.Exec(q); err != nil {
				t.Errorf("cleanup %q: %v", q, err)
			}
		}
		_ = d.Close()
	})
	return d
}
