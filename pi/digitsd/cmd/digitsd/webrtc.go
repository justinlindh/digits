package main

// 2-party WebRTC callbacks and helpers. Cut from main.go to keep the
// daemon entrypoint focused on startup and shared infrastructure. The
// daemonCallbacks struct definition stays on main.go; only methods that
// drive the inbound / outbound 2-party WebRTC path live here.
//
// Conference (mesh) callbacks live in conference.go.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	"github.com/justinlindh/digits/pi/digitsd/internal/config"
	"github.com/justinlindh/digits/pi/digitsd/internal/devmode"
	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"

	"github.com/pion/webrtc/v4"
)

func (d *daemonCallbacks) InitiateCall(targetNumber string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check if the signaling server is reachable before setting up WebRTC.
	if err := d.sig.Send(&sigclient.Message{Type: sigclient.TypeCall, To: targetNumber}); err != nil {
		return fmt.Errorf("server unreachable: %w", err)
	}
	// TypeCall was sent. If any later step fails, send a hangup so the
	// callee does not ring until timeout.
	callSent := true
	defer func() {
		if callSent {
			sendSignal(d.sig, &sigclient.Message{Type: sigclient.TypeHangup, To: targetNumber})
		}
	}()

	iceCfg := owebrtc.NewICEConfig(d.iceServers)
	var err error
	d.peerMgr, err = owebrtc.NewPeerManager(iceCfg)
	if err != nil {
		slog.Error("webrtc: new peer manager failed", "error", err)
		return fmt.Errorf("webrtc setup: %w", err)
	}

	d.callPeer = targetNumber
	d.isCaller = true
	d.isRestartingICE = false

	callerPM := d.peerMgr
	d.peerMgr.OnConnectionState = func(state webrtc.PeerConnectionState) {
		d.handleConnectionStateChange(callerPM, state)
	}

	// Handle remote audio track
	webrtcCh := d.mixer.AddWebRTCSource(targetNumber)
	localPM := d.peerMgr // capture for goroutine; each PM owns its decoder
	d.peerMgr.OnRemoteTrack = func(track *webrtc.TrackRemote) {
		go func() {
			defer recoverGoroutine("caller-remote-track")
			// Live playback: decode and feed into mixer
			var frameCount int
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					slog.Info("makeCall remote track ended", "frames", frameCount)
					return
				}
				pcm, err := localPM.Decode(pkt.Payload)
				if err != nil {
					continue
				}
				if localPM.InboundMuted() {
					// Silent hold: drop decoded audio rather than feeding the mixer.
					continue
				}
				// Copy: Decode returns a slice of a reused internal buffer
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				frameCount++
				select {
				case webrtcCh <- frame:
				default:
					// Drop frame: mixer is behind
				}
			}
		}()
	}

	// Gate ICE candidates behind SDP send: candidates must not arrive before the offer.
	sdpSent := make(chan struct{})
	d.peerMgr.OnICECandidate = func(candidate string) {
		<-sdpSent
		sendSignal(d.currentSig(), &sigclient.Message{
			Type:      sigclient.TypeICE,
			To:        targetNumber,
			Candidate: candidate,
		})
	}

	// Create offer (returns immediately; ICE trickles via OnICECandidate)
	offer, err := d.peerMgr.CreateOffer()
	if err != nil {
		slog.Error("webrtc: create offer failed", "error", err)
		close(sdpSent)
		return fmt.Errorf("webrtc offer: %w", err)
	}

	// Send call + SDP, then ungate ICE candidates
	sendSignal(d.sig, &sigclient.Message{Type: sigclient.TypeSDP, To: targetNumber, SDP: offer})
	close(sdpSent)

	// Start audio pipeline. Skip if one is already running (ADD flow: the held
	// peer's pipeline stays alive across the flash-to-add transition, and the
	// existing encode loop picks up the new peerMgr via the per-iteration read
	// under d.mu).
	if d.pipeline == nil {
		d.pipeline = d.newPipeline()
		if err := d.pipeline.Start(); err != nil {
			slog.Error("audio pipeline start failed", "error", err)
			return fmt.Errorf("audio pipeline: %w", err)
		}

		d.startEncodeLoop()
	}

	slog.Info("call initiated", "target", targetNumber)
	callSent = false
	return nil
}

