package main

import (
	"testing"
	"time"

	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
)

// newIdlePeerManager returns a peer that never leaves the New state: no
// offer/answer, no ICE agent, so a connect watchdog armed on it can only
// expire.
func newIdlePeerManager(t *testing.T) *owebrtc.PeerManager {
	t.Helper()
	pm, err := owebrtc.NewPeerManager(owebrtc.NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pm.Close() })
	return pm
}

func TestConnectWatchdog_FailsCallWhenPeerNeverConnects(t *testing.T) {
	fc := &fakeController{}
	d := newDispatchDaemon(t, fc)
	pm := newIdlePeerManager(t)
	d.mu.Lock()
	d.peerMgr = pm
	d.mu.Unlock()

	d.runConnectWatchdog(pm, "3140001", 100*time.Millisecond)

	got, ok := fc.lastSignal()
	if !ok || got != (signalCall{"connect_failed", "3140001"}) {
		t.Fatalf("watchdog signalled %+v, want HandleSignal(connect_failed, 3140001)", got)
	}
}

func TestConnectWatchdog_NoOpAfterPeerReplaced(t *testing.T) {
	fc := &fakeController{}
	d := newDispatchDaemon(t, fc)
	pm := newIdlePeerManager(t)
	// The call was torn down (or a new one started) before the deadline:
	// d.peerMgr no longer points at the peer the watchdog was armed for.
	d.mu.Lock()
	d.peerMgr = nil
	d.mu.Unlock()

	d.runConnectWatchdog(pm, "3140001", 100*time.Millisecond)

	if got, ok := fc.lastSignal(); ok {
		t.Fatalf("watchdog signalled %+v on a replaced peer, want nothing", got)
	}
}
