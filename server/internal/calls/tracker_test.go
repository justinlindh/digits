//go:build integration

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
		_, _ = d.DB.Exec("DELETE FROM conference_members")
		_, _ = d.DB.Exec("DELETE FROM conferences")
		_, _ = d.DB.Exec("DELETE FROM calls")
		_ = d.Close()
	})
	return d
}

func TestCallLifecycle(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d)

	// Initiate
	_, err := tr.OnCallInitiated("3140001", "3140002")
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

func TestOnCallInitiatedReturnsCallID(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d)
	id, err := tr.OnCallInitiated("555-1111", "555-2222")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if id <= 0 {
		t.Fatalf("want positive id, got %d", id)
	}
	if got, ok := tr.CallIDFor("555-1111"); !ok || got != id {
		t.Fatalf("CallIDFor caller: got (%d,%v), want (%d,true)", got, ok, id)
	}
	if got, ok := tr.CallIDFor("555-2222"); !ok || got != id {
		t.Fatalf("CallIDFor callee: got (%d,%v), want (%d,true)", got, ok, id)
	}
}

func TestActiveCalls(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d)

	_, _ = tr.OnCallInitiated("3140001", "3140002")
	_ = tr.OnCallAnswered("3140001", "3140002")

	active := tr.Active()
	if len(active) != 1 {
		t.Fatalf("expected 1 active call, got %d", len(active))
	}

	_ = tr.OnCallEnded("3140001", "3140002")
	active = tr.Active()
	if len(active) != 0 {
		t.Fatalf("expected 0 active calls, got %d", len(active))
	}
}
