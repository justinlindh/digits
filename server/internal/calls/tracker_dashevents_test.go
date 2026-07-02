//go:build integration

package calls

// Tests that *Tracker calls SetDashboardEvents.Notify on the lifecycle
// methods that change the active-call count: OnCallInitiated, OnCallEnded,
// and ClearByNumber. The dashboard SSE handler relies on these wakes to
// re-render counters without polling. Integration-tagged because the
// Tracker methods exercised here run real SQL through the shared TEST_DATABASE_URL.

import (
	"context"
	"sync/atomic"
	"testing"
)

type fakeNotifier struct {
	count atomic.Int32
}

func (f *fakeNotifier) Notify() {
	f.count.Add(1)
}

func (f *fakeNotifier) Count() int32 { return f.count.Load() }

func TestTracker_SetDashboardEvents_OptionalAndOverwritable(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d.DB)
	// nil notifier must be accepted without panic.
	tr.SetDashboardEvents(nil)
	n := &fakeNotifier{}
	tr.SetDashboardEvents(n)
	tr.SetDashboardEvents(n) // overwrite is fine
}

// TestTracker_NotifiesOnLifecycle exercises OnCallInitiated and OnCallEnded
// against a real DB and verifies the broadcaster fires once per state change.
// The dashboard SSE handler subscribes to those wakes to re-render the
// ON CALL counter, so a missed Notify here means a stale UI in production.
func TestTracker_NotifiesOnLifecycle(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d.DB)
	n := &fakeNotifier{}
	tr.SetDashboardEvents(n)

	if _, err := tr.OnCallInitiated(context.Background(), "3140101", "3140102"); err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if got := n.Count(); got != 1 {
		t.Fatalf("after OnCallInitiated: got %d Notifies want 1", got)
	}

	if err := tr.OnCallEnded(context.Background(), "3140101", "3140102"); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}
	if got := n.Count(); got != 2 {
		t.Fatalf("after OnCallEnded: got %d Notifies want 2", got)
	}

	// Idempotent end: nothing in the active map for this pair, so Notify
	// must not fire again.
	if err := tr.OnCallEnded(context.Background(), "3140101", "3140102"); err != nil {
		t.Fatalf("idempotent OnCallEnded: %v", err)
	}
	if got := n.Count(); got != 2 {
		t.Fatalf("no-op OnCallEnded must not Notify: got %d want 2", got)
	}
}

func TestTracker_NotifiesOnClearByNumber(t *testing.T) {
	d := setupTestDB(t)
	tr := New(d.DB)
	n := &fakeNotifier{}
	tr.SetDashboardEvents(n)

	if _, err := tr.OnCallInitiated(context.Background(), "3140201", "3140202"); err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	// One wake from OnCallInitiated.
	if got := n.Count(); got != 1 {
		t.Fatalf("after Initiate: got %d want 1", got)
	}

	tr.ClearByNumber(context.Background(), "3140201")
	if got := n.Count(); got != 2 {
		t.Fatalf("after ClearByNumber: got %d want 2", got)
	}

	// Second clear hits no entries; must not wake.
	tr.ClearByNumber(context.Background(), "3140201")
	if got := n.Count(); got != 2 {
		t.Fatalf("no-op Clear must not Notify: got %d want 2", got)
	}
}