func (d *daemonCallbacks) AnswerCall() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.pendingOffer == "" {
		slog.Warn("answer: no pending offer")
		return
	}

	// Fast path: use pre-created PeerConnection from ring phase.
	if d.preAnswer.peerMgr != nil {
		t0 := time.Now()
		d.mixer.StopTone()
		caller := d.preAnswer.caller
		pm := d.preAnswer.peerMgr
		answerSDP := d.preAnswer.answerSDP
		candidates := d.preAnswer.candidates

		d.peerMgr = pm
		d.callPeer = caller
		d.isCaller = false
		d.isRestartingICE = false
		d.preAnswer.peerMgr = nil
		d.preAnswer.answerSDP = ""
		d.preAnswer.webrtcCh = nil
		d.preAnswer.candidates = nil
		d.preAnswer.caller = ""
		d.pendingOffer = ""
		d.pendingCaller = ""

		sendSignal(d.sig, &sigclient.Message{
			Type: sigclient.TypeAnswer,
			To:   caller,
			SDP:  answerSDP,
		})

		for _, candidate := range candidates {
			sendSignal(d.sig, &sigclient.Message{
				Type:      sigclient.TypeICE,
				To:        caller,
				Candidate: candidate,
			})
		}

		// Any candidates still gathering after promotion should be sent directly.
		pm.OnICECandidate = func(candidate string) {
			sendSignal(d.currentSig(), &sigclient.Message{
				Type:      sigclient.TypeICE,
				To:        caller,
				Candidate: candidate,
			})
		}

		d.pipeline = d.newPipeline()
		if err := d.pipeline.Start(); err != nil {
			slog.Error("audio pipeline (answer fast-path) start failed", "error", err)
			return
		}
		d.startEncodeLoop()

		slog.Info("answered call (fast path)", "caller", caller, "elapsed", time.Since(t0).Round(time.Millisecond))
		return
	}

	// Stop tones: mixer continues writing silence (DAC keepalive)
	d.mixer.StopTone()

	caller := d.pendingCaller
	offerSDP := d.pendingOffer
	d.pendingOffer = ""
	d.pendingCaller = ""

	d.callPeer = caller
	d.isCaller = false
	d.isRestartingICE = false

	iceCfg := owebrtc.NewICEConfig(d.iceServers)
	var err error
	d.peerMgr, err = owebrtc.NewPeerManager(iceCfg)
	if err != nil {
		slog.Error("webrtc (answer): new peer manager failed", "error", err)
		return
	}

	answerPM := d.peerMgr
	d.peerMgr.OnConnectionState = func(state webrtc.PeerConnectionState) {
		d.handleConnectionStateChange(answerPM, state)
	}

	webrtcCh := d.mixer.AddWebRTCSource(caller)
	d.peerMgr.OnRemoteTrack = d.remoteTrackHandler(answerPM, webrtcCh)

	// Gate ICE candidates behind answer SDP send.
	sdpSent := make(chan struct{})
	d.peerMgr.OnICECandidate = func(candidate string) {
		<-sdpSent
		sendSignal(d.currentSig(), &sigclient.Message{
			Type:      sigclient.TypeICE,
			To:        caller,
			Candidate: candidate,
		})
	}

	// Accept the offer and generate answer (returns immediately; ICE trickles via OnICECandidate)
	answerSDP, err := d.peerMgr.AcceptOffer(offerSDP)
	if err != nil {
		slog.Error("webrtc: accept offer failed", "error", err)
		close(sdpSent)
		return
	}

	// Drain any ICE candidates that arrived before peerMgr was ready.
	for _, candidate := range d.pendingICE {
		if err := d.peerMgr.AddICECandidate(candidate); err != nil {
			slog.Warn("webrtc: add queued ICE candidate failed", "error", err)
		}
	}
	slog.Info("drained queued ICE candidates", "count", len(d.pendingICE))
	d.pendingICE = nil

	// Send answer SDP back to caller, then ungate ICE candidates
	sendSignal(d.sig, &sigclient.Message{
		Type: sigclient.TypeAnswer,
		To:   caller,
		SDP:  answerSDP,
	})
	close(sdpSent)

	if d.pipeline == nil {
		d.pipeline = d.newPipeline()
		if err := d.pipeline.Start(); err != nil {
			slog.Error("audio pipeline (answer) start failed", "error", err)
			return
		}
		d.startEncodeLoop()
	}

	slog.Info("answered call", "caller", caller)
}

