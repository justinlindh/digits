package calls

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedCall inserts a pre-built ActiveCall directly into the tracker's in-memory
// map. Tests that exercise pure in-memory logic use this instead of
// OnCallInitiated, which requires a live database.
func seedCall(tr *Tracker, id int64, caller, callee string) {
	tr.mu.Lock()
	tr.active[callKey(caller, callee)] = &ActiveCall{
		ID:        id,
		Caller:    caller,
		Callee:    callee,
		StartedAt: time.Now(),
	}
	tr.mu.Unlock()
}

func TestBusy(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Tracker)
		number string
		want   bool
	}{
		{
			name:   "idle number",
			setup:  func(_ *Tracker) {},
			number: "3140001",
			want:   false,
		},
		{
			name:   "number is caller",
			setup:  func(tr *Tracker) { seedCall(tr, 1, "3140001", "3140002") },
			number: "3140001",
			want:   true,
		},
		{
			name:   "number is callee",
			setup:  func(tr *Tracker) { seedCall(tr, 1, "3140001", "3140002") },
			number: "3140002",
			want:   true,
		},
		{
			name:   "unrelated call does not affect idle number",
			setup:  func(tr *Tracker) { seedCall(tr, 1, "3140001", "3140002") },
			number: "3140003",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := New(nil)
			tt.setup(tr)
			if got := tr.Busy(context.Background(), tt.number); got != tt.want {
				t.Errorf("Busy(%q) = %v, want %v", tt.number, got, tt.want)
			}
		})
	}
}

func TestCanAddAsHost(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Tracker)
		number string
		want   bool
	}{
		{
			name:   "idle number",
			setup:  func(_ *Tracker) {},
			number: "3140001",
			want:   false,
		},
		{
			name:   "number is callee",
			setup:  func(tr *Tracker) { seedCall(tr, 1, "3140002", "3140001") },
			number: "3140001",
			want:   false,
		},
		{
			name:   "number is caller of exactly one call",
			setup:  func(tr *Tracker) { seedCall(tr, 1, "3140001", "3140002") },
			number: "3140001",
			want:   true,
		},
		{
			name: "number is caller of two calls",
			setup: func(tr *Tracker) {
				seedCall(tr, 1, "3140001", "3140002")
				seedCall(tr, 2, "3140001", "3140003")
			},
			number: "3140001",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := New(nil)
			tt.setup(tr)
			if got := tr.CanAddAsHost(context.Background(), tt.number); got != tt.want {
				t.Errorf("CanAddAsHost(%q) = %v, want %v", tt.number, got, tt.want)
			}
		})
	}
}

func TestAllPeersOf(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Tracker)
		number string
		wantN  int
		wantIn string
	}{
		{
			name:   "no active calls",
			setup:  func(_ *Tracker) {},
			number: "3140001",
			wantN:  0,
		},
		{
			name:   "one peer as caller",
			setup:  func(tr *Tracker) { seedCall(tr, 1, "3140001", "3140002") },
			number: "3140001",
			wantN:  1,
			wantIn: "3140002",
		},
		{
			name:   "one peer as callee",
			setup:  func(tr *Tracker) { seedCall(tr, 1, "3140002", "3140001") },
			number: "3140001",
			wantN:  1,
			wantIn: "3140002",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := New(nil)
			tt.setup(tr)
			got := tr.AllPeersOf(context.Background(), tt.number)
			if len(got) != tt.wantN {
				t.Errorf("AllPeersOf(%q) len = %d, want %d (values: %v)", tt.number, len(got), tt.wantN, got)
				return
			}
			if tt.wantIn != "" && (len(got) == 0 || got[0] != tt.wantIn) {
				t.Errorf("AllPeersOf(%q) = %v, want element %q", tt.number, got, tt.wantIn)
			}
		})
	}
}

func TestPeerOf(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Tracker)
		number string
		want   string
	}{
		{
			name:   "not in any call",
			setup:  func(_ *Tracker) {},
			number: "3140001",
			want:   "",
		},
		{
			name:   "as caller",
			setup:  func(tr *Tracker) { seedCall(tr, 1, "3140001", "3140002") },
			number: "3140001",
			want:   "3140002",
		},
		{
			name:   "as callee",
			setup:  func(tr *Tracker) { seedCall(tr, 1, "3140002", "3140001") },
			number: "3140001",
			want:   "3140002",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := New(nil)
			tt.setup(tr)
			if got := tr.PeerOf(context.Background(), tt.number); got != tt.want {
				t.Errorf("PeerOf(%q) = %q, want %q", tt.number, got, tt.want)
			}
		})
	}
}

