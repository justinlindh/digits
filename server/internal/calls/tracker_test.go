package calls

import (
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/db"
)

func setupTestDB(t *testing.T) *db.Database {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("setup db: %v", err)
	}
	t.Cleanup(func() {
		d.DB.Exec("DELETE FROM calls")
		d.Close()
	})
	return d
}

func TestCallLifecycle(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d)

	// Initiate
	err := tr.OnCallInitiated("3140001", "3140002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}

	// Answer
	err = tr.OnCallAnswered("3140001", "3140002")
	if err != nil {
		t.Fatalf("OnCallAnswered: %v", err)
	}

	// End
	err = tr.OnCallEnded("3140001", "3140002")
	if err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}

	// Check history
	calls, err := tr.Recent(10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Status != "ended" {
		t.Fatalf("expected status ended, got %s", calls[0].Status)
	}
}

func TestActiveCalls(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d)

	tr.OnCallInitiated("3140001", "3140002")
	tr.OnCallAnswered("3140001", "3140002")

	active := tr.Active()
	if len(active) != 1 {
		t.Fatalf("expected 1 active call, got %d", len(active))
	}

	tr.OnCallEnded("3140001", "3140002")
	active = tr.Active()
	if len(active) != 0 {
		t.Fatalf("expected 0 active calls, got %d", len(active))
	}
}