func (d *daemonCallbacks) HangupCall() {
	t0 := time.Now()
	d.mu.Lock()

	// Finalize any active voicemail recording so a hangup (local, remote,
	// or driven by VoicemailRecordEnded) never leaves an orphan .tmp file.
	// recorderMu is independent of d.mu, so no deadlock with the FSM lock.
	//
	// finalizedVoicemail captures whether we actually advanced the unheard
	// count. On the caller-hangup-during-recording path the controller has
	// already issued LED:OFF before HangupCall ran; the wrapper resolved
	// that against the count from BEFORE this message was finalized, so
	// the LED would stay off. We re-emit the LED state below (after the
	// lock is released) when this flag is set so a freshly recorded
	// message lights the indicator immediately. The at-cap path
	// (VoicemailRecordEnded) already cleared d.recorder before invoking
	// HangupCall, so this flag stays false there and the explicit
	// evaluateLED in VoicemailRecordEnded is what fires.
	var finalizedVoicemail bool
	d.recorderMu.Lock()
	if d.recorder != nil {
		msg, err := d.recorder.Finalize()
		if err != nil {
			slog.Error("hangup: voicemail finalize failed", "error", err)
		}
		d.recorder = nil
		// A zero-frame recording (the caller hung up during the greeting,
		// before the recorder was armed) is discarded by Finalize and
		// returns a zero Message: nothing landed, so the unheard count did
		// not move and the LED does not need re-emitting.
		finalizedVoicemail = msg.ID != 0
	}
	d.recorderMu.Unlock()

	// Finalize any active custom-greeting recording. Hangup during *97
	// preserves what the user spoke up to that point (answering-machine
	// convention). The encoder is paired with the recorder; clear both.
	// finalizeGreetingRecording may have already run via the # path, in
	// which case greetingRecorder is nil and this block no-ops.
	d.greetingRecorderMu.Lock()
	if d.greetingRecorder != nil {
		if _, err := d.greetingRecorder.Finalize(); err != nil {
			slog.Error("hangup: greeting finalize failed", "error", err)
		}
		d.greetingRecorder = nil
		d.greetingEncoder = nil
	}
	d.greetingRecorderMu.Unlock()

	// Call is tearing down. Drop the Pico into instant-hangup mode so any
	// subsequent idle hook press doesn't sit behind the flash window.
	d.serial.DisableFlashDetection()

	d.pendingOffer = ""
	d.pendingCaller = ""
	d.pendingICE = nil
	d.cleanupPreAnswer()
	peer := d.callPeer
	d.callPeer = ""
	d.isCaller = false
	d.callReturnOrigin.Store(false)
	d.isRestartingICE = false
	d.cancelRestartTimerLocked()
	d.cancelDisconnectDebounceLocked()

	sendSignal(d.sig, &sigclient.Message{Type: sigclient.TypeHangup, To: peer})

	// Snapshot the slow-to-close resources and null them out so a fresh
	// call setup (next pickup) doesn't have to wait for pion's DTLS / ICE
	// shutdown. Pion's pc.Close blocks for hundreds of ms to seconds, and
	// previously held the daemon's serial event loop the entire time,
	// gating dialtone on the next HOOK:OFF.
	pipeline := d.pipeline
	peerMgr := d.peerMgr
	mesh := d.mesh
	d.pipeline = nil
	d.peerMgr = nil
	d.mesh = nil
	// Drop any voicemail decode channel so the next call's pickup attempt
	// cannot accidentally bridge to a stale source. The decode goroutine
	// will exit on its own once the peer connection tears down.
	d.voicemailWebRTCCh = nil

	// Cancel link-health reporters before releasing the lock so the
	// background goroutine never calls GetStats on a closed peer.
	if d.reporterCancel != nil {
		d.reporterCancel()
		d.reporterCancel = nil
	}
	for phone, cancel := range d.meshReporterCancels {
		cancel()
		delete(d.meshReporterCancels, phone)
	}

	if peer != "" {
		d.mixer.RemoveWebRTCSource(peer)
	}

	d.mu.Unlock()
	slog.Info("call disconnected", "peer", peer, "sync_elapsed", time.Since(t0).Round(time.Microsecond))

	if finalizedVoicemail {
		// A caller-hung-up-mid-recording finalize just bumped the unheard
		// count. The controller's SendLED("OFF") already ran before this
		// HangupCall, against the pre-finalize count. Re-emit so the
		// indicator catches up. Targeted: non-voicemail hangups leave the
		// LED state untouched here so the off-hook treatment (remote
		// hangup, line lockout) keeps its existing visuals.
		d.evaluateLED()
		// Same finalize pushes a fresh count to the server so the
		// owner-side web UI badge updates without polling.
		d.publishVoicemailState()
	}

	if d.pendingAutoUpdate.CompareAndSwap(true, false) && d.autoUpdateEnabled.Load() && !devmode.SkipAutoUpdate(devmode.DefaultSkipAutoUpdatePath) {
		slog.Info("auto-update: call ended, running deferred update")
		if d.triggerAutoUpdate != nil {
			go d.triggerAutoUpdate()
		}
	}

	go func() {
		defer recoverGoroutine("hangup-teardown")
		teardownStart := time.Now()
		if pipeline != nil {
			t := time.Now()
			pipeline.Stop()
			slog.Info("hangup teardown: pipeline stopped", "elapsed", time.Since(t).Round(time.Millisecond))
		}
		if peerMgr != nil {
			t := time.Now()
			if err := peerMgr.Close(); err != nil {
				slog.Warn("hangup teardown: peerMgr close failed", "error", err)
			}
			slog.Info("hangup teardown: peerMgr closed", "elapsed", time.Since(t).Round(time.Millisecond))
		}
		if mesh != nil {
			t := time.Now()
			mesh.CloseAll()
			slog.Info("hangup teardown: mesh closed", "elapsed", time.Since(t).Round(time.Millisecond))
		}
		slog.Info("hangup teardown complete", "peer", peer, "async_elapsed", time.Since(teardownStart).Round(time.Millisecond))
	}()
}