func TestCallIDForPair(t *testing.T) {
	tr := New(nil)
	seedCall(tr, 42, "3140001", "3140002")

	if id := tr.CallIDForPair(context.Background(), "3140001", "3140002"); id != 42 {
		t.Errorf("CallIDForPair forward = %d, want 42", id)
	}
	if id := tr.CallIDForPair(context.Background(), "3140002", "3140001"); id != 42 {
		t.Errorf("CallIDForPair reverse = %d, want 42", id)
	}
	if id := tr.CallIDForPair(context.Background(), "3140001", "3140003"); id != 0 {
		t.Errorf("CallIDForPair missing = %d, want 0", id)
	}
}

func TestInCall(t *testing.T) {
	tr := New(nil)
	seedCall(tr, 1, "3140001", "3140002")

	if !tr.InCall(context.Background(), "3140001", "3140002") {
		t.Error("InCall forward: want true, got false")
	}
	if !tr.InCall(context.Background(), "3140002", "3140001") {
		t.Error("InCall reverse: want true, got false")
	}
	if tr.InCall(context.Background(), "3140001", "3140003") {
		t.Error("InCall missing: want false, got true")
	}
}

func TestActive(t *testing.T) {
	tr := New(nil)
	if got := tr.Active(context.Background()); len(got) != 0 {
		t.Errorf("empty tracker Active() = %d calls, want 0", len(got))
	}

	seedCall(tr, 1, "3140001", "3140002")
	seedCall(tr, 2, "3140003", "3140004")
	got := tr.Active(context.Background())
	if len(got) != 2 {
		t.Errorf("Active() = %d calls, want 2", len(got))
	}
}

func TestClearByNumber(t *testing.T) {
	tr := New(nil)
	seedCall(tr, 1, "3140001", "3140002")
	seedCall(tr, 2, "3140001", "3140003")
	seedCall(tr, 3, "3140004", "3140005") // unrelated

	tr.ClearByNumber(context.Background(), "3140001")

	if tr.Busy(context.Background(), "3140001") {
		t.Error("3140001 should not be busy after ClearByNumber")
	}
	if tr.Busy(context.Background(), "3140002") {
		t.Error("3140002 should not be busy after its peer was cleared")
	}
	if !tr.Busy(context.Background(), "3140004") {
		t.Error("unrelated call should still show 3140004 as busy")
	}
}

func TestCallIDFor(t *testing.T) {
	tr := New(nil)

	if _, ok := tr.CallIDFor(context.Background(), "3140001"); ok {
		t.Error("CallIDFor on idle number should return (_, false)")
	}

	seedCall(tr, 99, "3140001", "3140002")

	id, ok := tr.CallIDFor(context.Background(), "3140001")
	if !ok || id != 99 {
		t.Errorf("CallIDFor caller = (%d, %v), want (99, true)", id, ok)
	}
	id, ok = tr.CallIDFor(context.Background(), "3140002")
	if !ok || id != 99 {
		t.Errorf("CallIDFor callee = (%d, %v), want (99, true)", id, ok)
	}
}

func TestSortedPair(t *testing.T) {
	tests := []struct {
		a, b         string
		wantA, wantB string
	}{
		{"3140001", "3140002", "3140001", "3140002"},
		{"3140002", "3140001", "3140001", "3140002"},
		{"aaa", "aaa", "aaa", "aaa"},
	}
	for _, tt := range tests {
		gotA, gotB := sortedPair(tt.a, tt.b)
		if gotA != tt.wantA || gotB != tt.wantB {
			t.Errorf("sortedPair(%q, %q) = (%q, %q), want (%q, %q)",
				tt.a, tt.b, gotA, gotB, tt.wantA, tt.wantB)
		}
	}
}

// TestConferencesBusy verifies that Busy delegates to the ConferenceTracker
// for members of active conferences.
func TestConferencesBusy(t *testing.T) {
	tr := New(nil)

	confID := uuid.New()
	conf := &Conference{
		ID:   confID,
		Host: "3140001",
		Members: map[string]*ConferenceMember{
			"3140001": {Phone: "3140001", Role: ConferenceRoleHost},
		},
	}
	tr.conferences.mu.Lock()
	tr.conferences.active[confID] = conf
	tr.conferences.memberIndex["3140001"] = confID
	tr.conferences.mu.Unlock()

	if !tr.Busy(context.Background(), "3140001") {
		t.Error("Busy should return true for conference host")
	}
	if tr.Busy(context.Background(), "3140002") {
		t.Error("Busy should return false for number not in conference")
	}
}
