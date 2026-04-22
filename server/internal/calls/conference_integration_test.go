//go:build integration

package calls_test

import (
	"context"
	"testing"

	"github.com/justinlindh/digits/server/internal/calls"
)

func TestTrackerBusyWithConference_Integration(t *testing.T) {
	d := openTestDB(t)

	tr := calls.New(d)
	id, err := tr.OnCallInitiated(context.Background(), "5550001", "5550002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if !tr.Busy(context.Background(), "5550001") {
		t.Fatalf("expected 5550001 busy from 2-party call")
	}

	// End the 2-party call so 5550001 is no longer in tr.active, then
	// put 5550001 in a conference. tr.Busy should still return true.
	if err := tr.OnCallEnded(context.Background(), "5550001", "5550002"); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}
	if tr.Busy(context.Background(), "5550001") {
		t.Fatalf("expected 5550001 NOT busy after call ended")
	}

	_, err = tr.Conferences().CreateConference("5550001", id, []string{"5550010", "5550011"})
	if err != nil {
		t.Fatalf("CreateConference: %v", err)
	}
	if !tr.Busy(context.Background(), "5550001") {
		t.Fatalf("expected 5550001 busy via conference")
	}
	if !tr.Busy(context.Background(), "5550010") {
		t.Fatalf("expected 5550010 busy via conference")
	}
	if tr.Busy(context.Background(), "5550099") {
		t.Fatalf("unexpected busy for 5550099")
	}
}

func TestConferencePersistence_Integration(t *testing.T) {
	d := openTestDB(t)

	tr := calls.New(d)
	callID, err := tr.OnCallInitiated(context.Background(), "5550010", "5550011")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	// Simulate the add-leg: host dials the third party while B is on hold.
	if _, err := tr.OnCallInitiated(context.Background(), "5550010", "5550012"); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}

	conf, err := tr.CreateConferencePersistent(context.Background(), "5550010", callID, []string{"5550011", "5550012"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	var dbHost string
	err = d.DB.QueryRow("SELECT host_phone FROM conferences WHERE id = $1", conf.ID).Scan(&dbHost)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if dbHost != "5550010" {
		t.Fatalf("expected host 5550010, got %s", dbHost)
	}

	var memberCount int
	err = d.DB.QueryRow("SELECT COUNT(*) FROM conference_members WHERE conference_id = $1", conf.ID).Scan(&memberCount)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if memberCount != 3 {
		t.Fatalf("expected 3 members persisted, got %d", memberCount)
	}

	// End it
	if err := tr.EndConferencePersistent(context.Background(), conf.ID, "test_end"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}
	var state, endReason string
	err = d.DB.QueryRow("SELECT state, end_reason FROM conferences WHERE id = $1", conf.ID).Scan(&state, &endReason)
	if err != nil {
		t.Fatalf("select after end: %v", err)
	}
	if state != "ended" || endReason != "test_end" {
		t.Fatalf("expected state=ended end_reason=test_end, got state=%s end_reason=%s", state, endReason)
	}
}

func TestDropMemberCreatesContinuationCall_Integration(t *testing.T) {
	d := openTestDB(t)

	tr := calls.New(d)
	callID, _ := tr.OnCallInitiated(context.Background(), "5550100", "5550101")
	// Simulate add-leg before merge.
	_, _ = tr.OnCallInitiated(context.Background(), "5550100", "5550102")
	conf, _ := tr.CreateConferencePersistent(context.Background(), "5550100", callID, []string{"5550101", "5550102"})

	remaining, ended, err := tr.DropMemberPersistent(context.Background(), conf.ID, "5550101", "hangup")
	if err != nil {
		t.Fatalf("DropMemberPersistent: %v", err)
	}
	if !ended {
		t.Fatalf("expected conference ended")
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(remaining))
	}

	// A new calls row must exist with originating_conference_id set and status = 'connected'
	var count int
	err = d.DB.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE originating_conference_id = $1 AND status = 'connected'`,
		conf.ID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count continuation calls: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 continuation calls row, got %d", count)
	}

	// Verify surviving members are busy via tr.active and Tracker.Busy reports them so.
	for _, p := range remaining {
		if !tr.Busy(context.Background(), p) {
			t.Fatalf("expected surviving member %s to be busy after continuation", p)
		}
	}
	// The dropped member is not busy.
	if tr.Busy(context.Background(), "5550101") {
		t.Fatalf("dropped member should not be busy")
	}
}

func TestCreateConferenceEvictsActiveEntries_Integration(t *testing.T) {
	d := openTestDB(t)

	tr := calls.New(d)
	callID, err := tr.OnCallInitiated(context.Background(), "5550200", "5550201")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}

	// Pre-condition: 2-party call is active, both phones busy, InCall true.
	if !tr.InCall(context.Background(), "5550200", "5550201") {
		t.Fatalf("expected InCall true before conference")
	}
	for _, p := range []string{"5550200", "5550201"} {
		if !tr.Busy(context.Background(), p) {
			t.Fatalf("expected %s busy before conference", p)
		}
	}

	// Add a second active call so we can verify only conference-related entries get evicted.
	if _, err := tr.OnCallInitiated(context.Background(), "5550300", "5550301"); err != nil {
		t.Fatalf("second OnCallInitiated: %v", err)
	}

	// Simulate add-leg: host dials 5550202 while 5550201 is on hold.
	if _, err := tr.OnCallInitiated(context.Background(), "5550200", "5550202"); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}

	_, err = tr.CreateConferencePersistent(context.Background(), "5550200", callID, []string{"5550201", "5550202"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	// After merge: the 2-party A<->B entry must be gone from the active map.
	if tr.InCall(context.Background(), "5550200", "5550201") {
		t.Fatalf("expected 2-party A<->B entry evicted from active map")
	}
	// Unrelated 2-party call must still be present.
	if !tr.InCall(context.Background(), "5550300", "5550301") {
		t.Fatalf("expected unrelated 2-party call to survive")
	}
	// All three conference members should be Busy (via conference).
	for _, p := range []string{"5550200", "5550201", "5550202"} {
		if !tr.Busy(context.Background(), p) {
			t.Fatalf("expected conference member %s busy", p)
		}
	}
}