// newPipeline constructs a fresh capture pipeline with per-line config
// applied (voice style, etc). The returned pipeline is not yet started.
// Must be called with d.mu held (reads d.cfg without acquiring the lock).
func (d *daemonCallbacks) newPipeline() *audio.Pipeline {
	cfg := audio.DefaultPipelineConfig()
	cfg.Character = d.cfg.VoiceStyleOrDefault() == config.VoiceStyleCopper
	return audio.NewPipeline(cfg)
}

// startEncodeLoop spawns the goroutine that reads captured PCM from the
// pipeline and sends it to the active peer and any conference mesh.
// Must be called with d.mu held (reads d.pipeline).
func (d *daemonCallbacks) startEncodeLoop() {
	go func() {
		defer recoverGoroutine("encode-loop")
		for frame := range d.pipeline.OutFrames() {
			d.mu.Lock()
			pm := d.peerMgr
			mesh := d.mesh
			d.mu.Unlock()
			if pm != nil {
				pm.SendPCMFrame(frame)
			}
			if mesh != nil {
				mesh.SendPCMFrameToAll(frame)
			}
		}
	}()
}

// remoteTrackHandler returns an OnRemoteTrack callback that decodes incoming
// RTP and feeds the mixer. It implements a three-phase startup:
//   - Phase 1: discard packets until the audio pipeline is running
//   - Phase 2: drain any buffered packets to catch up to real-time
//   - Phase 3: live decode and playback
func (d *daemonCallbacks) remoteTrackHandler(pm *owebrtc.PeerManager, webrtcCh chan []int16) func(*webrtc.TrackRemote) {
	return func(track *webrtc.TrackRemote) {
		go func() {
			defer recoverGoroutine("remote-track")
			var frameCount int

			slog.Info("remote track active, waiting for pipeline")
			var discarded int
			for {
				d.mu.Lock()
				pip := d.pipeline
				d.mu.Unlock()
				if pip != nil {
					slog.Info("pipeline ready", "discarded_packets", discarded)
					break
				}

				pkt, _, err := track.ReadRTP()
				if err != nil {
					slog.Info("remote track ended while waiting for pipeline", "discarded_packets", discarded)
					return
				}
				pm.Decode(pkt.Payload) //nolint:errcheck
				discarded++
			}

			drainStart := time.Now()
			var drained int
			var lastSeq uint16
			for {
				start := time.Now()
				pkt, _, err := track.ReadRTP()
				readTime := time.Since(start)
				if err != nil {
					slog.Info("remote track ended during drain")
					return
				}
				pm.Decode(pkt.Payload) //nolint:errcheck
				drained++
				lastSeq = pkt.SequenceNumber

				if readTime > 5*time.Millisecond {
					slog.Info("drain complete", "packets_skipped", drained-1, "duration", time.Since(drainStart).Round(time.Microsecond), "last_seq", lastSeq)
					break
				}
			}

			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					slog.Info("remote track ended", "frames", frameCount)
					return
				}
				recvTime := time.Now()
				pcm, err := pm.Decode(pkt.Payload)
				if err != nil {
					slog.Warn("decode error", "error", err, "pkt_bytes", len(pkt.Payload))
					continue
				}
				if pm.InboundMuted() {
					continue
				}
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				frameCount++
				select {
				case webrtcCh <- frame:
				default:
				}

				if frameCount <= 10 || frameCount%50 == 0 {
					slog.Info("FEED", "frame", frameCount, "seq", pkt.SequenceNumber, "recv", recvTime.Format("15:04:05.000000"))
				}
			}
		}()
	}
}

