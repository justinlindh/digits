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
