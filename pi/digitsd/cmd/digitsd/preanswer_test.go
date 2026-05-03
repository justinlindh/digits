package main

import (
	"testing"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
)

type nopWriter struct{}

func (nopWriter) WriteFrame([]int16) error { return nil }
func (nopWriter) PeriodSize() int          { return 960 }

func newTestDaemon() *daemonCallbacks {
	d := &daemonCallbacks{}
	d.mixer = audio.NewMixer(nopWriter{})
	return d
}

func TestPrepareAnswer_CreatesState(t *testing.T) {
	d := newTestDaemon()

	caller, err := owebrtc.NewPeerManager(owebrtc.NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = caller.Close() }()

	offer, err := caller.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}

	d.pendingCaller = "3140001"
	d.pendingOffer = offer

	d.prepareAnswer()

	if d.preAnswer.peerMgr == nil {
		t.Fatal("expected preAnswer.peerMgr to be set")
	}
	if d.preAnswer.answerSDP == "" {
		t.Fatal("expected preAnswer.answerSDP to be non-empty")
	}
	if d.preAnswer.caller != "3140001" {
		t.Fatalf("expected caller 3140001, got %q", d.preAnswer.caller)
	}
	if d.pendingICE != nil {
		t.Fatal("expected pendingICE to be cleared")
	}

	_ = d.preAnswer.peerMgr.Close()
}

func TestPrepareAnswer_NoopWithoutOffer(t *testing.T) {
	d := newTestDaemon()
	d.pendingCaller = "3140001"
	d.pendingOffer = ""

	d.prepareAnswer()

	if d.preAnswer.peerMgr != nil {
		t.Fatal("expected preAnswer.peerMgr to remain nil without offer")
	}
}

func TestPrepareAnswer_Idempotent(t *testing.T) {
	d := newTestDaemon()

	caller, err := owebrtc.NewPeerManager(owebrtc.NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = caller.Close() }()

	offer, err := caller.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}

	d.pendingCaller = "3140001"
	d.pendingOffer = offer

	d.prepareAnswer()
	firstPM := d.preAnswer.peerMgr

	d.prepareAnswer()

	if d.preAnswer.peerMgr != firstPM {
		t.Fatal("expected prepareAnswer to be idempotent")
	}

	_ = d.preAnswer.peerMgr.Close()
}

func TestCleanupPreAnswer_ClosesAndZeros(t *testing.T) {
	d := newTestDaemon()

	caller, err := owebrtc.NewPeerManager(owebrtc.NewICEConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = caller.Close() }()

	offer, err := caller.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}

	d.pendingCaller = "3140001"
	d.pendingOffer = offer
	d.prepareAnswer()

	if d.preAnswer.peerMgr == nil {
		t.Fatal("setup: preAnswer.peerMgr should exist")
	}

	d.cleanupPreAnswer()

	if d.preAnswer.peerMgr != nil {
		t.Fatal("expected preAnswer.peerMgr to be nil after cleanup")
	}
	if d.preAnswer.answerSDP != "" {
		t.Fatal("expected preAnswer.answerSDP to be empty after cleanup")
	}
	if d.preAnswer.caller != "" {
		t.Fatal("expected preAnswer.caller to be empty after cleanup")
	}
}

func TestCleanupPreAnswer_NoopWhenEmpty(t *testing.T) {
	d := newTestDaemon()
	d.cleanupPreAnswer()
}