// prepareAnswer pre-creates the WebRTC PeerConnection during the ring phase.
// This moves expensive ECDSA cert generation, SDP processing, and ICE gathering
// off the critical path between handset pickup and first audio.
//
// Security invariant: no audio pipeline is started, no encode loop is spawned,
// and no ICE candidates are sent to the caller. The caller cannot establish ICE
// connectivity without our answer SDP (which contains our ufrag/pwd), so no
// media can flow until AnswerCall sends it after pickup.
//
// Must be called with d.mu held. On any failure, logs and returns without
// setting preAnswer state; AnswerCall falls back to the full creation path.
func (d *daemonCallbacks) prepareAnswer() {
	if d.preAnswer.peerMgr != nil {
		return // already prepared
	}
	caller := d.pendingCaller
	offerSDP := d.pendingOffer
	if offerSDP == "" {
		return // no offer to work with yet
	}

	t0 := time.Now()

	iceCfg := owebrtc.NewICEConfig(d.iceServers)
	pm, err := owebrtc.NewPeerManager(iceCfg)
	if err != nil {
		slog.Error("prepareAnswer: new peer manager failed", "error", err)
		return
	}

	pm.OnConnectionState = func(state webrtc.PeerConnectionState) {
		d.handleConnectionStateChange(pm, state)
	}

	// Collect local ICE candidates into the preAnswer slice (NOT sent to caller).
	pm.OnICECandidate = func(candidate string) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.preAnswer.peerMgr == pm {
			d.preAnswer.candidates = append(d.preAnswer.candidates, candidate)
		}
	}

	webrtcCh := d.mixer.AddWebRTCSource(caller)
	pm.OnRemoteTrack = d.remoteTrackHandler(pm, webrtcCh)

	answerSDP, err := pm.AcceptOffer(offerSDP)
	if err != nil {
		slog.Error("prepareAnswer: accept offer failed", "error", err)
		d.mixer.RemoveWebRTCSource(caller)
		_ = pm.Close()
		return
	}

	// Add any ICE candidates that arrived before we were ready.
	for _, candidate := range d.pendingICE {
		if err := pm.AddICECandidate(candidate); err != nil {
			slog.Warn("prepareAnswer: add queued ICE candidate failed", "error", err)
		}
	}
	d.pendingICE = nil

	d.preAnswer.peerMgr = pm
	d.preAnswer.answerSDP = answerSDP
	d.preAnswer.webrtcCh = webrtcCh
	d.preAnswer.candidates = nil // will be populated by OnICECandidate as they gather
	d.preAnswer.caller = caller

	slog.Info("prepareAnswer: ready", "caller", caller, "elapsed", time.Since(t0).Round(time.Millisecond))
}

