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
	tracker.peers = map[string]string{"3140001": "3140002"}
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
	if len(tracker.ended) != 1 || tracker.ended[0] != "3140001→3140002" {
		t.Fatalf("OnCallEnded = %v, want [3140001->3140002]", tracker.ended)
	}
}

func TestCancelGraceLocalPreventsTeardown(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	tracker.peers = map[string]string{"3140001": "3140002"}
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
	if len(tracker.ended) != 0 {
		t.Fatalf("OnCallEnded called %v; expected none", tracker.ended)
	}
}

func TestStartGraceTimerReplacesExistingTimer(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	tracker.peers = map[string]string{"3140001": "3140002"}
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
	if len(tracker.ended) != 1 {
		t.Fatalf("OnCallEnded = %v, want exactly 1", tracker.ended)
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
	if len(tracker.ended) != 0 {
		t.Fatalf("OnCallEnded called immediately (held, nothing ended yet): %v", tracker.ended)
	}
	if !relay.cancelGraceLocal("3140001", "hw-1") {
		t.Fatal("no grace timer pending after in-call disconnect")
	}
}

// A device that reconnects with the same hardware_id while its previous
// connection is still registered must not have its live call torn down. The
// new conn replaces the old in place and runs OnReconnect (which finds no
// timer yet); then the old conn's read loop unwinds. OnConnClosed must notice
// the old conn was superseded and skip teardown, so no orphaned grace timer is
// left to expire and hang up the reconnected call.
func TestOnConnClosedSkipsTeardownForSupersededReconnect(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	tracker.peers = map[string]string{"3140001": "3140002"}
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = time.Hour // a wrongly-started timer would not fire during the test

	peer := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", peer)

	oldConn := &Conn{HardwareID: "hw-1", Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", oldConn)
	newConn := &Conn{HardwareID: "hw-1", Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", newConn) // replaces oldConn in place

	// New conn's register path: cancels any pending timer (there is none yet).
	relay.OnReconnect(context.Background(), "3140001", "hw-1")

	// Old conn's read loop finally unwinds and reports the disconnect.
	relay.OnConnClosed(context.Background(), oldConn)

	if relay.cancelGraceLocal("3140001", "hw-1") {
		t.Fatal("an orphaned grace timer was started for a reconnected device")
	}
	if len(tracker.ended) != 0 {
		t.Fatalf("OnCallEnded called %v; the reconnected call must be left intact", tracker.ended)
	}
}

// A reconnect that lands between OnConnClosed's ConnIsCurrent check and the
// grace timer being armed finds no timer to cancel, so the orphaned timer
// survives to expiry. The fire-time presence recheck must notice the device
// is connected again and keep the call instead of hanging up the peer.
func TestGraceExpiryKeepsCallWhenReconnectRacesTimerArm(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	tracker.peers = map[string]string{"3140001": "3140002"}
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = 30 * time.Millisecond

	peer := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", peer)

	oldConn := &Conn{HardwareID: "hw-1", Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", oldConn)

	// The old conn's read loop passes OnConnClosed's ConnIsCurrent check and
	// is then preempted. The device reconnects: Register replaces the conn in
	// place and OnReconnect finds no grace timer to cancel yet.
	newConn := &Conn{HardwareID: "hw-1", Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", newConn)
	relay.OnReconnect(context.Background(), "3140001", "hw-1")

	// The old handler resumes and arms a grace timer for a live device.
	relay.OnDisconnect(context.Background(), "3140001", "hw-1")

	// Window expires: the recheck sees hw-1 registered and keeps the call.
	time.Sleep(120 * time.Millisecond)
	if len(tracker.ended) != 0 {
		t.Fatalf("call torn down despite device being connected: ended=%v", tracker.ended)
	}
	select {
	case <-peer.Send:
		t.Fatal("peer received hangup despite reconnect")
	default:
	}
}

// The expiry recheck matches on line + hardware id: a genuinely departed
// device (unregistered before expiry) must still be torn down.
func TestGraceExpiryTearsDownWhenDeviceStaysGone(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	tracker.peers = map[string]string{"3140001": "3140002"}
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = 30 * time.Millisecond

	peer := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", peer)

	conn := &Conn{HardwareID: "hw-1", Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)

	// Real disconnect order: OnConnClosed (arms the timer), then Unregister.
	relay.OnConnClosed(context.Background(), conn)
	hub.Unregister("3140001", conn)

	select {
	case data := <-peer.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeHangup {
			t.Fatalf("peer got %q, want hangup", msg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("peer never received hangup after grace expiry")
	}
	if len(tracker.ended) != 1 {
		t.Fatalf("OnCallEnded = %v, want exactly 1", tracker.ended)
	}
}

// A genuine last-device disconnect (conn still current, not superseded) must
// still hold the call open with a grace timer. Guards against the reconnect
// fix over-suppressing normal teardown.
func TestOnConnClosedHoldsCallForGenuineDisconnect(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	tracker.peers = map[string]string{"3140001": "3140002"}
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = time.Hour

	peer := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", peer)

	// OnConnClosed runs before Unregister, so the departing conn is still the
	// hub's current conn for its line: teardown proceeds into the grace path.
	conn := &Conn{HardwareID: "hw-1", Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)

	relay.OnConnClosed(context.Background(), conn)

	if !relay.cancelGraceLocal("3140001", "hw-1") {
		t.Fatal("expected a grace timer for a genuine in-call disconnect")
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

func TestGraceLifecycleReconnectWithinWindowKeepsCall(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	tracker.peers = map[string]string{"3140001": "3140002", "3140002": "3140001"}
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = 80 * time.Millisecond

	peer := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", peer)

	relay.OnDisconnect(context.Background(), "3140001", "hw-1")

	time.Sleep(20 * time.Millisecond)
	relay.OnReconnect(context.Background(), "3140001", "hw-1")

	time.Sleep(120 * time.Millisecond)
	if len(tracker.ended) != 0 {
		t.Fatalf("call torn down despite reconnect: ended=%v", tracker.ended)
	}
	select {
	case <-peer.Send:
		t.Fatal("peer received hangup despite reconnect")
	default:
	}
}

func TestGraceLifecycleExpiryTearsDownAndNotifiesPeer(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	tracker.peers = map[string]string{"3140001": "3140002", "3140002": "3140001"}
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = 30 * time.Millisecond

	peer := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", peer)

	relay.OnDisconnect(context.Background(), "3140001", "hw-1")

	select {
	case data := <-peer.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeHangup {
			t.Fatalf("peer got %q, want hangup", msg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("peer never notified after grace expiry")
	}
	if len(tracker.ended) != 1 || tracker.ended[0] != "3140001→3140002" {
		t.Fatalf("OnCallEnded = %v, want [3140001->3140002]", tracker.ended)
	}
}
