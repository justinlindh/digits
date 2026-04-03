package owebrtc

import "testing"

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
	defer mgr.Close()

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
	defer caller.Close()

	callee, err := NewPeerManager(NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()

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
