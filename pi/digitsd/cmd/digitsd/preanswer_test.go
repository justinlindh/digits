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

	d.mu.Lock()
	d.pendingCaller = "3140001"
	d.pendingOffer = offer
	d.pendingICE = []string{"candidate:1 1 udp 2130706431 127.0.0.1 5000 typ host"}
	d.prepareAnswer()
	pm := d.preAnswer.peerMgr
	answerSDP := d.preAnswer.answerSDP
	callerField := d.preAnswer.caller
	pendingICE := d.pendingICE
	d.mu.Unlock()

	if pm == nil {
		t.Fatal("expected preAnswer.peerMgr to be set")
	}
	if answerSDP == "" {
		t.Fatal("expected preAnswer.answerSDP to be non-empty")
	}
	if callerField != "3140001" {
		t.Fatalf("expected caller 3140001, got %q", callerField)
	}
	if pendingICE != nil {
		t.Fatal("expected pendingICE to be cleared")
	}

	_ = pm.Close()
}

func TestPrepareAnswer_NoopWithoutOffer(t *testing.T) {
	d := newTestDaemon()

	d.mu.Lock()
	d.pendingCaller = "3140001"
	d.pendingOffer = ""
	d.prepareAnswer()
	pm := d.preAnswer.peerMgr
	d.mu.Unlock()

	if pm != nil {
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

	d.mu.Lock()
	d.pendingCaller = "3140001"
	d.pendingOffer = offer
	d.prepareAnswer()
	firstPM := d.preAnswer.peerMgr
	d.prepareAnswer()
	secondPM := d.preAnswer.peerMgr
	d.mu.Unlock()

	if secondPM != firstPM {
		t.Fatal("expected prepareAnswer to be idempotent")
	}

	_ = firstPM.Close()
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

	d.mu.Lock()
	d.pendingCaller = "3140001"
	d.pendingOffer = offer
	d.prepareAnswer()

	if d.preAnswer.peerMgr == nil {
		d.mu.Unlock()
		t.Fatal("setup: preAnswer.peerMgr should exist")
	}

	d.cleanupPreAnswer()
	pm := d.preAnswer.peerMgr
	sdp := d.preAnswer.answerSDP
	c := d.preAnswer.caller
	d.mu.Unlock()

	if pm != nil {
		t.Fatal("expected preAnswer.peerMgr to be nil after cleanup")
	}
	if sdp != "" {
		t.Fatal("expected preAnswer.answerSDP to be empty after cleanup")
	}
	if c != "" {
		t.Fatal("expected preAnswer.caller to be empty after cleanup")
	}
}

func TestCleanupPreAnswer_NoopWhenEmpty(t *testing.T) {
	d := newTestDaemon()
	d.mu.Lock()
	d.cleanupPreAnswer()
	d.mu.Unlock()
}

