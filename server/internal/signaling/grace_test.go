package signaling

import (
	"testing"
	"time"
)

func TestNewRelaySetsDefaultGraceWindow(t *testing.T) {
	r := NewRelay(NewHub(), newMockTracker(), nil, nil)
	if r.GraceWindow != 20*time.Second {
		t.Fatalf("default GraceWindow = %v, want 20s", r.GraceWindow)
	}
	if r.graceTimers == nil {
		t.Fatal("graceTimers map not initialized")
	}
}

func TestGraceTimerFiresTeardownAndHangsUpPeer(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = 20 * time.Millisecond

	peer := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", peer)

	relay.startGraceTimer("3140001", "hw-1", "3140002")

	select {
	case data := <-peer.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeHangup {
			t.Fatalf("peer got %q, want hangup", msg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("peer never received hangup after grace expiry")
	}
	if got := tracker.clearedNumbers(); len(got) != 1 || got[0] != "3140001" {
		t.Fatalf("ClearByNumber calls = %v, want [3140001]", got)
	}
}

func TestCancelGraceLocalPreventsTeardown(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = 50 * time.Millisecond

	peer := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", peer)

	relay.startGraceTimer("3140001", "hw-1", "3140002")
	if !relay.cancelGraceLocal("3140001", "hw-1") {
		t.Fatal("cancelGraceLocal returned false; expected a live timer")
	}

	select {
	case <-peer.Send:
		t.Fatal("peer received a hangup; grace should have been canceled")
	case <-time.After(120 * time.Millisecond):
	}
	if got := tracker.clearedNumbers(); len(got) != 0 {
		t.Fatalf("ClearByNumber called %v; expected none", got)
	}
}

func TestStartGraceTimerReplacesExistingTimer(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = 30 * time.Millisecond

	peer := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", peer)

	// Two starts for the same key: the first must be canceled and replaced,
	// so exactly one teardown fires.
	relay.startGraceTimer("3140001", "hw-1", "3140002")
	relay.startGraceTimer("3140001", "hw-1", "3140002")

	// Drain hangups for ~120ms (4x window) and count them.
	deadline := time.After(150 * time.Millisecond)
	hangups := 0
loop:
	for {
		select {
		case data := <-peer.Send:
			if msg, _ := ParseMessage(data); msg.Type == TypeHangup {
				hangups++
			}
		case <-deadline:
			break loop
		}
	}
	if hangups != 1 {
		t.Fatalf("got %d hangups, want exactly 1 (old timer replaced, not double-fired)", hangups)
	}
	if got := tracker.clearedNumbers(); len(got) != 1 {
		t.Fatalf("ClearByNumber calls = %v, want exactly 1", got)
	}
}
