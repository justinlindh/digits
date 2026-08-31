package main

import (
	"testing"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	"github.com/justinlindh/digits/pi/digitsd/internal/voicemail"
	"github.com/pion/webrtc/v4"
)

// newVoicemailTestDaemon builds a daemon ready for VoicemailAutoAnswer: an
// unconnected signaling client (sends fail gracefully and are logged), a real
// store in a temp dir, a pre-built pipeline so the ALSA-backed Start path is
// skipped, and a real idle controller so the greeting goroutine's abandon
// path (woken when the test closes the peer) is a clean no-op.
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
	d.ctrl = phone.NewController(d, "7390000")
	return d
}

func TestVoicemailAutoAnswer_PromotesPreparedPeer(t *testing.T) {
	d := newVoicemailTestDaemon(t)
	offer := newCallerOffer(t)

	d.mu.Lock()
	d.pendingCaller = "3140001"
	d.pendingOffer = offer
	d.prepareAnswer()
	prepared := d.preAnswer.pm
	d.mu.Unlock()
	if prepared == nil {
		t.Fatal("setup: prepareAnswer did not create a peer")
	}

	if !d.VoicemailAutoAnswer() {
		t.Fatal("expected auto-answer to succeed")
	}

	d.mu.Lock()
	promoted := d.peerMgr
	preAnswerPM := d.preAnswer.pm
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

func TestVoicemailAutoAnswer_PreparesWhenNotPrepared(t *testing.T) {
	d := newVoicemailTestDaemon(t)
	offer := newCallerOffer(t)

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
		t.Fatal("expected a PeerManager to be prepared and promoted")
	}
	_ = pm.Close()
}

func TestVoicemailAutoAnswer_RebuildsWhenPreparedPeerDead(t *testing.T) {
	d := newVoicemailTestDaemon(t)
	offer := newCallerOffer(t)

	d.mu.Lock()
	d.pendingCaller = "3140001"
	d.pendingOffer = offer
	d.prepareAnswer()
	prepared := d.preAnswer.pm
	d.mu.Unlock()
	if prepared == nil {
		t.Fatal("setup: prepareAnswer did not create a peer")
	}
	// Simulate an agent that died during a ring longer than its deadline.
	_ = prepared.Close()

	if !d.VoicemailAutoAnswer() {
		t.Fatal("expected auto-answer to rebuild and succeed")
	}

	d.mu.Lock()
	pm := d.peerMgr
	d.mu.Unlock()
	if pm == nil || pm == prepared {
		t.Fatal("expected a fresh peer, not the dead prepared one")
	}
	_ = pm.Close()
}

func TestAnswerCall_RebuildsWhenPreparedPeerDead(t *testing.T) {
	d := newVoicemailTestDaemon(t)
	offer := newCallerOffer(t)

	d.mu.Lock()
	d.pendingCaller = "3140001"
	d.pendingOffer = offer
	d.prepareAnswer()
	prepared := d.preAnswer.pm
	d.mu.Unlock()
	if prepared == nil {
		t.Fatal("setup: prepareAnswer did not create a peer")
	}
	_ = prepared.Close()

	d.AnswerCall()

	d.mu.Lock()
	pm := d.peerMgr
	d.mu.Unlock()
	if pm == nil || pm == prepared {
		t.Fatal("expected a fresh peer, not the dead prepared one")
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

func TestWaitForPeerConnected_TerminalStatesShortCircuit(t *testing.T) {
	for _, st := range []webrtc.PeerConnectionState{
		webrtc.PeerConnectionStateFailed,
		webrtc.PeerConnectionStateClosed,
	} {
		t.Run(st.String(), func(t *testing.T) {
			state := func() webrtc.PeerConnectionState { return st }
			start := time.Now()
			if waitForPeerConnected(state, time.Hour) {
				t.Fatalf("expected false for a %s peer", st)
			}
			if time.Since(start) > time.Second {
				t.Fatalf("expected a %s peer to short-circuit, not wait out the timeout", st)
			}
		})
	}
}
