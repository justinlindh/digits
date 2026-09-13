package main

import (
	"testing"

	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
)

// newIdlePeerManager returns a peer that never leaves the New state: no
// offer/answer, no ICE agent, so a connect deadline on it can only expire.
func newIdlePeerManager(t *testing.T) *owebrtc.PeerManager {
	t.Helper()
	pm, err := owebrtc.NewPeerManager(owebrtc.NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pm.Close() })
	return pm
}

func TestConnectDeadline_FailsCallWhenPeerNeverConnected(t *testing.T) {
	fc := &fakeController{}
	d := newDispatchDaemon(t, fc)
	pm := newIdlePeerManager(t)
	d.mu.Lock()
	d.peerMgr = pm
	d.mu.Unlock()

	d.connectDeadline(pm, "3140001")

	got, ok := fc.lastHangup()
	if !ok || got != (hangupCall{"3140001", sigclient.HangupReasonConnectTimeout}) {
		t.Fatalf("deadline reported %+v, want HandleHangup(3140001, connect_timeout)", got)
	}
}

func TestConnectDeadline_NoOpAfterPeerReplaced(t *testing.T) {
	fc := &fakeController{}
	d := newDispatchDaemon(t, fc)
	// The call was torn down (or a new one started) before the deadline:
	// d.peerMgr no longer points at the peer the timer was armed for.
	pm := newIdlePeerManager(t)

	d.connectDeadline(pm, "3140001")

	if got, ok := fc.lastHangup(); ok {
		t.Fatalf("deadline reported %+v on a replaced peer, want nothing", got)
	}
}

func TestConnectTimer_ArmAndCancel(t *testing.T) {
	d := newDispatchDaemon(t, &fakeController{})
	pm := newIdlePeerManager(t)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.peerMgr = pm

	d.armConnectTimerLocked("3140001")
	if d.connectTimer == nil {
		t.Fatal("arm did not start the connect timer")
	}
	d.cancelConnectTimerLocked()
	if d.connectTimer != nil {
		t.Fatal("cancel left the connect timer armed")
	}
}
