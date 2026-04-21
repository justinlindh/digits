package calls

import (
	"testing"
	"time"
)

func TestConferenceTracker_CreateAndCap(t *testing.T) {
	ct := NewConferenceTracker()
	conf, err := ct.CreateConference("5550001", 42, []string{"5550002", "5550003"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(conf.Members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(conf.Members))
	}
	if conf.Members["5550001"].Role != ConferenceRoleHost {
		t.Fatalf("host role not set correctly")
	}
	if conf.Members["5550002"].Role != ConferenceRoleAdded {
		t.Fatalf("added role not set correctly")
	}

	// 4-party is rejected
	_, err = ct.CreateConference("5550010", 99, []string{"5550011", "5550012", "5550013"})
	if err == nil {
		t.Fatalf("expected cap error for 4 members")
	}
}

func TestConferenceTracker_BusyAndContains(t *testing.T) {
	ct := NewConferenceTracker()
	conf, _ := ct.CreateConference("5550001", 42, []string{"5550002", "5550003"})

	for _, p := range []string{"5550001", "5550002", "5550003"} {
		if !ct.IsBusy(p) {
			t.Fatalf("expected %s to be busy in conference", p)
		}
	}
	if ct.IsBusy("5550099") {
		t.Fatalf("unexpected busy for 5550099")
	}

	if !ct.ConferenceContains(conf.ID, "5550002", "5550003") {
		t.Fatalf("ConferenceContains should return true for B,C in same conference")
	}
	if ct.ConferenceContains(conf.ID, "5550002", "5550099") {
		t.Fatalf("ConferenceContains should return false for non-member")
	}
}

func TestConferenceTracker_DropMemberEndsConference(t *testing.T) {
	ct := NewConferenceTracker()
	conf, _ := ct.CreateConference("5550001", 42, []string{"5550002", "5550003"})

	remaining, ended, err := ct.DropMember(conf.ID, "5550002", "hangup")
	if err != nil {
		t.Fatalf("drop: %v", err)
	}
	if !ended {
		t.Fatalf("expected conference to end after a drop (v1 caps at 3)")
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining members, got %d", len(remaining))
	}
	if ct.IsBusy("5550002") {
		t.Fatalf("dropped member should not be busy")
	}
	// After end, surviving members are no longer busy via conference state
	if ct.IsBusy("5550001") || ct.IsBusy("5550003") {
		t.Fatalf("after conference end, surviving members should not be busy via conference state")
	}
}

func TestConferenceTracker_EndConference(t *testing.T) {
	ct := NewConferenceTracker()
	conf, _ := ct.CreateConference("5550001", 42, []string{"5550002", "5550003"})

	members, err := ct.EndConference(conf.ID, "host_hangup")
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("EndConference should return all members that were active, got %d", len(members))
	}
	for _, p := range []string{"5550001", "5550002", "5550003"} {
		if ct.IsBusy(p) {
			t.Fatalf("after end, %s should not be busy", p)
		}
	}
}

func TestConferenceTracker_NoDuplicateHost(t *testing.T) {
	ct := NewConferenceTracker()
	ct.CreateConference("5550001", 42, []string{"5550002", "5550003"})

	// Same host trying to start a second conference should fail
	_, err := ct.CreateConference("5550001", 99, []string{"5550010", "5550011"})
	if err == nil {
		t.Fatalf("expected error for duplicate host")
	}
}

func TestConferenceTracker_MemberAlreadyInConference(t *testing.T) {
	ct := NewConferenceTracker()
	ct.CreateConference("5550001", 42, []string{"5550002", "5550003"})

	// 5550002 already in a conference
	_, err := ct.CreateConference("5550020", 99, []string{"5550002", "5550021"})
	if err == nil {
		t.Fatalf("expected error for member already in conference")
	}

	now := time.Now()
	_ = now
}
