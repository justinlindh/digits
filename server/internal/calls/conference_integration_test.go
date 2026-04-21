//go:build integration

package calls_test

import (
	"testing"

	"github.com/justinlindh/digits/server/internal/calls"
)

func TestTrackerBusyWithConference_Integration(t *testing.T) {
	d := openTestDB(t)

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

func TestConferencePersistence_Integration(t *testing.T) {
	d := openTestDB(t)

	tr := calls.New(d)
	callID, err := tr.OnCallInitiated("5550010", "5550011")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}

	conf, err := tr.CreateConferencePersistent("5550010", callID, []string{"5550011", "5550012"})
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
	if err := tr.EndConferencePersistent(conf.ID, "test_end"); err != nil {
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
	callID, _ := tr.OnCallInitiated("5550100", "5550101")
	conf, _ := tr.CreateConferencePersistent("5550100", callID, []string{"5550101", "5550102"})

	remaining, ended, err := tr.DropMemberPersistent(conf.ID, "5550101", "hangup")
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
		if !tr.Busy(p) {
			t.Fatalf("expected surviving member %s to be busy after continuation", p)
		}
	}
	// The dropped member is not busy.
	if tr.Busy("5550101") {
		t.Fatalf("dropped member should not be busy")
	}
}
