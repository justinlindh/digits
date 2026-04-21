//go:build integration

package calls_test

import (
	"os"
	"testing"

	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/db"
)

func TestTrackerBusyWithConference_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.DB.Exec("DELETE FROM calls")
		_ = d.Close()
	})

	tr := calls.New(d)
	id, err := tr.OnCallInitiated("5550001", "5550002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if !tr.Busy("5550001") {
		t.Fatalf("expected 5550001 busy from 2-party call")
	}

	// End the 2-party call so 5550001 is no longer in tr.active, then
	// put 5550001 in a conference. tr.Busy should still return true.
	if err := tr.OnCallEnded("5550001", "5550002"); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}
	if tr.Busy("5550001") {
		t.Fatalf("expected 5550001 NOT busy after call ended")
	}

	_, err = tr.Conferences().CreateConference("5550001", id, []string{"5550010", "5550011"})
	if err != nil {
		t.Fatalf("CreateConference: %v", err)
	}
	if !tr.Busy("5550001") {
		t.Fatalf("expected 5550001 busy via conference")
	}
	if !tr.Busy("5550010") {
		t.Fatalf("expected 5550010 busy via conference")
	}
	if tr.Busy("5550099") {
		t.Fatalf("unexpected busy for 5550099")
	}
}