// cleanupPreAnswer tears down any pre-created PeerConnection (e.g. caller
// hung up during ring). Must be called with d.mu held.
func (d *daemonCallbacks) cleanupPreAnswer() {
	if d.preAnswer.peerMgr == nil {
		return
	}
	slog.Info("cleanupPreAnswer: tearing down pre-created peer", "caller", d.preAnswer.caller)
	pm := d.preAnswer.peerMgr
	caller := d.preAnswer.caller
	d.preAnswer.peerMgr = nil
	d.preAnswer.answerSDP = ""
	d.preAnswer.webrtcCh = nil
	d.preAnswer.candidates = nil
	d.preAnswer.caller = ""
	if caller != "" {
		d.mixer.RemoveWebRTCSource(caller)
	}
	go func() {
		defer recoverGoroutine("cleanupPreAnswer")
		if err := pm.Close(); err != nil {
			slog.Warn("cleanupPreAnswer: close failed", "error", err)
		}
	}()
}

// triggerHangup dispatches a hangup to the controller from a fresh goroutine.
// Callers holding d.mu must use this to avoid deadlock: HandleSignal calls
// back into HangupCall, which also acquires d.mu.
//
// d.ctrl is assigned once in main() before the event loop starts and is
// never mutated, so the unsynchronized nil read here is safe.
func (d *daemonCallbacks) triggerHangup() {
	if d.ctrl == nil {
		return
	}
	go d.ctrl.HandleSignal("hangup", "")
}

// connAction is the decision output for a pion connection-state change.
type connAction int

const (
	actionNone          connAction = iota
	actionStartDebounce            // Disconnected: wait before reacting
	actionEnterRecovery            // Failed (or debounce expiry): drive ICE recovery
	actionClearRecovery            // Connected: cancel timers, recovery succeeded
)

// connStateAction maps a pion connection state plus current recovery flags to
// the action the daemon should take. Pure and table-tested; the side effects
// live in handleConnectionStateChange.
func connStateAction(state webrtc.PeerConnectionState, recovering, debouncePending bool) connAction {
	switch state {
	case webrtc.PeerConnectionStateConnected:
		return actionClearRecovery
	case webrtc.PeerConnectionStateDisconnected:
		if recovering || debouncePending {
			return actionNone
		}
		return actionStartDebounce
	case webrtc.PeerConnectionStateFailed:
		if recovering {
			return actionNone
		}
		return actionEnterRecovery
	default:
		return actionNone
	}
}

// reconnAction is the decision output for a signaling-WebSocket reconnect that
// happened while the phone was not idle.
type reconnAction int

const (
	reconnTeardown      reconnAction = iota // not a resumable 2-party call: tear down
	reconnResumeNoop                        // 2-party call, media survived: keep going
	reconnResumeRestart                     // 2-party call, media dropped: drive ICE recovery
)

// reconnectAction decides what to do with an active call when the signaling
// WebSocket reconnects. Only an established 2-party call (no mesh, has peer,
// controller in CONNECTED) is resumable; everything else (ringing, calling,
// voicemail, conference) tears down as before. Pure and table-tested.
func reconnectAction(ctrlState phone.State, hasMesh, hasPeer bool, connState webrtc.PeerConnectionState) reconnAction {
	if !hasPeer || hasMesh || ctrlState != phone.StateCONNECTED {
		return reconnTeardown
	}
	if connState == webrtc.PeerConnectionStateConnected {
		return reconnResumeNoop
	}
	return reconnResumeRestart
}

