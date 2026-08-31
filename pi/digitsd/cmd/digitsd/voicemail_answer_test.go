package main

import (
	"testing"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	"github.com/justinlindh/digits/pi/digitsd/internal/voicemail"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
	"github.com/pion/webrtc/v4"
)

// newVoicemailTestDaemon builds a daemon ready for VoicemailAutoAnswer: an
// unconnected signaling client (sends fail gracefully and are logged), a real
// store in a temp dir, and a pre-built pipeline so the ALSA-backed Start path
// is skipped. The connect timeout is huge so the greeting goroutine parks
// instead of touching the nil controller.
func newVoicemailTestDaemon(t *testing.T) *daemonCallbacks {
	t.Helper()
	d := newTestDaemon()
	d.sig = sigclient.NewClient("ws://127.0.0.1:1", "7390000", "test-hw", "tok")
	store, err := voicemail.Open(t.TempDir(), voicemail.Options{})
	if err != nil {
		t.Fatal(err)
	}
	d.voicemailStore = store
	d.pipeline = audio.NewPipeline(audio.DefaultPipelineConfig())
	d.voicemailConnectTimeout = time.Hour
	return d
}

func TestVoicemailAutoAnswer_PromotesPreparedPeer(t *testing.T) {
	d := newVoicemailTestDaemon(t)

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
	prepared := d.preAnswer.peerMgr
	d.mu.Unlock()
	if prepared == nil {
		t.Fatal("setup: prepareAnswer did not create a peer")
	}

	if !d.VoicemailAutoAnswer() {
		t.Fatal("expected auto-answer to succeed")
	}

	d.mu.Lock()
	promoted := d.peerMgr
	preAnswerPM := d.preAnswer.peerMgr
	answerSDP := d.preAnswer.answerSDP
	callPeer := d.callPeer
	vmCh := d.voicemailWebRTCCh
	d.mu.Unlock()

	if promoted != prepared {
		t.Fatal("expected voicemail to promote the prepared peer, got a different PeerManager")
	}
	if preAnswerPM != nil || answerSDP != "" {
		t.Fatal("expected preAnswer state to be cleared after promotion")
	}
	if callPeer != "3140001" {
		t.Fatalf("callPeer = %q, want 3140001", callPeer)
	}
	if vmCh == nil {
		t.Fatal("expected voicemailWebRTCCh to be set")
	}
	d.recorderMu.Lock()
	rec := d.recorder
	d.recorderMu.Unlock()
	if rec == nil {
		t.Fatal("expected an open recorder")
	}
	_ = promoted.Close()
}

func TestVoicemailAutoAnswer_BuildsPeerWithoutPrepared(t *testing.T) {
	d := newVoicemailTestDaemon(t)

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
	d.mu.Unlock()

	if !d.VoicemailAutoAnswer() {
		t.Fatal("expected auto-answer to succeed without a prepared peer")
	}

	d.mu.Lock()
	pm := d.peerMgr
	d.mu.Unlock()
	if pm == nil {
		t.Fatal("expected a PeerManager to be built")
	}
	_ = pm.Close()
}

func TestWaitForPeerConnected_Connected(t *testing.T) {
	state := func() webrtc.PeerConnectionState { return webrtc.PeerConnectionStateConnected }
	start := time.Now()
	if !waitForPeerConnected(state, time.Second) {
		t.Fatal("expected true for a connected peer")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("expected an already-connected peer to return promptly")
	}
}

func TestWaitForPeerConnected_Timeout(t *testing.T) {
	state := func() webrtc.PeerConnectionState { return webrtc.PeerConnectionStateConnecting }
	if waitForPeerConnected(state, 120*time.Millisecond) {
		t.Fatal("expected false when the peer never connects")
	}
}

func TestWaitForPeerConnected_FailedShortCircuits(t *testing.T) {
	state := func() webrtc.PeerConnectionState { return webrtc.PeerConnectionStateFailed }
	start := time.Now()
	if waitForPeerConnected(state, time.Hour) {
		t.Fatal("expected false for a failed peer")
	}
	if time.Since(start) > time.Second {
		t.Fatal("expected a failed peer to short-circuit, not wait out the timeout")
	}
}

func TestWaitForPeerConnected_ClosedShortCircuits(t *testing.T) {
	state := func() webrtc.PeerConnectionState { return webrtc.PeerConnectionStateClosed }
	start := time.Now()
	if waitForPeerConnected(state, time.Hour) {
		t.Fatal("expected false for a closed peer")
	}
	if time.Since(start) > time.Second {
		t.Fatal("expected a closed peer to short-circuit, not wait out the timeout")
	}
}
