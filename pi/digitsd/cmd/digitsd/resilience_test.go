package main

import (
	"testing"

	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
	"github.com/pion/webrtc/v4"
)

func TestConnStateAction(t *testing.T) {
	cases := []struct {
		name            string
		state           webrtc.PeerConnectionState
		recovering      bool
		debouncePending bool
		want            connAction
	}{
		{"connected clears", webrtc.PeerConnectionStateConnected, true, true, actionClearRecovery},
		{"disconnected fresh starts debounce", webrtc.PeerConnectionStateDisconnected, false, false, actionStartDebounce},
		{"disconnected while debouncing is noop", webrtc.PeerConnectionStateDisconnected, false, true, actionNone},
		{"disconnected while recovering is noop", webrtc.PeerConnectionStateDisconnected, true, false, actionNone},
		{"failed fresh enters recovery", webrtc.PeerConnectionStateFailed, false, false, actionEnterRecovery},
		{"failed while recovering is noop", webrtc.PeerConnectionStateFailed, true, false, actionNone},
		{"connecting is noop", webrtc.PeerConnectionStateConnecting, false, false, actionNone},
		{"closed is noop", webrtc.PeerConnectionStateClosed, false, false, actionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := connStateAction(tc.state, tc.recovering, tc.debouncePending)
			if got != tc.want {
				t.Fatalf("connStateAction(%v, recovering=%v, debounce=%v) = %v, want %v",
					tc.state, tc.recovering, tc.debouncePending, got, tc.want)
			}
		})
	}
}

func TestReconnectAction(t *testing.T) {
	cases := []struct {
		name      string
		ctrlState phone.State
		hasMesh   bool
		hasPeer   bool
		connState webrtc.PeerConnectionState
		want      reconnAction
	}{
		{"connected 2party media-up resumes noop", phone.StateCONNECTED, false, true, webrtc.PeerConnectionStateConnected, reconnResumeNoop},
		{"connected 2party media-down restarts", phone.StateCONNECTED, false, true, webrtc.PeerConnectionStateDisconnected, reconnResumeRestart},
		{"connected 2party media-failed restarts", phone.StateCONNECTED, false, true, webrtc.PeerConnectionStateFailed, reconnResumeRestart},
		{"conference tears down", phone.StateCONFERENCE_MERGED, true, true, webrtc.PeerConnectionStateConnected, reconnTeardown},
		{"no peer tears down", phone.StateCONNECTED, false, false, webrtc.PeerConnectionStateConnected, reconnTeardown},
		{"ringing tears down", phone.StateRINGING, false, true, webrtc.PeerConnectionStateConnected, reconnTeardown},
		{"voicemail recording tears down", phone.StateVOICEMAIL_RECORDING, false, true, webrtc.PeerConnectionStateConnected, reconnTeardown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reconnectAction(tc.ctrlState, tc.hasMesh, tc.hasPeer, tc.connState)
			if got != tc.want {
				t.Fatalf("reconnectAction(%v, mesh=%v, peer=%v, %v) = %v, want %v",
					tc.ctrlState, tc.hasMesh, tc.hasPeer, tc.connState, got, tc.want)
			}
		})
	}
}

func newTestPeerManager(t *testing.T) *owebrtc.PeerManager {
	t.Helper()
	pm, err := owebrtc.NewPeerManager(owebrtc.NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pm.Close() })
	// Drive a full offer/answer handshake against a throwaway remote peer so the
	// local PC reaches the stable signaling state with a live ICE agent. Recovery
	// only ever runs on an established peer, where CreateRestartOffer can rotate
	// credentials; a fresh PC has no ICE agent and CreateRestartOffer errors.
	remote, err := owebrtc.NewPeerManager(owebrtc.NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })
	offer, err := pm.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}
	answer, err := remote.AcceptOffer(offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := pm.SetAnswer(answer); err != nil {
		t.Fatal(err)
	}
	return pm
}

func TestEnterICERecoveryCallerArmsTimerAndRecovers(t *testing.T) {
	pm := newTestPeerManager(t)
	// An unconnected client makes sendSignal return an error cleanly instead of
	// dereferencing a nil receiver; the recovery bookkeeping still stands.
	sig := sigclient.NewClient("ws://127.0.0.1:0/ws", "3140000", "hw", "tok")
	d := &daemonCallbacks{peerMgr: pm, isCaller: true, callPeer: "3140002", sig: sig}
	d.enterICERecovery(pm, "test")

	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.isRestartingICE {
		t.Fatal("caller recovery did not set isRestartingICE")
	}
	if d.restartTimer == nil {
		t.Fatal("caller recovery did not arm the restart timeout")
	}
}

func TestEnterICERecoveryCalleeWaitsWithoutOffer(t *testing.T) {
	pm := newTestPeerManager(t)
	d := &daemonCallbacks{peerMgr: pm, isCaller: false, callPeer: "3140001"}
	d.enterICERecovery(pm, "test")

	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.isRestartingICE {
		t.Fatal("callee recovery did not set isRestartingICE")
	}
	if d.restartTimer == nil {
		t.Fatal("callee recovery did not arm the wait timeout")
	}
}

func TestEnterICERecoveryIdempotentWhenAlreadyRecovering(t *testing.T) {
	pm := newTestPeerManager(t)
	d := &daemonCallbacks{peerMgr: pm, isCaller: true, callPeer: "x", isRestartingICE: true}
	d.enterICERecovery(pm, "test") // must be a no-op
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.restartTimer != nil {
		t.Fatal("second recovery armed a duplicate timer")
	}
}

func TestHandleConnStateFailedWhileRecoveringDoesNotHangUp(t *testing.T) {
	pm := newTestPeerManager(t)
	// nil ctrl: triggerHangup would early-return on nil ctrl anyway, but the
	// point is the actionNone branch must not even attempt teardown. We assert
	// recovery state is left intact (isRestartingICE stays true, no panic).
	d := &daemonCallbacks{peerMgr: pm, isCaller: true, callPeer: "x", isRestartingICE: true}
	// restartTimer would normally be armed during recovery; simulate that.
	d.mu.Lock()
	d.startRestartTimeout()
	d.mu.Unlock()

	d.handleConnectionStateChange(pm, webrtc.PeerConnectionStateFailed)

	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.isRestartingICE {
		t.Fatal("Failed-while-recovering cleared recovery state; it must let the deadline timer govern")
	}
	if d.restartTimer != nil {
		// Timer is still armed (not stopped by a teardown); stop it so the test
		// does not leave a 25s AfterFunc dangling.
		d.restartTimer.Stop()
	} else {
		t.Fatal("Failed-while-recovering stopped the restart timer; deadline must still govern")
	}
}