// tryResumeAfterReconnect handles an active call when the signaling WebSocket
// reconnects. It returns true if it took ownership of the call (resumed or
// kept it), false if the caller should fall back to full teardown. Only an
// established 2-party call resumes; conference/voicemail/ringing tear down.
func (d *daemonCallbacks) tryResumeAfterReconnect(ctrlState phone.State) bool {
	d.mu.Lock()
	pm := d.peerMgr
	hasMesh := d.mesh != nil
	// Read connection state under the same lock as pm so the pair is consistent.
	var connState webrtc.PeerConnectionState
	if pm != nil {
		connState = pm.ConnectionState()
	}
	d.mu.Unlock()

	switch reconnectAction(ctrlState, hasMesh, pm != nil, connState) {
	case reconnResumeNoop:
		slog.Info("signal: media survived reconnect, call continues", "state", ctrlState)
		return true
	case reconnResumeRestart:
		slog.Info("signal: media dropped during reconnect, driving ICE recovery", "state", ctrlState)
		d.enterICERecovery(pm, "ws-reconnect")
		return true
	default: // reconnTeardown
		return false
	}
}

// cancelDisconnectDebounceLocked stops a pending Disconnected debounce timer.
// Must be called with d.mu held.
func (d *daemonCallbacks) cancelDisconnectDebounceLocked() {
	if d.disconnectTimer != nil {
		d.disconnectTimer.Stop()
		d.disconnectTimer = nil
	}
}

// cancelRestartTimerLocked stops the ICE-restart deadline timer.
// Must be called with d.mu held.
func (d *daemonCallbacks) cancelRestartTimerLocked() {
	if d.restartTimer != nil {
		d.restartTimer.Stop()
		d.restartTimer = nil
	}
}

// enterICERecovery starts media recovery for the active 2-party call. The
// caller side rotates ICE credentials and sends a fresh restart offer; the
// callee side arms the wait timeout and waits for the caller's offer. Single
// offerer (caller) avoids offer/answer glare. Idempotent: if recovery is
// already in progress, it does nothing and lets the deadline timer govern.
// Must NOT be called with d.mu held.
func (d *daemonCallbacks) enterICERecovery(pm *owebrtc.PeerManager, reason string) {
	d.mu.Lock()
	if d.peerMgr != pm {
		d.mu.Unlock()
		return
	}
	if d.isRestartingICE {
		d.mu.Unlock()
		return
	}
	d.isRestartingICE = true
	d.cancelDisconnectDebounceLocked()
	d.startRestartTimeout()
	isCaller := d.isCaller
	peer := d.callPeer

	if !isCaller {
		d.mu.Unlock()
		slog.Warn("ice-recovery: waiting for restart offer from caller", "reason", reason)
		return
	}

	offer, err := d.peerMgr.CreateRestartOffer()
	if err != nil {
		slog.Error("ice-recovery: create offer failed", "error", err, "reason", reason)
		d.isRestartingICE = false
		d.cancelRestartTimerLocked()
		d.mu.Unlock()
		d.triggerHangup()
		return
	}
	sig := d.sig
	d.mu.Unlock()

	slog.Info("ice-recovery: sending restart offer", "peer", peer, "reason", reason, "bytes", len(offer))
	sendSignal(sig, &sigclient.Message{
		Type: sigclient.TypeICERestart,
		To:   peer,
		SDP:  offer,
	})
}

