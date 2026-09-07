//go:build integration

package calls_test

import (
	"context"
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

func setRenumberEnabled(t *testing.T, d *db.Database, enabled bool) {
	t.Helper()
	if _, err := d.DB.ExecContext(context.Background(),
		`UPDATE renumber_control SET enabled = $1, updated_at = NOW() WHERE singleton`, enabled); err != nil {
		t.Fatalf("set renumber gate to %t: %v", enabled, err)
	}
	t.Cleanup(func() {
		_, _ = d.DB.ExecContext(context.Background(),
			`DELETE FROM renumber_control; INSERT INTO renumber_control (singleton, enabled, identity_cutover) VALUES (TRUE, FALSE, FALSE)`)
	})
}
