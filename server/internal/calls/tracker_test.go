//go:build integration

package calls

import (
	"context"
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
	tr := New(d.DB)

	// Initiate
	_, err := tr.OnCallInitiated(context.Background(), "3140001", "3140002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}

	// Answer
	err = tr.OnCallAnswered(context.Background(), "3140001", "3140002")
	if err != nil {
		t.Fatalf("OnCallAnswered: %v", err)
	}

	// End
	err = tr.OnCallEnded(context.Background(), "3140001", "3140002")
	if err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}

	// Check history
	calls, err := tr.RecentForPhones(context.Background(), []string{"3140001", "3140002"}, 10)
	if err != nil {
		t.Fatalf("RecentForPhones: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Status != CallStatusEnded {
		t.Fatalf("expected status ended, got %s", calls[0].Status)
	}
}

func TestOnCallInitiatedReturnsCallID(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d.DB)
	id, err := tr.OnCallInitiated(context.Background(), "555-1111", "555-2222")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if id <= 0 {
		t.Fatalf("want positive id, got %d", id)
	}
	if got, ok := tr.CallIDFor(context.Background(), "555-1111"); !ok || got != id {
		t.Fatalf("CallIDFor caller: got (%d,%v), want (%d,true)", got, ok, id)
	}
	if got, ok := tr.CallIDFor(context.Background(), "555-2222"); !ok || got != id {
		t.Fatalf("CallIDFor callee: got (%d,%v), want (%d,true)", got, ok, id)
	}
}

func TestActiveCalls(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d.DB)

	_, _ = tr.OnCallInitiated(context.Background(), "3140001", "3140002")
	_ = tr.OnCallAnswered(context.Background(), "3140001", "3140002")

	active := tr.Active(context.Background())
	if len(active) != 1 {
		t.Fatalf("expected 1 active call, got %d", len(active))
	}

	_ = tr.OnCallEnded(context.Background(), "3140001", "3140002")
	active = tr.Active(context.Background())
	if len(active) != 0 {
		t.Fatalf("expected 0 active calls, got %d", len(active))
	}
}

func TestOnCallEndedWithReason_PersistsReason(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d.DB)

	id, err := tr.OnCallInitiated(context.Background(), "3140011", "3140012")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if err := tr.OnCallAnswered(context.Background(), "3140011", "3140012"); err != nil {
		t.Fatalf("OnCallAnswered: %v", err)
	}
	if err := tr.OnCallEndedWithReason(context.Background(), "3140012", "3140011", "connect_timeout"); err != nil {
		t.Fatalf("OnCallEndedWithReason: %v", err)
	}

	c, err := tr.GetCall(context.Background(), id)
	if err != nil {
		t.Fatalf("GetCall: %v", err)
	}
	if c.Status != CallStatusEnded {
		t.Fatalf("expected status ended, got %s", c.Status)
	}
	if c.EndReason == nil || *c.EndReason != "connect_timeout" {
		t.Fatalf("expected end_reason connect_timeout, got %v", c.EndReason)
	}
}

func TestOnCallEnded_LeavesEndReasonNull(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d.DB)

	id, err := tr.OnCallInitiated(context.Background(), "3140021", "3140022")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if err := tr.OnCallEnded(context.Background(), "3140021", "3140022"); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}

	c, err := tr.GetCall(context.Background(), id)
	if err != nil {
		t.Fatalf("GetCall: %v", err)
	}
	if c.EndReason != nil {
		t.Fatalf("expected NULL end_reason, got %q", *c.EndReason)
	}
}
