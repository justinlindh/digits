package owebrtc

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestICEConfig_NoServers(t *testing.T) {
	cfg := NewICEConfig(nil)
	wc := cfg.WebRTCConfig()
	if len(wc.ICEServers) != 0 {
		t.Fatalf("expected 0 ICE servers, got %d", len(wc.ICEServers))
	}
}

func TestICEConfig_WithTURN(t *testing.T) {
	servers := []ICEServerConfig{
		{URLs: []string{"stun:turn.digits.family:3478"}},
		{URLs: []string{"turn:turn.digits.family:3478"}, Username: "u", Credential: "c"},
	}
	cfg := NewICEConfig(servers)
	wc := cfg.WebRTCConfig()
	if len(wc.ICEServers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(wc.ICEServers))
	}
	if wc.ICEServers[1].Username != "u" {
		t.Fatal("expected username u")
	}
}

func TestPeerManager_CreateOffer(t *testing.T) {
	mgr, err := NewPeerManager(NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	offer, err := mgr.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}
	if offer == "" {
		t.Fatal("empty offer")
	}
	if len(offer) < 100 {
		t.Fatal("SDP too short")
	}
}

func TestPeerManager_OfferAnswer(t *testing.T) {
	// Create two peers, exchange SDP
	caller, err := NewPeerManager(NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = caller.Close() }()

	callee, err := NewPeerManager(NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = callee.Close() }()

	offer, err := caller.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}

	answer, err := callee.AcceptOffer(offer)
	if err != nil {
		t.Fatal(err)
	}

	err = caller.SetAnswer(answer)
	if err != nil {
		t.Fatal(err)
	}
	// If we get here without error, SDP exchange succeeded
}

// waitGatherComplete blocks until ICE gathering is complete or timeout.
func waitGatherComplete(t *testing.T, pm *PeerManager) {
	t.Helper()
	done := make(chan struct{}, 1)
	pm.pc.OnICEGatheringStateChange(func(state webrtc.ICEGatheringState) {
		if state == webrtc.ICEGatheringStateComplete {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	})
	if pm.pc.ICEGatheringState() == webrtc.ICEGatheringStateComplete {
		return
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ICE gathering to complete")
	}
}

func TestPeerManager_SetOutboundMuted(t *testing.T) {
	pm, err := NewPeerManager(NewICEConfig(nil))
	if err != nil {
		t.Fatalf("NewPeerManager: %v", err)
	}
	defer func() { _ = pm.Close() }()

	if pm.OutboundMuted() {
		t.Fatalf("expected unmuted by default")
	}
	pm.SetOutboundMuted(true)
	if !pm.OutboundMuted() {
		t.Fatalf("expected muted after SetOutboundMuted(true)")
	}
	pm.SetOutboundMuted(false)
	if pm.OutboundMuted() {
		t.Fatalf("expected unmuted after SetOutboundMuted(false)")
	}
}

func TestPeerManager_ICERestart(t *testing.T) {
	caller, err := NewPeerManager(NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = caller.Close() }()

	callee, err := NewPeerManager(NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = callee.Close() }()

	// Initial SDP exchange
	offer, err := caller.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}
	answer, err := callee.AcceptOffer(offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := caller.SetAnswer(answer); err != nil {
		t.Fatal(err)
	}

	// Wait for both sides to finish gathering before restart
	waitGatherComplete(t, caller)
	waitGatherComplete(t, callee)

	// ICE restart: caller creates restart offer, callee accepts
	restartOffer, err := caller.CreateRestartOffer()
	if err != nil {
		t.Fatal(err)
	}
	if restartOffer == "" {
		t.Fatal("empty restart offer")
	}

	restartAnswer, err := callee.AcceptOffer(restartOffer)
	if err != nil {
		t.Fatal(err)
	}
	if restartAnswer == "" {
		t.Fatal("empty restart answer")
	}

	if err := caller.SetAnswer(restartAnswer); err != nil {
		t.Fatal(err)
	}
}
