//go:build integration

package calls_test

import (
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/calls"
)

// TestRecentHistoryForPhones_PureCall verifies a plain 2-party call yields
// one Call entry.
func TestRecentHistoryForPhones_PureCall(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)

	_, err := tr.OnCallInitiated("7770001", "7770002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if err := tr.OnCallEnded("7770001", "7770002"); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}

	entries, err := tr.RecentHistoryForPhones([]string{"7770001", "7770002"}, 10)
	if err != nil {
		t.Fatalf("RecentHistoryForPhones: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Kind != calls.HistoryEntryCall {
		t.Errorf("expected Kind=Call, got %v", entries[0].Kind)
	}
	if entries[0].Call == nil {
		t.Fatal("expected Call to be set")
	}
	if entries[0].IsConference() {
		t.Error("IsConference should be false for a call entry")
	}
}

// TestRecentHistoryForPhones_ThreeWayHappy verifies that a full 3-way call
// (merge + host hangup) produces exactly 1 Conference entry with 3 members
// and correct duration, and the merged pre-merge legs do NOT appear.
func TestRecentHistoryForPhones_ThreeWayHappy(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)

	callID, err := tr.OnCallInitiated("7771001", "7771002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	// Add-leg: host dials the third party.
	if _, err := tr.OnCallInitiated("7771001", "7771003"); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}

	conf, err := tr.CreateConferencePersistent("7771001", callID, []string{"7771002", "7771003"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	if err := tr.EndConferencePersistent(conf.ID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}

	entries, err := tr.RecentHistoryForPhones([]string{"7771001", "7771002", "7771003"}, 10)
	if err != nil {
		t.Fatalf("RecentHistoryForPhones: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (conference), got %d", len(entries))
	}
	e := entries[0]
	if e.Kind != calls.HistoryEntryConference {
		t.Fatalf("expected Kind=Conference, got %v", e.Kind)
	}
	if !e.IsConference() {
		t.Error("IsConference should be true")
	}
	if e.Conference == nil {
		t.Fatal("Conference field is nil")
	}
	cs := e.Conference
	if len(cs.Members) != 3 {
		t.Errorf("expected 3 members, got %d: %v", len(cs.Members), cs.Members)
	}
	if cs.Host != "7771001" {
		t.Errorf("expected host 7771001, got %s", cs.Host)
	}
	if cs.Members[0] != "7771001" {
		t.Errorf("expected host first in Members, got %v", cs.Members)
	}
	if cs.EndedAt == nil {
		t.Error("EndedAt should be set for ended conference")
	}
	// Duration should be >= 0 (the conference was created and immediately ended
	// in the test, so it is allowed to be 0).
	if cs.DurationS < 0 {
		t.Errorf("unexpected negative DurationS: %d", cs.DurationS)
	}
}

// TestRecentHistoryForPhones_MemberLeave verifies that when a member leaves,
// the result is 1 Conference + 1 continuation Call (the survivor pair).
func TestRecentHistoryForPhones_MemberLeave(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)

	callID, err := tr.OnCallInitiated("7772001", "7772002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if _, err := tr.OnCallInitiated("7772001", "7772003"); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}

	conf, err := tr.CreateConferencePersistent("7772001", callID, []string{"7772002", "7772003"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	remaining, ended, err := tr.DropMemberPersistent(conf.ID, "7772003", "hangup")
	if err != nil {
		t.Fatalf("DropMemberPersistent: %v", err)
	}
	if !ended {
		t.Fatal("expected conference ended after drop")
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(remaining))
	}

	// End the continuation call for cleanup
	if err := tr.OnCallEnded(remaining[0], remaining[1]); err != nil {
		t.Fatalf("OnCallEnded continuation: %v", err)
	}

	entries, err := tr.RecentHistoryForPhones([]string{"7772001", "7772002", "7772003"}, 10)
	if err != nil {
		t.Fatalf("RecentHistoryForPhones: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (1 conference + 1 continuation), got %d: %+v", len(entries), entries)
	}

	var confCount, callCount int
	for _, e := range entries {
		switch e.Kind {
		case calls.HistoryEntryConference:
			confCount++
		case calls.HistoryEntryCall:
			callCount++
			if e.Call.OriginatingConferenceID == nil {
				t.Error("continuation call should have OriginatingConferenceID set")
			}
		}
	}
	if confCount != 1 || callCount != 1 {
		t.Errorf("expected 1 conference + 1 call, got conf=%d call=%d", confCount, callCount)
	}
}

// TestRecentHistoryForPhones_MixedTimeline verifies stable descending sort
// across a mix of call and conference entries.
func TestRecentHistoryForPhones_MixedTimeline(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)

	// T1: 2-party call
	id1, err := tr.OnCallInitiated("7773001", "7773002")
	if err != nil {
		t.Fatalf("OnCallInitiated T1: %v", err)
	}
	if err := tr.OnCallEnded("7773001", "7773002"); err != nil {
		t.Fatalf("OnCallEnded T1: %v", err)
	}

	// Brief sleep so timestamps are distinguishable
	time.Sleep(10 * time.Millisecond)

	// T2: 3-way conference
	id2, err := tr.OnCallInitiated("7773001", "7773002")
	if err != nil {
		t.Fatalf("OnCallInitiated T2: %v", err)
	}
	if _, err := tr.OnCallInitiated("7773001", "7773003"); err != nil {
		t.Fatalf("OnCallInitiated T2 add-leg: %v", err)
	}
	conf, err := tr.CreateConferencePersistent("7773001", id2, []string{"7773002", "7773003"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent T2: %v", err)
	}
	if err := tr.EndConferencePersistent(conf.ID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent T2: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// T3: another 2-party call (reuse same numbers — OK since earlier calls are ended)
	_, err = tr.OnCallInitiated("7773001", "7773002")
	if err != nil {
		t.Fatalf("OnCallInitiated T3: %v", err)
	}
	if err := tr.OnCallEnded("7773001", "7773002"); err != nil {
		t.Fatalf("OnCallEnded T3: %v", err)
	}

	// Suppress "declared and not used" — id1 is referenced only for clarity.
	_ = id1

	entries, err := tr.RecentHistoryForPhones([]string{"7773001", "7773002", "7773003"}, 10)
	if err != nil {
		t.Fatalf("RecentHistoryForPhones: %v", err)
	}
	// Expect: T3 call, T2 conference. T1 call is NOT merged so appears too = 3 entries.
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Kind != calls.HistoryEntryCall {
		t.Errorf("entry 0 should be Call (T3), got %v", entries[0].Kind)
	}
	if entries[1].Kind != calls.HistoryEntryConference {
		t.Errorf("entry 1 should be Conference (T2), got %v", entries[1].Kind)
	}
	if entries[2].Kind != calls.HistoryEntryCall {
		t.Errorf("entry 2 should be Call (T1), got %v", entries[2].Kind)
	}
	if !entries[0].SortTime.After(entries[1].SortTime) {
		t.Error("entries not sorted descending: entry 0 should be after entry 1")
	}
	if !entries[1].SortTime.After(entries[2].SortTime) {
		t.Error("entries not sorted descending: entry 1 should be after entry 2")
	}
}

// TestRecentHistoryForPhones_MergedLegsExcluded verifies that the pre-merge
// call legs do not appear in the results.
func TestRecentHistoryForPhones_MergedLegsExcluded(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)

	callID, err := tr.OnCallInitiated("7774001", "7774002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if _, err := tr.OnCallInitiated("7774001", "7774003"); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}

	conf, err := tr.CreateConferencePersistent("7774001", callID, []string{"7774002", "7774003"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}
	if err := tr.EndConferencePersistent(conf.ID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}

	entries, err := tr.RecentHistoryForPhones([]string{"7774001", "7774002", "7774003"}, 10)
	if err != nil {
		t.Fatalf("RecentHistoryForPhones: %v", err)
	}
	// Only the conference should appear; merged call rows must be excluded.
	for _, e := range entries {
		if e.Kind == calls.HistoryEntryCall {
			t.Errorf("merged call leg appeared as Call entry: %+v", e.Call)
		}
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 entry (the conference), got %d", len(entries))
	}
}
