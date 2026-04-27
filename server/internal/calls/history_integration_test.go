//go:build integration

package calls_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/justinlindh/digits/server/internal/calls"
)

// TestRecentHistoryForPhones_PureCall verifies a plain 2-party call yields
// one Call entry.
func TestRecentHistoryForPhones_PureCall(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)

	_, err := tr.OnCallInitiated(context.Background(), "7770001", "7770002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if err := tr.OnCallEnded(context.Background(), "7770001", "7770002"); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}

	entries, err := tr.RecentHistoryForPhones(context.Background(), []string{"7770001", "7770002"}, nil, 10)
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

	callID, err := tr.OnCallInitiated(context.Background(), "7771001", "7771002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	// Add-leg: host dials the third party.
	if _, err := tr.OnCallInitiated(context.Background(), "7771001", "7771003"); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}

	conf, err := tr.CreateConferencePersistent(context.Background(), "7771001", callID, []string{"7771002", "7771003"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	if err := tr.EndConferencePersistent(context.Background(), conf.ID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}

	entries, err := tr.RecentHistoryForPhones(context.Background(), []string{"7771001", "7771002", "7771003"}, nil, 10)
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

	callID, err := tr.OnCallInitiated(context.Background(), "7772001", "7772002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if _, err := tr.OnCallInitiated(context.Background(), "7772001", "7772003"); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}

	conf, err := tr.CreateConferencePersistent(context.Background(), "7772001", callID, []string{"7772002", "7772003"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	remaining, ended, err := tr.DropMemberPersistent(context.Background(), conf.ID, "7772003", "hangup")
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
	if err := tr.OnCallEnded(context.Background(), remaining[0], remaining[1]); err != nil {
		t.Fatalf("OnCallEnded continuation: %v", err)
	}

	entries, err := tr.RecentHistoryForPhones(context.Background(), []string{"7772001", "7772002", "7772003"}, nil, 10)
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
	id1, err := tr.OnCallInitiated(context.Background(), "7773001", "7773002")
	if err != nil {
		t.Fatalf("OnCallInitiated T1: %v", err)
	}
	if err := tr.OnCallEnded(context.Background(), "7773001", "7773002"); err != nil {
		t.Fatalf("OnCallEnded T1: %v", err)
	}

	// Brief sleep so timestamps are distinguishable
	time.Sleep(10 * time.Millisecond)

	// T2: 3-way conference
	id2, err := tr.OnCallInitiated(context.Background(), "7773001", "7773002")
	if err != nil {
		t.Fatalf("OnCallInitiated T2: %v", err)
	}
	if _, err := tr.OnCallInitiated(context.Background(), "7773001", "7773003"); err != nil {
		t.Fatalf("OnCallInitiated T2 add-leg: %v", err)
	}
	conf, err := tr.CreateConferencePersistent(context.Background(), "7773001", id2, []string{"7773002", "7773003"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent T2: %v", err)
	}
	if err := tr.EndConferencePersistent(context.Background(), conf.ID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent T2: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// T3: another 2-party call (reuse same numbers — OK since earlier calls are ended)
	_, err = tr.OnCallInitiated(context.Background(), "7773001", "7773002")
	if err != nil {
		t.Fatalf("OnCallInitiated T3: %v", err)
	}
	if err := tr.OnCallEnded(context.Background(), "7773001", "7773002"); err != nil {
		t.Fatalf("OnCallEnded T3: %v", err)
	}

	// Suppress "declared and not used" — id1 is referenced only for clarity.
	_ = id1

	entries, err := tr.RecentHistoryForPhones(context.Background(), []string{"7773001", "7773002", "7773003"}, nil, 10)
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

// TestRecentHistoryForPhones_CursorPagination_CallsOnly verifies that a
// cursor pointing at the Nth call returns only entries strictly older than
// the cursor in the merged timeline order.
func TestRecentHistoryForPhones_CursorPagination_CallsOnly(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)

	// Insert 5 plain calls between phones A and B, ended in order so
	// started_at increases monotonically.
	for i := 0; i < 5; i++ {
		if _, err := tr.OnCallInitiated(context.Background(), "7780001", "7780002"); err != nil {
			t.Fatalf("OnCallInitiated[%d]: %v", i, err)
		}
		if err := tr.OnCallEnded(context.Background(), "7780001", "7780002"); err != nil {
			t.Fatalf("OnCallEnded[%d]: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	page1, err := tr.RecentHistoryForPhones(context.Background(), []string{"7780001", "7780002"}, nil, 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	// Expect 3 (limit+1) entries returned by the underlying probe.
	if len(page1) != 3 {
		t.Fatalf("page1: expected 3 entries, got %d", len(page1))
	}

	// Build cursor at entry index 1 (the 2nd entry on a 2-page).
	cursor := calls.CursorForEntry(page1[1])

	page2, err := tr.RecentHistoryForPhones(context.Background(), []string{"7780001", "7780002"}, &cursor, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	// 5 calls total, 2 returned on page1, page2 should hold the remaining 3.
	if len(page2) != 3 {
		t.Fatalf("page2: expected 3 remaining entries, got %d", len(page2))
	}
	// Continuity: page2[0] is strictly older than the cursor.
	if !page2[0].SortTime.Before(cursor.Time) && page2[0].Call.ID >= page1[1].Call.ID {
		t.Errorf("page2[0] is not older than cursor: page2[0]=%v cursor=%v", page2[0].SortTime, cursor.Time)
	}
}

// TestRecentHistoryForPhones_MergedLegsExcluded verifies that the pre-merge
// call legs do not appear in the results.
func TestRecentHistoryForPhones_MergedLegsExcluded(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)

	callID, err := tr.OnCallInitiated(context.Background(), "7774001", "7774002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if _, err := tr.OnCallInitiated(context.Background(), "7774001", "7774003"); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}

	conf, err := tr.CreateConferencePersistent(context.Background(), "7774001", callID, []string{"7774002", "7774003"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}
	if err := tr.EndConferencePersistent(context.Background(), conf.ID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}

	entries, err := tr.RecentHistoryForPhones(context.Background(), []string{"7774001", "7774002", "7774003"}, nil, 10)
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

// TestRecentHistoryForPhones_CursorTieBreak verifies that a call and a
// conference created in the same transaction (and therefore at the same
// timestamp) are ordered call-before-conference, and a Call cursor advances
// past the call so the next page starts with the conference.
func TestRecentHistoryForPhones_CursorTieBreak(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)

	// 3-way merge yields a Call (the originating leg, marked merged) plus a
	// Conference. The originating call is excluded from history (filtered
	// by end_reason='merged_to_conference'), so we set up a tie differently:
	// create a plain ended call and a conference at the same instant via
	// raw inserts.
	now := time.Now().UTC().Round(time.Microsecond)

	var callID int64
	if err := d.DB.QueryRow(`
		INSERT INTO calls (caller, callee, status, started_at, ended_at, duration_s)
		VALUES ('7790001', '7790002', 'ended', $1, $1, 0) RETURNING id`, now).Scan(&callID); err != nil {
		t.Fatalf("insert call: %v", err)
	}
	confID := uuid.New()
	if _, err := d.DB.Exec(`
		INSERT INTO conferences (id, host_phone, originating_call_id, state, created_at, ended_at, end_reason)
		VALUES ($1, '7790001', $2, 'ended', $3, $3, 'host_hangup')`, confID, callID, now); err != nil {
		t.Fatalf("insert conference: %v", err)
	}
	if _, err := d.DB.Exec(`
		INSERT INTO conference_members (conference_id, phone, role) VALUES ($1, '7790001', 'host'), ($1, '7790002', 'added'), ($1, '7790003', 'added')`, confID); err != nil {
		t.Fatalf("insert conference_members: %v", err)
	}

	page1, err := tr.RecentHistoryForPhones(context.Background(), []string{"7790001", "7790002", "7790003"}, nil, 1)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) < 1 {
		t.Fatalf("page1: expected at least 1 entry, got %d", len(page1))
	}
	// Tie-break rule: Call comes before Conference on equal timestamp.
	if page1[0].Kind != calls.HistoryEntryCall {
		t.Fatalf("page1[0] expected Call, got %v", page1[0].Kind)
	}

	cursor := calls.CursorForEntry(page1[0])
	page2, err := tr.RecentHistoryForPhones(context.Background(), []string{"7790001", "7790002", "7790003"}, &cursor, 5)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) < 1 {
		t.Fatalf("page2: expected at least 1 entry, got %d", len(page2))
	}
	if page2[0].Kind != calls.HistoryEntryConference {
		t.Errorf("page2[0] expected Conference (post-call cursor advance), got %v", page2[0].Kind)
	}
	if page2[0].Conference == nil || page2[0].Conference.ID != confID {
		t.Errorf("page2[0] conference ID mismatch")
	}

	// Advance past the conference: page3 should be empty. Without the
	// conferences-subquery cursor filter the conference would re-appear.
	cursor2 := calls.CursorForEntry(page2[0])
	page3, err := tr.RecentHistoryForPhones(context.Background(), []string{"7790001", "7790002", "7790003"}, &cursor2, 5)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	for _, e := range page3 {
		if e.Kind == calls.HistoryEntryConference && e.Conference != nil && e.Conference.ID == confID {
			t.Errorf("page3 leaked the cursor conference (cursor filter missing on conferences subquery)")
		}
	}
}