// handleConnectionStateChange is called (without d.mu held) from a pion
// goroutine when the WebRTC peer connection state changes. The action is
// decided by connStateAction: Connected clears recovery, Disconnected starts a
// debounce, Failed enters recovery, and a Failed event while already recovering
// is ignored so the restart deadline timer governs the hangup.
//
// pm is the PeerManager captured at callback-setup time. Because HangupCall
// detaches teardown into a goroutine, pion may fire a state change on a
// pre-hangup peer after the daemon has already moved on to a new call. Every
// branch that reads d.peerMgr therefore checks d.peerMgr == pm under d.mu and
// bails on mismatch, so a stale event can't drive recovery against the new
// call's peer.
func (d *daemonCallbacks) handleConnectionStateChange(pm *owebrtc.PeerManager, state webrtc.PeerConnectionState) {
	d.mu.Lock()
	if d.peerMgr != pm {
		d.mu.Unlock()
		return
	}
	action := connStateAction(state, d.isRestartingICE, d.disconnectTimer != nil)
	d.mu.Unlock()

	switch action {
	case actionClearRecovery:
		d.mu.Lock()
		if d.peerMgr != pm {
			d.mu.Unlock()
			return
		}
		wasRestarting := d.isRestartingICE
		d.isRestartingICE = false
		d.cancelDisconnectDebounceLocked()
		d.cancelRestartTimerLocked()
		// Spawn the link-health reporter once per call (not on recovery).
		if !d.linkHealthDisabled && d.reporterCancel == nil && d.peerMgr != nil {
			rctx, cancel := context.WithCancel(context.Background())
			d.reporterCancel = cancel
			sig := d.sig
			interval := d.linkHealthInterval
			d.mu.Unlock()
			send := func(s owebrtc.Sample) error {
				payload := &sigclient.LinkHealthPayload{
					TS:       s.TS.UnixMilli(),
					LossPct:  s.LossPct,
					JitterMs: s.JitterMs,
					RttMs:    s.RttMs,
					ConnType: s.ConnType,
					BytesIn:  s.BytesIn,
					BytesOut: s.BytesOut,
				}
				return sig.Send(&sigclient.Message{Type: sigclient.TypeLinkHealth, LinkHealth: payload})
			}
			reporter := owebrtc.NewReporter(pm, send, interval)
			go reporter.Run(rctx)
		} else {
			d.mu.Unlock()
		}
		if wasRestarting {
			slog.Info("webrtc: ICE recovery succeeded -- connection recovered")
		}

	case actionStartDebounce:
		d.mu.Lock()
		if d.peerMgr != pm || d.isRestartingICE || d.disconnectTimer != nil {
			d.mu.Unlock()
			return
		}
		d.disconnectTimer = time.AfterFunc(disconnectDebounce, func() {
			d.mu.Lock()
			d.disconnectTimer = nil
			stale := d.peerMgr != pm
			recovering := d.isRestartingICE
			d.mu.Unlock()
			if stale || recovering {
				return
			}
			if pm.ConnectionState() == webrtc.PeerConnectionStateConnected {
				slog.Info("webrtc: disconnected self-healed during debounce")
				return
			}
			slog.Warn("webrtc: still disconnected after debounce, entering ICE recovery")
			d.enterICERecovery(pm, "disconnected-debounce")
		})
		d.mu.Unlock()
		slog.Info("webrtc: disconnected, starting debounce", "debounce", disconnectDebounce)

	case actionEnterRecovery:
		d.mu.Lock()
		d.cancelDisconnectDebounceLocked()
		d.mu.Unlock()
		slog.Warn("webrtc: connection failed, entering ICE recovery")
		d.enterICERecovery(pm, "failed")

	case actionNone:
		if state == webrtc.PeerConnectionStateFailed {
			slog.Warn("webrtc: connection failed while recovering; deadline timer governs")
		}
	}
}

// startRestartTimeout sets a timer that hangs up the call if the ICE restart
// does not complete within iceRestartTimeout.  Must be called with d.mu held.
func (d *daemonCallbacks) startRestartTimeout() {
	d.restartTimer = time.AfterFunc(iceRestartTimeout, func() {
		d.mu.Lock()
		restarting := d.isRestartingICE
		d.mu.Unlock()
		if restarting {
			slog.Warn("webrtc: ICE restart timed out, hanging up")
			d.triggerHangup()
		}
	})
}
