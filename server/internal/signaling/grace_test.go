package signaling

import (
	"context"
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

func TestOnDisconnectHoldsInCall2Party(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	tracker.peers = map[string]string{"3140001": "3140002"}
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = time.Hour // never fires during the test

	relay.OnDisconnect(context.Background(), "3140001", "hw-1")

	if got := tracker.clearedNumbers(); len(got) != 0 {
		t.Fatalf("ClearByNumber called immediately: %v", got)
	}
	if !relay.cancelGraceLocal("3140001", "hw-1") {
		t.Fatal("no grace timer pending after in-call disconnect")
	}
}

func TestOnDisconnectIdlePhoneClearsImmediately(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	tracker.peers = map[string]string{} // not in any call
	relay := NewRelay(hub, tracker, nil, nil)

	relay.OnDisconnect(context.Background(), "3140009", "hw-9")

	if got := tracker.clearedNumbers(); len(got) != 1 || got[0] != "3140009" {
		t.Fatalf("idle disconnect ClearByNumber = %v, want [3140009]", got)
	}
	if relay.cancelGraceLocal("3140009", "hw-9") {
		t.Fatal("idle disconnect should not arm a grace timer")
	}
}

func TestDeliverFromRedisReconnectInvokesHook(t *testing.T) {
	hub := NewHub()
	var gotNumber, gotHW string
	hub.SetReconnectHook(func(number, hardwareID string) {
		gotNumber, gotHW = number, hardwareID
	})
	hub.deliverFromRedis(&Envelope{
		TargetType: "reconnect",
		Target:     "3140001",
		Message:    &Message{HardwareID: "hw-1"},
	})
	if gotNumber != "3140001" || gotHW != "hw-1" {
		t.Fatalf("hook got (%q,%q), want (3140001,hw-1)", gotNumber, gotHW)
	}
}

func TestPublishReconnectPublishesEnvelope(t *testing.T) {
	hub := NewHub()
	fb := newFakeRedis()
	hub.SetRedis(fb)
	hub.PublishReconnect("3140001", "hw-1")
	envs := fb.publishedEnvelopes()
	if len(envs) != 1 {
		t.Fatalf("published %d envelopes, want 1", len(envs))
	}
	env := envs[0]
	if env.TargetType != "reconnect" || env.Target != "3140001" || env.Message == nil || env.Message.HardwareID != "hw-1" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}

func TestOnReconnectCancelsLocalTimerAndPublishes(t *testing.T) {
	hub := NewHub()
	fake := newFakeRedis()
	hub.SetRedis(fake)
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = time.Hour

	relay.startGraceTimer("3140001", "hw-1", "3140002")
	relay.OnReconnect(context.Background(), "3140001", "hw-1")

	if relay.cancelGraceLocal("3140001", "hw-1") {
		t.Fatal("grace timer still present after OnReconnect")
	}
	envs := fake.publishedEnvelopes()
	if len(envs) != 1 || envs[0].TargetType != "reconnect" {
		t.Fatalf("expected one reconnect publish, got %+v", envs)
	}
}

func TestHandleRemoteReconnectCancelsWithoutPublishing(t *testing.T) {
	hub := NewHub()
	fake := newFakeRedis()
	hub.SetRedis(fake)
	relay := NewRelay(hub, newMockTracker(), nil, nil)
	relay.GraceWindow = time.Hour

	relay.startGraceTimer("3140001", "hw-1", "3140002")
	relay.HandleRemoteReconnect("3140001", "hw-1")

	if relay.cancelGraceLocal("3140001", "hw-1") {
		t.Fatal("grace timer still present after HandleRemoteReconnect")
	}
	if envs := fake.publishedEnvelopes(); len(envs) != 0 {
		t.Fatalf("HandleRemoteReconnect published %d envelopes, want 0", len(envs))
	}
}

func TestDeliverFromRedisReconnectNilHookNoPanic(t *testing.T) {
	hub := NewHub() // no hook set
	hub.deliverFromRedis(&Envelope{TargetType: "reconnect", Target: "x", Message: &Message{HardwareID: "y"}})
	// reaching here without panic is the assertion
}

func TestPublishReconnectNoRedisIsNoop(t *testing.T) {
	hub := NewHub()                         // no redis configured
	hub.PublishReconnect("3140001", "hw-1") // must not panic, no-op
}
