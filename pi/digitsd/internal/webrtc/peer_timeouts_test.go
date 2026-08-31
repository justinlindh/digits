package owebrtc

import (
	"testing"
	"time"
)

func TestNewPeerManagerWithTimeouts(t *testing.T) {
	pm, err := NewPeerManagerWithTimeouts(NewICEConfig(nil), 5*time.Second, 85*time.Second, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pm.Close() }()

	offer, err := pm.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}
	if offer == "" {
		t.Fatal("expected a non-empty offer from a timeout-configured peer")
	}
}
