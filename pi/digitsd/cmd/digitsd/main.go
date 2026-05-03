package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/assets"
	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	"github.com/justinlindh/digits/pi/digitsd/internal/bootcount"
	"github.com/justinlindh/digits/pi/digitsd/internal/config"
	"github.com/justinlindh/digits/pi/digitsd/internal/contacts"
	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	"github.com/justinlindh/digits/pi/digitsd/internal/updater"
	"github.com/justinlindh/digits/pi/digitsd/internal/version"
	"github.com/justinlindh/digits/pi/digitsd/internal/watchdog"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
	"github.com/justinlindh/digits/pi/digitsd/internal/wififallback"

	"github.com/pion/webrtc/v4"
)

// iceRestartTimeout is how long to wait for an ICE restart to succeed
// before giving up and hanging up the call.
const iceRestartTimeout = 15 * time.Second

// pairingRefreshInterval is how often an unpaired device reconnects to
// obtain a fresh pairing code. Must be shorter than the server-side
// CodeTTL (10 min) so the code is refreshed before it expires.
const pairingRefreshInterval = 9 * time.Minute

// pairingAnnouncementInterval is how long to wait between repeats of the
// spoken pairing-code sequence while the phone is unpaired and off-hook.
// Short enough that a user who missed the digits can re-hear them without
// hanging up; long enough to avoid talking over a listener who got it the
// first time.
const pairingAnnouncementInterval = 15 * time.Second

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
}

var (
	configPath  = flag.String("config", config.DefaultPath, "path to JSON config file")
	signaldURL  = flag.String("signald", "", "signald WebSocket URL (overrides config file)")
	numberFlag  = flag.String("number", "", "this phone's number, e.g. 3140001 (overrides config file)")
	serialDev   = flag.String("serial", "/dev/serial0", "serial port device")
	socketPath  = flag.String("socket", "/home/digits/digits/pi/uart.sock", "UART command socket path")
	toneDir     = flag.String("tones", "/home/digits/digits/pi/tones", "directory containing WAV tone files")
	alsaDevice  = flag.String("alsa-playback", "", "ALSA playback device (auto-detects Codec Zero if empty)")
	showVersion = flag.Bool("version", false, "print version and exit")
)

// daemonCallbacks implements phone.Callbacks and wires hardware + WebRTC.
type daemonCallbacks struct {
	serial           *phone.SerialPort
	sig              *sigclient.Client
	mixer            *audio.Mixer
	serviceCodes     *phone.ServiceCodeHandler
	ctrl             *phone.Controller
	mu               sync.Mutex
	peerMgr          *owebrtc.PeerManager
	mesh             *owebrtc.MeshManager // conference-only peer pool; 2-party calls use peerMgr
	pipeline         *audio.Pipeline
	number           string
	cfg              *config.Config
	pendingOffer     string
	pendingCaller    string
	pendingICE       []string // ICE candidates received before peerMgr is created
	iceServers       []owebrtc.ICEServerConfig // cached STUN/TURN servers from signald
	debugMode        bool     // read from DIGITS_DEBUG env at startup
	paired           atomic.Bool
	pairingCode          string    // current pairing code from server
	pairingCodeReceivedAt time.Time // when the current pairing code was received
	callPeer         string   // number of the remote party during an active call
	isCaller         bool     // true if we initiated the current call
	isRestartingICE  bool     // true while an ICE restart is in progress
	restartTimer     *time.Timer // timeout for ICE restart attempt

	// Link-health reporter: spawned when a call reaches Connected, canceled on teardown.
	// Protected by mu.
	reporterCancel      context.CancelFunc
	linkHealthDisabled  bool
	linkHealthInterval  time.Duration

	// meshReporterCancels holds one CancelFunc per mesh peer's link-health
	// reporter. Keyed by peer phone. Protected by mu, same as mesh.
	meshReporterCancels map[string]context.CancelFunc

	// Auto-update state. The atomic bools are safe for concurrent read from
	// the update goroutine. triggerAutoUpdate is set once at startup and
	// captures the run()-scoped variables needed by runAutoUpdate.
	autoUpdateEnabled atomic.Bool
	pendingAutoUpdate atomic.Bool
	triggerAutoUpdate func() // set in run(), calls runAutoUpdate with captured vars
}

// sendSignal sends a signaling message and logs failures.
func sendSignal(sig *sigclient.Client, msg *sigclient.Message) {
	if err := sig.Send(msg); err != nil {
		slog.Warn("signal send failed", "type", msg.Type, "to", msg.To, "error", err)
	}
}

// recoverGoroutine logs a panic with its stack trace so a single bad frame
// doesn't crash an audio/WebRTC goroutine silently.
func recoverGoroutine(name string) {
	if r := recover(); r != nil {
		slog.Error("goroutine panic recovered", "goroutine", name, "panic", r, "stack", string(debug.Stack()))
	}
}

// --- phone.Callbacks implementation ---

func (d *daemonCallbacks) SendTone(name string) {
	// Map controller tone names to WAV file names.
	switch strings.ToUpper(name) {
	case phone.ToneDial:
		d.mixer.PlayLoop("tone_dial")
		slog.Info("dialtone playing")
	case phone.ToneRingback:
		d.mixer.PlayLoop("tone_ringback")
	case phone.ToneBusy:
		d.mixer.PlayLoop("tone_busy")
	case phone.ToneReorder:
		d.mixer.PlayLoop("tone_reorder")
	case phone.ToneHowler:
		d.mixer.PlayLoop("tone_howler")
	case phone.ToneIntercept:
		d.mixer.PlayOnce("intercept")
	case phone.ToneDisconnected:
		d.mixer.PlayOnce("disconnected")
	case phone.ToneStop:
		d.mixer.StopTone()
	case phone.ToneStopAll:
		d.mixer.StopAll()
	default:
		slog.Warn("tone: unknown", "name", name)
	}
}

// OncePlaying reports whether a one-shot tone is still playing.
func (d *daemonCallbacks) OncePlaying() bool {
	return d.mixer.OncePlaying()
}

func (d *daemonCallbacks) SendRing(start bool) {
	d.serial.Ring(start)
}

func (d *daemonCallbacks) SendLED(mode string) {
	d.serial.LED(mode)
}

func (d *daemonCallbacks) NotifyCallConnected() {
	d.serial.CallConnected()
}

func (d *daemonCallbacks) SetFlashEnabled(enabled bool) {
	d.serial.FlashEnabled(enabled)
}

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
			// Live playback — decode and feed into mixer
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
				// Copy — Decode returns a slice of a reused internal buffer
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				frameCount++
				select {
				case webrtcCh <- frame:
				default:
					// Drop frame — mixer is behind
				}
			}
		}()
	}

	// Gate ICE candidates behind SDP send — candidates must not arrive before the offer.
	sdpSent := make(chan struct{})
	d.peerMgr.OnICECandidate = func(candidate string) {
		<-sdpSent
		sendSignal(d.sig, &sigclient.Message{
			Type:      sigclient.TypeICE,
			To:        targetNumber,
			Candidate: candidate,
		})
	}

	// Create offer (returns immediately — ICE trickles via OnICECandidate)
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

		// Encode and send captured audio to the 2-party peer and any conference mesh peers.
		// Each PeerManager owns its own encoder; per-peer outbound mute is handled in SendPCMFrame.
		go func() {
			defer recoverGoroutine("caller-encode-loop")
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

	// Stop tones — mixer continues writing silence (DAC keepalive)
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

	// Handle remote audio track — decode and feed into mixer.
	webrtcCh := d.mixer.AddWebRTCSource(caller)
	d.peerMgr.OnRemoteTrack = func(track *webrtc.TrackRemote) {
		go func() {
			defer recoverGoroutine("answer-remote-track")
			var frameCount int

			// Phase 1: Wait for pipeline (user picks up phone).
			// Read and discard RTP packets to prevent buffering.
			// Decode each to keep Opus decoder state in sync.
			slog.Info("remote track active, waiting for answer")
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
					slog.Info("remote track ended while waiting for answer", "discarded_packets", discarded)
					return
				}
				answerPM.Decode(pkt.Payload) //nolint:errcheck
				discarded++
			}

			// Phase 2: Drain stale packets until caught up to real-time.
			// Decode each to maintain Opus state, but don't play.
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
				answerPM.Decode(pkt.Payload) //nolint:errcheck
				drained++
				lastSeq = pkt.SequenceNumber

				if readTime > 5*time.Millisecond {
					slog.Info("drain complete", "packets_skipped", drained-1, "duration", time.Since(drainStart).Round(time.Microsecond), "last_seq", lastSeq)
					break
				}
			}

			// Phase 3: Live playback loop — feed decoded PCM into mixer.
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					slog.Info("remote track ended", "frames", frameCount)
					return
				}
				recvTime := time.Now()
				pcm, err := answerPM.Decode(pkt.Payload)
				if err != nil {
					slog.Warn("decode error", "error", err, "pkt_bytes", len(pkt.Payload))
					continue
				}
				if answerPM.InboundMuted() {
					// Silent hold: drop decoded audio rather than feeding the mixer.
					continue
				}
				// Copy — Decode returns a slice of a reused internal buffer
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				frameCount++
				select {
				case webrtcCh <- frame:
				default:
					// Drop frame — mixer is behind
				}

				if frameCount <= 10 || frameCount%50 == 0 {
					slog.Info("FEED", "frame", frameCount, "seq", pkt.SequenceNumber, "recv", recvTime.Format("15:04:05.000000"))
				}
			}
		}()
	}

	// Gate ICE candidates behind answer SDP send.
	sdpSent := make(chan struct{})
	d.peerMgr.OnICECandidate = func(candidate string) {
		<-sdpSent
		sendSignal(d.sig, &sigclient.Message{
			Type:      sigclient.TypeICE,
			To:        caller,
			Candidate: candidate,
		})
	}

	// Accept the offer and generate answer (returns immediately — ICE trickles via OnICECandidate)
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

	// Start audio pipeline (capture only — playback goes through mixer).
	// Skip if one is already running; see matching comment in InitiateCall.
	if d.pipeline == nil {
		d.pipeline = d.newPipeline()
		if err := d.pipeline.Start(); err != nil {
			slog.Error("audio pipeline (answer) start failed", "error", err)
			return
		}

		// Encode and send captured audio to the 2-party peer and any conference mesh peers.
		// Each PeerManager owns its own encoder; per-peer outbound mute is handled in SendPCMFrame.
		go func() {
			defer recoverGoroutine("answer-encode-loop")
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

	slog.Info("answered call", "caller", caller)
}

func (d *daemonCallbacks) MutePeer(phone string, muted bool) {
	d.mu.Lock()
	mesh := d.mesh
	pm := d.peerMgr
	callPeer := d.callPeer
	d.mu.Unlock()

	// First try the conference mesh.
	if mesh != nil {
		if meshPM := mesh.GetPeer(phone); meshPM != nil {
			meshPM.SetOutboundMuted(muted)
			meshPM.SetInboundMuted(muted)
			slog.Info("mute peer (mesh)", "phone", phone, "muted", muted)
			return
		}
	}
	// Fall back to the 2-party peerMgr if phone matches the current 2-party peer.
	if pm != nil && callPeer == phone {
		pm.SetOutboundMuted(muted)
		pm.SetInboundMuted(muted)
		slog.Info("mute peer (2-party)", "phone", phone, "muted", muted)
		return
	}
	slog.Warn("MutePeer: no peer found", "phone", phone, "muted", muted)
}

func (d *daemonCallbacks) MigrateToMesh(phone string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.peerMgr == nil || d.callPeer != phone {
		slog.Warn("MigrateToMesh: no matching 2-party peer", "phone", phone, "callPeer", d.callPeer)
		return
	}

	// Ensure the mesh exists.
	if d.mesh == nil {
		d.mesh = owebrtc.NewMeshManager(owebrtc.NewICEConfig(d.iceServers))
	}

	// Transfer ownership: the existing PeerManager moves into the mesh under
	// the peer's phone key. d.peerMgr is cleared so future 2-party calls
	// create a fresh PeerConnection. d.callPeer is intentionally kept so that
	// HOOK:FLASH dispatch and other paths can still identify the B party after
	// migration (the peer is now in the mesh, but its identity doesn't change).
	d.mesh.Adopt(phone, d.peerMgr)
	d.peerMgr = nil
}

// currentPeer returns the phone number of the 2-party remote peer that
// HOOK:FLASH dispatch should treat as the "active" party to hold. The answer
// depends on the controller's state rather than on daemon internals -- this
// dispatches explicitly so future state additions have to declare their own
// peer policy instead of silently inheriting the "len(mesh)==1" heuristic.
//
// - CONNECTED: prefer d.callPeer. It's set by InitiateCall/AnswerCall; if a
//   previous ADD was aborted and its TearDownPeer cleared d.callPeer while
//   leaving the original held party B in the mesh, fall back to the single
//   mesh peer.
// - ADD_*: the controller has already captured the held party in c.heldPeer,
//   so the value returned here is not consulted by onHookFlash. Return
//   d.callPeer as a best-effort identity.
// - All other states: no meaningful "current peer" -- return empty.
//
// Must NOT be called with d.mu held. ctrl.State() acquires c.mu, so the
// lock-order invariant is preserved by snapshotting the state first.
func (d *daemonCallbacks) currentPeer() string {
	s := d.ctrl.State()

	d.mu.Lock()
	defer d.mu.Unlock()

	switch s {
	case phone.StateCONNECTED:
		if d.callPeer != "" {
			return d.callPeer
		}
		if d.mesh != nil {
			peers := d.mesh.ActivePeers()
			if len(peers) == 1 {
				return peers[0]
			}
		}
		return ""
	case phone.StateADD_DIALTONE, phone.StateADD_DIALING,
		phone.StateADD_CALLING, phone.StateADD_PRIVATE,
		phone.StateADD_INTERCEPT, phone.StateCONFERENCE_MERGED:
		return d.callPeer
	default:
		return ""
	}
}

func (d *daemonCallbacks) TearDownPeer(phone string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cancel, ok := d.meshReporterCancels[phone]; ok {
		cancel()
		delete(d.meshReporterCancels, phone)
	}
	if d.mesh != nil {
		d.mesh.RemovePeer(phone)
	}
	// If the phone being torn down is the current 2-party peer (e.g. an
	// ADD_CALLING target that the server rejected before it could migrate
	// into the mesh), close its PeerManager too so we don't leak a dead PC
	// across retries.
	if d.peerMgr != nil && d.callPeer == phone {
		if err := d.peerMgr.Close(); err != nil {
			slog.Warn("TearDownPeer: peerMgr close failed", "phone", phone, "error", err)
		}
		d.peerMgr = nil
		d.callPeer = ""
	}
	d.mixer.RemoveWebRTCSource(phone)
}

func (d *daemonCallbacks) RequestConferenceMerge(held, active string) {
	d.mu.Lock()
	sig := d.sig
	d.mu.Unlock()
	slog.Info("conference: sending ConferenceMerge to server", "held", held, "active", active)
	sendSignal(sig, &sigclient.Message{
		Type:       sigclient.TypeConferenceMerge,
		HeldPeer:   held,
		ActivePeer: active,
	})
}

func (d *daemonCallbacks) AddMeshPeer(phone string, initiator bool) {
	if !initiator {
		// Responder path: don't pre-create the mesh peer. setupMeshResponder
		// will create it when the initiator's SDP offer arrives. Pre-creating
		// here would leave a PC in signaling state 'stable', which causes the
		// TypeSDP dispatch at line ~1855 to mistakenly route the incoming
		// offer as an answer (SetRemote(answer) from stable is an invalid
		// pion transition).
		slog.Info("conference: responder waiting for initiator SDP", "phone", phone)
		return
	}

	d.mu.Lock()
	if d.mesh == nil {
		iceCfg := owebrtc.NewICEConfig(d.iceServers)
		d.mesh = owebrtc.NewMeshManager(iceCfg)
	}
	mesh := d.mesh
	confID := d.ctrl.ConferenceID()
	sig := d.sig
	d.mu.Unlock()

	slog.Info("conference: adding mesh peer", "phone", phone, "initiator", initiator, "conf_id", confID)

	pm, err := mesh.AddPeer(phone)
	if err != nil {
		slog.Error("conference: add mesh peer failed", "phone", phone, "err", err)
		return
	}

	// Wire remote audio track into the mixer BEFORE any async signaling work.
	// Pion can fire OnTrack during negotiation; setting it after CreateOffer
	// would race against the remote track arriving.
	webrtcCh := d.mixer.AddWebRTCSource(phone)
	pm.OnRemoteTrack = func(track *webrtc.TrackRemote) {
		slog.Info("conference: remote track attached (initiator)", "phone", phone)
		go func() {
			defer recoverGoroutine("conf-remote-track-" + phone)
			gotFirst := false
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					slog.Info("conference: remote track ended", "phone", phone)
					return
				}
				if !gotFirst {
					slog.Info("conference: first RTP packet received", "phone", phone)
					gotFirst = true
				}
				// pm owns its own decoder — safe to call concurrently with other peers.
				pcm, err := pm.Decode(pkt.Payload)
				if err != nil {
					continue
				}
				if pm.InboundMuted() {
					// Silent hold: drop decoded audio rather than feeding the mixer.
					continue
				}
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				select {
				case webrtcCh <- frame:
				default:
					// drop frame if consumer is behind
				}
			}
		}()
	}

	// Wire ICE candidate forwarding. Gate candidates behind SDP send so the
	// remote side has a local description before processing candidates.
	sdpSent := make(chan struct{})
	pm.OnICECandidate = func(candidate string) {
		<-sdpSent
		sendSignal(sig, &sigclient.Message{
			Type:      sigclient.TypeICE,
			To:        phone,
			ConfID:    confID,
			Candidate: candidate,
		})
	}

	pm.OnConnectionState = d.meshReporterOnConnected(pm, phone)

	if initiator {
		// Initiator creates and sends the SDP offer to the peer.
		offer, err := pm.CreateOffer()
		if err != nil {
			slog.Error("conference: create offer failed", "phone", phone, "err", err)
			close(sdpSent)
			return
		}
		sendSignal(sig, &sigclient.Message{
			Type:   sigclient.TypeSDP,
			To:     phone,
			ConfID: confID,
			SDP:    offer,
		})
		close(sdpSent)
		slog.Info("conference: sent SDP offer to peer", "phone", phone, "conf_id", confID)
	} else {
		// Responder waits for the initiator's offer (handled in TypeSDP dispatch).
		// Ungate ICE candidates immediately since we don't send an offer here.
		close(sdpSent)
	}
}

func (d *daemonCallbacks) RemoveMeshPeer(phone string) {
	slog.Info("conference: removing mesh peer", "phone", phone)
	d.mu.Lock()
	mesh := d.mesh
	if cancel, ok := d.meshReporterCancels[phone]; ok {
		cancel()
		delete(d.meshReporterCancels, phone)
	}
	d.mu.Unlock()
	if mesh != nil {
		mesh.RemovePeer(phone)
	}
	d.mixer.RemoveWebRTCSource(phone)
}

func (d *daemonCallbacks) TearDownAllMeshPeers() {
	d.mu.Lock()
	mesh := d.mesh
	var peers []string
	if mesh != nil {
		peers = mesh.ActivePeers()
	}
	for phone, cancel := range d.meshReporterCancels {
		cancel()
		delete(d.meshReporterCancels, phone)
	}
	d.mu.Unlock()
	slog.Info("conference: tearing down all mesh peers", "count", len(peers), "peers", peers)
	if mesh == nil {
		return
	}
	for _, p := range peers {
		d.mixer.RemoveWebRTCSource(p)
	}
	mesh.CloseAll()
}

// setupMeshResponder creates a mesh peer for an incoming conference SDP offer,
// wires the remote track and ICE candidate handlers, and accepts the offer.
// Returns the answer SDP. Must NOT be called with d.mu held.
func (d *daemonCallbacks) setupMeshResponder(peer, offerSDP, confID string) (string, error) {
	d.mu.Lock()
	if d.mesh == nil {
		iceCfg := owebrtc.NewICEConfig(d.iceServers)
		d.mesh = owebrtc.NewMeshManager(iceCfg)
	}
	mesh := d.mesh
	d.mu.Unlock()

	pm, err := mesh.AddPeer(peer)
	if err != nil {
		return "", fmt.Errorf("mesh AddPeer: %w", err)
	}

	// Wire remote audio track BEFORE AcceptOffer so pion cannot miss it.
	webrtcCh := d.mixer.AddWebRTCSource(peer)
	pm.OnRemoteTrack = func(track *webrtc.TrackRemote) {
		slog.Info("conference: remote track attached (responder)", "phone", peer)
		go func() {
			defer recoverGoroutine("conf-remote-track-" + peer)
			gotFirst := false
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					slog.Info("conference: remote track ended (responder)", "phone", peer)
					return
				}
				if !gotFirst {
					slog.Info("conference: first RTP packet received (responder)", "phone", peer)
					gotFirst = true
				}
				// pm owns its own decoder — safe to call concurrently with other peers.
				pcm, err := pm.Decode(pkt.Payload)
				if err != nil {
					continue
				}
				if pm.InboundMuted() {
					// Silent hold: drop decoded audio rather than feeding the mixer.
					continue
				}
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				select {
				case webrtcCh <- frame:
				default:
				}
			}
		}()
	}

	// Gate ICE candidates behind answer SDP send.
	sdpSent := make(chan struct{})
	pm.OnICECandidate = func(candidate string) {
		<-sdpSent
		d.mu.Lock()
		sig := d.sig
		d.mu.Unlock()
		sendSignal(sig, &sigclient.Message{
			Type:      sigclient.TypeICE,
			To:        peer,
			ConfID:    confID,
			Candidate: candidate,
		})
	}

	pm.OnConnectionState = d.meshReporterOnConnected(pm, peer)

	// Use confID from the offer rather than ctrl.ConferenceID() so the answer
	// is correctly routed even if ConferenceMember has not yet arrived.
	answerSDP, err := pm.AcceptOffer(offerSDP)
	if err != nil {
		close(sdpSent)
		return "", fmt.Errorf("AcceptOffer: %w", err)
	}
	close(sdpSent)
	return answerSDP, nil
}

func (d *daemonCallbacks) HangupCall() {
	t0 := time.Now()
	d.mu.Lock()

	// Call is tearing down. Drop the Pico into instant-hangup mode so any
	// subsequent idle hook press doesn't sit behind the flash window.
	d.serial.FlashEnabled(false)

	d.pendingOffer = ""
	d.pendingCaller = ""
	d.pendingICE = nil
	peer := d.callPeer
	d.callPeer = ""
	d.isCaller = false
	d.isRestartingICE = false
	if d.restartTimer != nil {
		d.restartTimer.Stop()
		d.restartTimer = nil
	}

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

	if d.pendingAutoUpdate.CompareAndSwap(true, false) && d.autoUpdateEnabled.Load() {
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

// applyVoiceStyleLive forwards a voice style change to the active pipeline
// if one is running. Safe to call with no active pipeline: it becomes a
// no-op, and the config cache save in setVoiceStyleConfig still persists
// the style for the next call to pick up at construction time.
func (d *daemonCallbacks) applyVoiceStyleLive(style string) {
	d.mu.Lock()
	pip := d.pipeline
	d.mu.Unlock()
	if pip == nil {
		return
	}
	pip.SetVoiceStyle(style)
}

// setVoiceStyleConfig writes the new voice style to the local config cache
// under d.mu (serializing with the call-setup paths that read
// d.cfg.VoiceStyle) and then persists it to disk outside the lock so the
// atomic tmp+rename write doesn't block call setup.
func (d *daemonCallbacks) setVoiceStyleConfig(style string) error {
	d.mu.Lock()
	d.cfg.VoiceStyle = style
	d.mu.Unlock()
	return d.cfg.Save()
}

// applySilentModeLive forwards a silent-mode change to the phone controller.
// Safe before the controller is constructed: becomes a no-op, and
// setSilentModeConfig still persists the flag for the next startup.
func (d *daemonCallbacks) applySilentModeLive(silent bool) {
	d.mu.Lock()
	ctrl := d.ctrl
	d.mu.Unlock()
	if ctrl == nil {
		return
	}
	ctrl.SetSilentMode(silent)
}

// setSilentModeConfig writes the new flag to the local config cache under
// d.mu (serializing with call-setup paths that read d.cfg) and then persists
// it to disk outside the lock.
func (d *daemonCallbacks) setSilentModeConfig(silent bool) error {
	d.mu.Lock()
	d.cfg.SilentMode = silent
	d.mu.Unlock()
	return d.cfg.Save()
}

// setAutoUpdateConfig writes the new flag to the local config cache under
// d.mu and persists it to disk outside the lock.
func (d *daemonCallbacks) setAutoUpdateConfig(enabled bool) error {
	d.mu.Lock()
	d.cfg.AutoUpdate = enabled
	d.mu.Unlock()
	return d.cfg.Save()
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

// handleConnectionStateChange is called (without d.mu held) from a pion
// goroutine when the WebRTC peer connection state changes.  On transient
// failures the original caller attempts a single ICE restart before giving up.
//
// pm is the PeerManager captured at callback-setup time. Because HangupCall
// detaches teardown into a goroutine, pion may fire a state change on a
// pre-hangup peer after the daemon has already moved on to a new call. Every
// branch that reads d.peerMgr / d.isCaller therefore checks d.peerMgr == pm
// under d.mu and bails on mismatch, so a stale Failed event can't trigger an
// ICE restart against the new call's peer.
func (d *daemonCallbacks) handleConnectionStateChange(pm *owebrtc.PeerManager, state webrtc.PeerConnectionState) {
	switch state {
	case webrtc.PeerConnectionStateConnected:
		d.mu.Lock()
		if d.peerMgr != pm {
			d.mu.Unlock()
			return
		}
		wasRestarting := d.isRestartingICE
		d.isRestartingICE = false
		if d.restartTimer != nil {
			d.restartTimer.Stop()
			d.restartTimer = nil
		}
		// Spawn the link-health reporter once per call (not on ICE restart recovery).
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
			slog.Info("webrtc: ICE restart succeeded -- connection recovered")
		}

	case webrtc.PeerConnectionStateFailed:
		d.mu.Lock()
		if d.peerMgr != pm {
			d.mu.Unlock()
			return
		}
		alreadyRestarting := d.isRestartingICE
		isCaller := d.isCaller
		d.mu.Unlock()

		if alreadyRestarting {
			slog.Warn("webrtc: ICE restart failed, hanging up")
			d.triggerHangup()
			return
		}

		if isCaller {
			slog.Warn("webrtc: connection failed, attempting ICE restart")
			d.attemptICERestart()
		} else {
			slog.Warn("webrtc: connection failed, waiting for ICE restart from caller")
			d.mu.Lock()
			d.isRestartingICE = true
			d.startRestartTimeout()
			d.mu.Unlock()
		}
	}
}

// attemptICERestart creates a new SDP offer with rotated ICE credentials
// and sends it to the remote peer.  Must NOT be called with d.mu held.
func (d *daemonCallbacks) attemptICERestart() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.peerMgr == nil {
		slog.Warn("ice-restart: no peer manager, hanging up")
		d.triggerHangup()
		return
	}

	d.isRestartingICE = true

	offer, err := d.peerMgr.CreateRestartOffer()
	if err != nil {
		slog.Error("ice-restart: create offer failed", "error", err)
		d.isRestartingICE = false
		d.triggerHangup()
		return
	}

	peer := d.callPeer
	d.startRestartTimeout()

	slog.Info("ice-restart: sending restart offer", "peer", peer, "bytes", len(offer))
	sendSignal(d.sig, &sigclient.Message{
		Type: sigclient.TypeICERestart,
		To:   peer,
		SDP:  offer,
	})
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

// --- phone.SocketHandler implementation ---

func (d *daemonCallbacks) HandleSocketCommand(cmd string) string {
	// Test event injection (for automated testing without physical hardware).
	// Only available when DIGITS_DEBUG=1 is set in the environment.
	if strings.HasPrefix(cmd, "TEST:EVENT:") {
		if !d.debugMode {
			return "ERROR: TEST:EVENT not available in production"
		}
		event := cmd[11:]
		slog.Info("test: injecting event", "event", event)
		if d.ctrl != nil {
			// HOOK:FLASH has a dedicated dispatch that needs the active
			// 2-party peer, mirroring the serial reader path in main.go.
			if event == "HOOK:FLASH" {
				d.ctrl.HandleHookFlash(d.currentPeer())
			} else {
				d.ctrl.HandleEvent(event)
			}
		}
		return "OK"
	}

	// Intercept tone commands (handled locally)
	if strings.HasPrefix(cmd, "TONE:") {
		tone := cmd[5:]
		switch tone {
		case phone.ToneStop:
			d.mixer.StopTone()
		case phone.ToneDial:
			d.mixer.PlayLoop("tone_dial")
		case phone.ToneRingback:
			d.mixer.PlayLoop("tone_ringback")
		case phone.ToneBusy:
			d.mixer.PlayLoop("tone_busy")
		}
		return "OK"
	}

	// Fire-and-forget commands
	if strings.HasPrefix(cmd, "LED:") || cmd == "RING:STOP" || strings.HasPrefix(cmd, "HOOK:") {
		d.serial.SendFire(cmd)
		return "OK"
	}

	// All other commands: send and wait for response
	resp, err := d.serial.SendCommand(cmd, 3*time.Second)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	return resp
}

// AddSerialMonitor registers a tap on the serial port. Used by the socket
// server's MONITOR upgrade so an interactive UART terminal can mirror live
// TX/RX without taking the port away from digitsd.
func (d *daemonCallbacks) AddSerialMonitor(ch chan string) func() {
	return d.serial.AddMonitor(ch)
}

// SendRaw forwards a command line to the Pico without waiting for a response.
// Used by the MONITOR upgrade to inject commands typed into the interactive
// terminal; any response comes back through the monitor tap as a normal RX.
func (d *daemonCallbacks) SendRaw(cmd string) {
	d.serial.SendFire(cmd)
}

// Default paths for SWD flash infrastructure on the Pi.
const (
	defaultFirmwarePath        = "/data/digits/firmware.elf"
	defaultFirmwareVersionPath = "/data/digits/firmware.elf.version"
	defaultFlashScript         = "/usr/local/bin/flash-pico.sh"
	defaultSWDConfig           = "/usr/local/share/digits/swd/digits-swd.cfg"
	defaultOpenOCD             = "/usr/bin/openocd"
	pcbRevPath                 = "/etc/digits-pcb-rev"
	mixerStatePath             = "/data/digits_mixer.state"
)

// firmwareNeedsReflash reports a confident version mismatch; either side
// empty returns false (treated as "unknown, skip").
func firmwareNeedsReflash(picoVersion, bundledVersion string) bool {
	if picoVersion == "" || bundledVersion == "" {
		return false
	}
	return picoVersion != bundledVersion
}

func readBundledFirmwareVersion() string {
	data, err := os.ReadFile(defaultFirmwareVersionPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// reflashPico delegates to flash-pico.sh (RESCUE retry, FLASHSIZE override,
// PCB-rev marker write at 0x101FF000) and re-establishes the serial port.
// Aborts on serial-reopen failure: nothing else digitsd does works without
// UART. Returns the reopened port and whether the post-flash PING passed.
func reflashPico(sp *phone.SerialPort, serialDev string, serialLogger *slog.Logger, reason string) (*phone.SerialPort, bool) {
	if _, err := os.Stat(defaultFirmwarePath); err != nil {
		slog.Info("reflash: no firmware at path, skipping", "path", defaultFirmwarePath, "reason", reason)
		return sp, false
	}
	slog.Info("reflash: starting", "path", defaultFirmwarePath, "reason", reason)
	if err := sp.Close(); err != nil {
		slog.Warn("reflash: close serial failed", "error", err)
	}
	// SKIP_SERVICE_CONTROL=1 stops the script from systemctl-stopping us
	// (we ARE digitsd) and from doing its own post-flash PING (we hold
	// the serial port; we'll PING ourselves below).
	cmd := exec.Command("setsid", "bash", defaultFlashScript, defaultFirmwarePath)
	cmd.Env = append(os.Environ(), "SKIP_SERVICE_CONTROL=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		slog.Error("reflash: flash script failed", "error", err, "reason", reason)
	} else {
		slog.Info("reflash: flash script succeeded", "reason", reason)
	}
	time.Sleep(2 * time.Second)
	newSp, err := phone.OpenSerial(serialDev, 115200, serialLogger)
	if err != nil {
		log.Fatalf("reflash: serial re-open failed: %v", err)
	}
	// A virgin Pico needs to cold-boot the freshly written firmware before
	// it can answer PING; flash-pico.sh's own sleeps don't always cover
	// it. Poll Ping() until deadline so the first POST after reflash
	// reads PASS instead of "Phone will not function." Ping itself has a
	// 2 s timeout, so a 10 s ceiling allows ~4 attempts.
	if pingErr := pollPing(newSp.Ping, 10*time.Second, 500*time.Millisecond); pingErr != nil {
		slog.Warn("reflash: PING failed after flash", "error", pingErr, "reason", reason)
		return newSp, false
	}
	slog.Info("reflash: PING PASS", "reason", reason)
	return newSp, true
}

// pollPing retries ping() until it succeeds or deadline elapses, returning
// the last error on timeout. interval is the gap between attempts; the
// caller's ping function carries its own timeout. Decoupled from
// *phone.SerialPort so it can be unit-tested with a fake.
func pollPing(ping func() error, deadline, interval time.Duration) error {
	end := time.Now().Add(deadline)
	var lastErr error
	for {
		if err := ping(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(end) {
			return lastErr
		}
		time.Sleep(interval)
	}
}

// readPCBRev returns the fab revision of the carrier board ("1", "2", ...)
// stamped at image-build time. Defaults to "1" if the marker is missing or
// unreadable so that older images (and dev hosts) keep their original V1
// behavior.
func readPCBRev() string {
	data, err := os.ReadFile(pcbRevPath)
	if err != nil {
		return "1"
	}
	rev := strings.TrimSpace(string(data))
	if rev == "" {
		return "1"
	}
	return rev
}

// statusFunc is a callback to report update progress back to the server.
type statusFunc func(status, detail string)

func triggerFactoryReset(sig *sigclient.Client, deviceID string) {
	if err := bootcount.SetThreshold(bootcount.DefaultPath, 3); err != nil {
		slog.Error("factory reset: failed to set boot counter", "err", err)
		return
	}
	// AutoFactoryResetFlag tells the recovery server to skip its menu and
	// run Factory Reset directly. Without it, the user lands in a
	// "X failed boot attempts detected" web UI even though the device
	// didn't actually fail to boot. Best-effort: if the write fails the
	// recovery menu still appears, just with a misleading header.
	if err := os.WriteFile(bootcount.AutoFactoryResetFlag, []byte("1\n"), 0644); err != nil {
		slog.Warn("factory reset: write auto-reset flag failed", "path", bootcount.AutoFactoryResetFlag, "err", err)
	}
	// Tell the server to invalidate its copy of paired_at + device_token
	// over the still-authenticated WS, BEFORE we reboot. Factory reset
	// wipes /data (and with it the local DeviceToken), so the device comes
	// back unpaired. Without this, the server still has paired_at set and
	// rejects the post-reset register-without-token as "device_token
	// required", looping every 3 seconds. Brief sleep so the message
	// lands (writes are async on the WS Send channel).
	if sig != nil && deviceID != "" {
		sendSignal(sig, &sigclient.Message{Type: sigclient.TypeRepair, HardwareID: deviceID})
		time.Sleep(500 * time.Millisecond)
	}
	slog.Info("factory reset: boot counter set to 3, auto-reset flag written, rebooting")
	_ = exec.Command("sudo", "reboot").Run()
}

// updateInProgress guards against concurrent firmware update runs.
// The startup update goroutine and server-triggered updates both check this
// to avoid racing (e.g. double-flashing the Pico).
var updateInProgress atomic.Bool

// runAutoUpdate checks whether the device is idle (no active call) and, if so,
// delegates to runTargetedUpdate with empty targets (install whatever is
// latest). When a call is in progress the update is deferred: pendingAutoUpdate
// is set so HangupCall can retry once the call ends.
func runAutoUpdate(d *daemonCallbacks, serverURL, piVersion, fwVersion string, flashCapable bool, afterFirmwareUpdated func()) {
	d.mu.Lock()
	inCall := d.callPeer != ""
	d.mu.Unlock()

	if inCall {
		slog.Info("auto-update: call in progress, deferring until idle")
		d.pendingAutoUpdate.Store(true)
		return
	}

	slog.Info("auto-update: device is idle, checking for updates")
	runTargetedUpdate(serverURL, piVersion, fwVersion, "", "", flashCapable, nil, afterFirmwareUpdated)
}

func runTargetedUpdate(serverURL, piVersion, fwVersion, targetPi, targetFW string, flashCapable bool, reportStatus statusFunc, afterFirmwareUpdated func()) {
	if !updateInProgress.CompareAndSwap(false, true) {
		slog.Info("updater: skipping -- another update is already in progress")
		return
	}
	defer updateInProgress.Store(false)
	// When a specific component is targeted, don't auto-upgrade the other.
	// A targeted trigger with only target_pi set should not also install firmware.
	targeted := targetPi != "" || targetFW != ""

	if reportStatus == nil {
		reportStatus = func(string, string) {} // no-op
	}

	baseURL := strings.TrimSuffix(serverURL, "/ws")
	baseURL = strings.Replace(baseURL, "wss://", "https://", 1)
	baseURL = strings.Replace(baseURL, "ws://", "http://", 1)

	up := updater.New(updater.Config{
		ServerBaseURL:    baseURL,
		CurrentPiVersion: piVersion,
		CurrentFWVersion: fwVersion,
	})

	slog.Info("updater: checking for updates", "target_pi", targetPi, "target_fw", targetFW)
	result, err := up.CheckVersion(targetPi, targetFW)
	if err != nil {
		slog.Error("updater: check failed", "error", err)
		reportStatus("failed", fmt.Sprintf("Check failed: %v", err))
		return
	}
	// When a specific component is targeted, suppress the other so we don't
	// accidentally install firmware when the user only clicked "Install Pi Software".
	if targeted {
		if targetPi == "" {
			result.PiAvailable = false
		}
		if targetFW == "" {
			result.FWAvailable = false
		}
	}

	if !result.PiAvailable && !result.FWAvailable {
		slog.Info("updater: already up to date")
		reportStatus("up_to_date", "Already running latest version")
		return
	}
	slog.Info("updater: update available", "pi_available", result.PiAvailable, "pi_version", result.PiVersion, "fw_available", result.FWAvailable, "fw_version", result.FWVersion)

	fwSkipped := false
	if result.FWAvailable {
		if !flashCapable {
			slog.Info("updater: firmware update available but SWD flash not supported on this device, skipping")
			fwSkipped = true
		} else {
			reportStatus("downloading", "Downloading firmware "+result.FWVersion)
			path, err := up.Download(result.FWURL, "firmware.elf", result.FWSHA256)
			if err != nil {
				slog.Error("updater: firmware download failed", "error", err)
				reportStatus("failed", fmt.Sprintf("Firmware download failed: %v", err))
				return
			}
			reportStatus("applying", "Flashing firmware "+result.FWVersion)
			if err := up.ApplyFirmwareUpdate(path); err != nil {
				slog.Error("updater: firmware apply failed", "error", err)
				reportStatus("failed", fmt.Sprintf("Firmware flash failed: %v", err))
				return
			}
			if afterFirmwareUpdated != nil {
				afterFirmwareUpdated()
			}
		}
	}
	if result.PiAvailable {
		reportStatus("downloading", "Downloading digitsd "+result.PiVersion)
		path, err := up.Download(result.PiURL, "digitsd-aarch64", result.PiSHA256)
		if err != nil {
			slog.Error("updater: pi download failed", "error", err)
			reportStatus("failed", fmt.Sprintf("Download failed: %v", err))
			return
		}
		reportStatus("rebooting", "Installing digitsd "+result.PiVersion+" -- restarting...")
		if err := up.ApplyPiUpdate(path, result.PiVersion); err != nil {
			slog.Error("updater: pi update failed", "error", err)
			reportStatus("failed", fmt.Sprintf("Install failed: %v", err))
			return
		}
	}

	// Report final status when pi binary wasn't replaced (no restart)
	if !result.PiAvailable {
		if result.FWAvailable && !fwSkipped {
			reportStatus("success", "Firmware updated to "+result.FWVersion)
		} else if fwSkipped {
			reportStatus("up_to_date", "Pi is current; firmware update requires SWD wiring")
		}
	}
}

// resetPicoHardware clears any residual ring or LED state on the Pico in case
// the Pi rebooted mid-call. Safe no-op on clean boots where none of these
// hardware states were active.
func resetPicoHardware(sp *phone.SerialPort) {
	slog.Info("pico: clearing residual hardware state on startup")
	sp.Ring(false)
	sp.LED("OFF")
}

// playPairingAnnouncement queues one full pairing-voice sequence on mixer:
// silence pad, welcome, the code digits, and the "expires in N minute(s)"
// tail. code and receivedAt are passed explicitly (not read from cb) so the
// caller controls the cross-goroutine read: the announcement runs from a
// goroutine spawned on HOOK:OFF and reading cb.pairingCode there would race
// the dispatcher's TypePairingCode handler. minutesLeft is computed from
// receivedAt on each call so a long-listening user hears an accurate
// countdown.
func playPairingAnnouncement(mixer *audio.Mixer, code string, receivedAt time.Time) {
	mixer.PlayOnce("pairing_silence")
	mixer.PlayOnce("pairing_welcome")
	for _, ch := range code {
		mixer.PlayOnce("spoken_" + string(ch))
	}
	minutesLeft := int(math.Ceil(pairingRefreshInterval.Minutes() - time.Since(receivedAt).Minutes()))
	if minutesLeft < 1 {
		minutesLeft = 1
	} else if minutesLeft > 10 {
		minutesLeft = 10
	}
	unitClip := "pairing_expires_minutes"
	if minutesLeft == 1 {
		unitClip = "pairing_expires_minute"
	}
	mixer.PlayOnce("pairing_expires_prefix")
	mixer.PlayOnce(fmt.Sprintf("spoken_%d", minutesLeft))
	mixer.PlayOnce(unitClip)
}

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		os.Exit(0)
	}

	// --- Config file loading ---
	// Load from JSON file, then let CLI flags override individual fields.
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Effective server URL: CLI flag > config file > built-in default
	effectiveServerURL := cfg.ServerURL
	if *signaldURL != "" {
		effectiveServerURL = *signaldURL // CLI flag wins
	}
	if effectiveServerURL == "" {
		effectiveServerURL = "wss://localhost:8443/ws" // backward-compat default
	}

	// Effective phone number: CLI flag > config file
	effectiveNumber := cfg.PhoneNumber
	if *numberFlag != "" {
		effectiveNumber = *numberFlag // CLI flag wins
	}

	if effectiveNumber == "" {
		effectiveNumber = "unpaired"
		slog.Info("digitsd: no phone number configured -- starting in pairing mode")
	}

	slog.Info("digitsd starting", "server", effectiveServerURL, "number", effectiveNumber, "config", *configPath)

	// 0. Extract embedded assets on version change
	extractor := &assets.Extractor{
		FS:         assets.SubFS(),
		RootDir:    "/",
		DataDir:    "/data/digits",
		MarkerPath: "/data/digits/asset-version",
		Remount: func(rw bool) error {
			mode := "ro"
			if rw {
				mode = "rw"
			}
			return exec.Command("sudo", "mount", "-o", "remount,"+mode, "/").Run()
		},
		RootfsWriteFile: func(data []byte, dest string, perm os.FileMode) error {
			// Write to a temp file, then sudo cp + chmod to the rootfs destination.
			// This matches the pattern used by the existing updater for binary replacement.
			tmp, err := os.CreateTemp("", "asset-*")
			if err != nil {
				return fmt.Errorf("create temp: %w", err)
			}
			tmpPath := tmp.Name()
			defer os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup
			if _, err := tmp.Write(data); err != nil {
				_ = tmp.Close()
				return fmt.Errorf("write temp: %w", err)
			}
			_ = tmp.Close()

			if err := exec.Command("sudo", "mkdir", "-p", filepath.Dir(dest)).Run(); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), err)
			}
			if err := exec.Command("sudo", "cp", tmpPath, dest).Run(); err != nil {
				return fmt.Errorf("cp to %s: %w", dest, err)
			}
			if err := exec.Command("sudo", "chmod", fmt.Sprintf("%o", perm), dest).Run(); err != nil {
				return fmt.Errorf("chmod %s: %w", dest, err)
			}
			return nil
		},
		ReloadSystemd: func() error {
			return exec.Command("sudo", "systemctl", "daemon-reload").Run()
		},
	}
	if err := extractor.Extract(version.Version); err != nil {
		slog.Warn("asset extraction failed", "err", err)
	}

	// Render the active SWD config from the per-variant file matching this
	// hardware. /etc/digits-pcb-rev is stamped by build-image.sh and is the
	// single source of truth for which fab revision this image targets.
	// Re-evaluated on every startup so editing the marker self-heals without
	// needing an asset-version bump; skip the rootfs remount + write when
	// the on-disk file already matches, which is the steady-state case.
	pcbRev := readPCBRev()
	embedSrc := fmt.Sprintf("rootfs/usr/local/share/digits/swd/digits-swd-v%s.cfg", pcbRev)
	if data, err := fs.ReadFile(assets.SubFS(), embedSrc); err != nil {
		slog.Warn("swd render: read embed failed", "src", embedSrc, "err", err)
	} else if existing, _ := os.ReadFile(defaultSWDConfig); bytes.Equal(existing, data) {
		slog.Info("swd render: already current", "pcb_rev", pcbRev)
	} else if err := extractor.Remount(true); err != nil {
		slog.Warn("swd render: remount rw failed", "err", err)
	} else {
		if err := extractor.RootfsWriteFile(data, defaultSWDConfig, 0644); err != nil {
			slog.Warn("swd render: write failed", "dest", defaultSWDConfig, "err", err)
		} else {
			slog.Info("swd render: installed config", "pcb_rev", pcbRev)
		}
		if err := extractor.Remount(false); err != nil {
			slog.Warn("swd render: remount ro failed", "err", err)
		}
	}

	// Render the active mixer state from the per-codec embedded file. Picked
	// by detectCodec() walking /proc/asound, so this naturally tracks V1↔V2
	// hardware swaps. The on-disk file is the canonical apply target for
	// digits-mixer.service, which runs alsactl restore at boot before
	// digitsd starts. On the first boot after an OTA that changed the
	// embedded state, the service will have applied the previous-version
	// file; we update the file here so the next reboot picks up the new
	// canonical. Live re-apply is intentionally skipped: it would need a
	// sudoers rule for `alsactl restore` that does not currently ship to
	// the digits user, and the existing OTA channel cannot update sudoers
	// (etc/sudoers.d/digits-updater is in the Makefile's OVERLAY_EXCLUDE).
	mixerEmbedSrc := fmt.Sprintf("mixer/v%s.state", audio.CodecPCBVariant())
	mixerCard := audio.CodecCardName()
	if data, err := fs.ReadFile(assets.SubFS(), mixerEmbedSrc); err != nil {
		slog.Warn("mixer render: read embed failed", "src", mixerEmbedSrc, "err", err)
	} else if existing, _ := os.ReadFile(mixerStatePath); bytes.Equal(existing, data) {
		slog.Info("mixer render: already current", "card", mixerCard)
	} else if err := extractor.RootfsWriteFile(data, mixerStatePath, 0644); err != nil {
		slog.Warn("mixer render: write failed", "dest", mixerStatePath, "err", err)
	} else {
		slog.Info("mixer render: wrote canonical state, applies on next reboot via digits-mixer.service", "card", mixerCard, "size", len(data))
	}

	// 1. Open serial port directly (log to both stdout and uart.log file)
	uartLogPath := filepath.Join(filepath.Dir(*socketPath), "uart.log")
	uartLogFile, err := os.OpenFile(uartLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Warn("cannot open uart log, logging to stdout only", "path", uartLogPath, "error", err)
		uartLogFile = nil
	}
	var serialWriter io.Writer = os.Stdout
	if uartLogFile != nil {
		serialWriter = io.MultiWriter(os.Stdout, uartLogFile)
		defer func() { _ = uartLogFile.Close() }()
	}
	serialLogger := slog.New(slog.NewTextHandler(serialWriter, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	sp, err := phone.OpenSerial(*serialDev, 115200, serialLogger)
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	defer func() { _ = sp.Close() }()

	// Post-reboot firmware version results. STATUS:READY (external SWD flash
	// or power cycle) and our own auto-update flow both invoke
	// requeryFirmware to retry QueryVersion (the Pico's UART command loop
	// isn't quite awake yet at the instant the chip finishes booting). The
	// goroutine sends the result on fwVersionCh so the main loop can update
	// fwVersion/fwCommit without any shared-state synchronization.
	//
	// requeryInFlight dedupes overlapping calls. After an auto-update flash
	// both afterFirmwareUpdated and the Pico's STATUS:READY message want to
	// trigger a requery within the same second; the second call becomes a
	// no-op and avoids a "channel full, dropping" warning. The flag clears
	// when the goroutine returns, so a later genuine reboot starts fresh.
	type fwVersionResult struct{ version, commit string }
	fwVersionCh := make(chan fwVersionResult, 1)
	var requeryInFlight atomic.Bool
	requeryFirmware := func() {
		if !requeryInFlight.CompareAndSwap(false, true) {
			slog.Debug("pico: requery already in flight, skipping duplicate trigger")
			return
		}
		go func() {
			defer requeryInFlight.Store(false)
			const attempts = 5
			for attempt := 1; attempt <= attempts; attempt++ {
				v, c, err := sp.QueryVersion()
				if err == nil {
					select {
					case fwVersionCh <- fwVersionResult{version: v, commit: c}:
					default:
						slog.Warn("pico: version result channel full, dropping")
					}
					return
				}
				slog.Warn("pico: version query attempt failed", "attempt", attempt, "error", err)
				if attempt < attempts {
					time.Sleep(500 * time.Millisecond)
				}
			}
			slog.Warn("pico: version query after reboot gave up")
		}()
	}

	// POST: verify Pico is alive
	postRetries := 3
	postOk := false
	for i := 1; i <= postRetries; i++ {
		if err := sp.Ping(); err == nil {
			postOk = true
			break
		}
		slog.Warn("POST: no PONG", "attempt", i, "max", postRetries)
		time.Sleep(1 * time.Second)
	}
	if postOk {
		slog.Info("POST: PASS -- Pico UART healthy")
	} else {
		slog.Warn("POST: FAIL -- Pico not responding")
		// reflashPico closes the original sp before flashing and returns a
		// freshly-opened port regardless of whether post-flash PING passed.
		// Always take newSp; only the postOk flag is gated on success.
		newSp, ok := reflashPico(sp, *serialDev, serialLogger, "post-fail")
		sp = newSp
		if ok {
			postOk = true
		}
		if !postOk {
			slog.Warn("POST: Continuing without Pico. Phone will not function.")
		}
	}

	// Query firmware version (best-effort)
	var fwVersion, fwCommit string
	if postOk {
		if v, c, err := sp.QueryVersion(); err != nil {
			slog.Warn("firmware version query failed", "error", err)
		} else {
			fwVersion, fwCommit = v, c
			slog.Info("firmware version", "version", fwVersion, "commit", fwCommit)
		}
	}

	// A re-flashed SD card must overwrite stale Pico firmware; the PING-fail
	// path doesn't fire when the Pico responds.
	if postOk && fwVersion != "" {
		bundled := readBundledFirmwareVersion()
		if firmwareNeedsReflash(fwVersion, bundled) {
			slog.Warn("firmware version mismatch with bundled image",
				"pico", fwVersion, "bundled", bundled,
				"action", "auto-reflash")
			newSp, ok := reflashPico(sp, *serialDev, serialLogger, "version-mismatch")
			sp = newSp
			if ok {
				if v, c, err := sp.QueryVersion(); err == nil {
					fwVersion, fwCommit = v, c
					slog.Info("firmware version after reflash", "version", fwVersion, "commit", fwCommit)
				} else {
					slog.Warn("firmware version query after reflash failed", "error", err)
					fwVersion, fwCommit = "", ""
				}
			}
		}
	}

	// Cross-check the firmware's runtime-detected board against the Pi's
	// /etc/digits-pcb-rev marker. flash-pico.sh writes the rev byte during
	// deploy so they normally agree; the override path covers fresh chips
	// that have not been Pi-flashed yet, or boards moved between Pi units.
	firmwareBoard := ""
	if postOk {
		name, raw, err := sp.QueryBoard()
		if err != nil {
			slog.Warn("firmware board query failed", "error", err, "raw", raw)
		} else {
			firmwareBoard = name
			slog.Info("firmware board", "name", firmwareBoard, "raw", raw)
		}
	}

	if firmwareBoard != "" && pcbRev != "" {
		expectedFw := "v" + pcbRev
		if firmwareBoard != expectedFw {
			slog.Warn("firmware board / pcb_rev mismatch",
				"firmware", firmwareBoard,
				"pcb_rev", pcbRev,
				"action", "sending CONFIG:PCB_REV override")

			cmd := "CONFIG:PCB_REV=" + expectedFw
			if resp, err := sp.SendCommand(cmd, 1*time.Second); err != nil {
				slog.Error("hardware: CONFIG:PCB_REV send failed",
					"cmd", cmd, "error", err)
			} else {
				slog.Info("hardware: CONFIG:PCB_REV applied",
					"cmd", cmd, "resp", resp)
			}
		}
	}

	// Gate HOOK:FLASH forwarding on firmware version.
	// Only v1.5.0+ emits HOOK:FLASH; older firmware must not forward stray events.
	hookFlash := hookFlashCapable(fwVersion)
	slog.Info("firmware capability", "version", fwVersion, "flash_capable", hookFlash)
	sp.SetFlashEnabled(hookFlash)

	// Reconcile hook polarity with the firmware on every startup. The Pico
	// retains whichever invert state was last set across a Pi reboot (only
	// loses it on Pico power loss), so removing hook_inverted from config
	// would otherwise leave a stale HOOK:INVERT:ON in effect. Send the
	// matching command for either direction every time.
	if postOk {
		hookInvertCmd := "HOOK:INVERT:OFF"
		if cfg.HookInverted {
			hookInvertCmd = "HOOK:INVERT:ON"
		}
		var hookOk bool
		for i := 1; i <= 3; i++ {
			resp, err := sp.SendCommand(hookInvertCmd, 2*time.Second)
			if err == nil && resp == hookInvertCmd {
				slog.Info("hook invert: configured", "resp", resp)
				hookOk = true
				break
			}
			slog.Warn("hook invert: attempt failed", "attempt", i, "resp", resp, "error", err)
			time.Sleep(500 * time.Millisecond)
		}
		if !hookOk {
			log.Fatalf("hook invert: failed after 3 attempts, refusing to run with possibly wrong hook polarity")
		}
	}

	// Clear any residual Pico hardware state from before the last reboot.
	// If the Pi crashed mid-ring the Pico keeps ringing until told otherwise;
	// this is a safe no-op on clean boots.
	if postOk {
		resetPicoHardware(sp)
	}

	// 2. Open ALSA playback. V1 uses plughw direct to the codec; V2 routes
	// through a /etc/asound.conf plug device that pins hardware to 44.1 kHz
	// and resamples 48 kHz application audio for the chip's PLL-friendly
	// rate.
	pbDev := *alsaDevice
	if pbDev == "" {
		pbDev = audio.CodecPlaybackDevice()
		slog.Info("alsa playback: using codec", "device", pbDev)
	}
	pbCfg := audio.Config{
		Device:     pbDev,
		SampleRate: 48000,
		Channels:   1,
		FrameSize:  960,
	}
	pb, err := audio.NewPlayback(pbCfg)
	if err != nil {
		log.Fatalf("alsa playback: %v", err)
	}
	defer pb.Close()

	// 3. Create mixer and load tones
	mixer := audio.NewMixer(pb)
	if err := mixer.LoadTonesFromDir(*toneDir); err != nil {
		log.Fatalf("mixer load tones: %v", err)
	}
	// Load pairing voice prompts (subdirectory)
	pairingDir := filepath.Join(*toneDir, "pairing")
	if _, err := os.Stat(pairingDir); err == nil {
		if err := mixer.LoadTonesFromDir(pairingDir); err != nil {
			slog.Warn("could not load pairing tones", "error", err)
		}
	}
	mixer.Start()
	defer mixer.Stop()

	// Debug: capture raw PCM output if CAPTURE_PCM is set
	if capPath := os.Getenv("CAPTURE_PCM"); capPath != "" {
		if err := mixer.EnableCapture(capPath); err != nil {
			slog.Warn("PCM capture failed", "error", err)
		} else {
			defer mixer.DisableCapture()
		}
	}

	deviceID, err := config.LoadOrCreateDeviceID()
	if err != nil {
		slog.Warn("device ID unavailable", "error", err)
	}

	// 4. Create signaling client
	sig := sigclient.NewClient(effectiveServerURL, effectiveNumber, deviceID, cfg.DeviceToken)

	// 5. Create service code handler
	// doubleBeep plays two short DTMF star tones as an audible confirmation.
	doubleBeep := func() {
		mixer.PlayOnce("dtmf_star")
		time.Sleep(150 * time.Millisecond)
		mixer.PlayOnce("dtmf_star")
		time.Sleep(300 * time.Millisecond)
	}

	svcCodes := phone.NewServiceCodeHandler()
	confirmer := phone.NewConfirmer()
	// resetToDialtone is filled in once ctrl is created (lower in main).
	// Captured by the confirm helper's onTimeout so the FSM state matches
	// the audio state we restore: without ResetToDialtone the FSM stays
	// in IDLE (where ctrl.Reset left it after the original Terminal
	// dispatch) and the next keypress fails to trigger SendTone(ToneStop),
	// so the dial tone loops under the user's DTMF beeps. Same regression
	// covered by controller_test.go:1694.
	var resetToDialtone func()
	confirm := func(promptName string, action func()) {
		mixer.StopTone()
		mixer.PlayOnce(promptName)
		armed := confirmer.Arm(action, func() {
			slog.Info("confirmer: timed out")
			mixer.StopAll()
			mixer.PlayLoop("tone_dial")
			if resetToDialtone != nil {
				resetToDialtone()
			}
		}, 10*time.Second)
		if !armed {
			slog.Warn("confirm: another confirmation already pending, ignoring", "prompt", promptName)
		}
	}
	svcCodes.OnVolume = func(level int) {
		if err := phone.SetVolume(level); err != nil {
			slog.Warn("volume set failed", "error", err)
		}
		mixer.StopAll()
		time.Sleep(250 * time.Millisecond)
		doubleBeep()
		mixer.PlayLoop("tone_dial")
	}
	svcCodes.OnShutdown = func() {
		slog.Info("service code: executing shutdown")
		_ = exec.Command("sudo", "shutdown", "-h", "now").Run()
	}
	svcCodes.OnReboot = func() {
		slog.Info("service code: executing reboot")
		_ = exec.Command("sudo", "reboot").Run()
	}
	svcCodes.OnSetup = func() {
		slog.Info("service code: *#SETUP# (*#73887#) -> awaiting confirmation")
		confirm("confirm_wifi_setup", func() {
			slog.Info("service code setup confirmed: removing wifi-configured flag, rebooting")
			// /data/wifi-configured is owned by root (digits-setup writes it
			// as root); /data itself is mode 755 root:root. digitsd runs as
			// the 'digits' user, which lacks write access to /data and so
			// cannot unlink the flag directly. The digits-updater sudoers
			// entry grants NOPASSWD on rm -f for this exact path.
			out, err := exec.Command("sudo", "/usr/bin/rm", "-f", phone.WifiConfiguredFlag).CombinedOutput()
			if err != nil {
				slog.Warn("service code setup: remove wifi flag failed", "path", phone.WifiConfiguredFlag, "error", err, "output", strings.TrimSpace(string(out)))
			} else {
				slog.Info("service code setup: removed wifi flag -- Pi will boot into AP mode", "path", phone.WifiConfiguredFlag)
			}
			// Spoken cue + 200ms ALSA-teardown pad so the tail of "...mode"
			// isn't clipped by systemd shutdown.
			mixer.StopAll()
			mixer.PlayOnce("rebooting_into_access_point_mode")
			for mixer.OncePlaying() {
				time.Sleep(50 * time.Millisecond)
			}
			time.Sleep(200 * time.Millisecond)
			_ = exec.Command("sudo", "reboot").Run()
		})
	}

	svcCodes.OnAudioTest = func() {
		slog.Info("service code: *#TEST# -> audio test (record 5s, playback)")
		mixer.StopAll()

		doubleBeep()
		for mixer.OncePlaying() {
			time.Sleep(50 * time.Millisecond)
		}

		// Open capture AFTER beeps so there's no startup delay.
		// Use the default pipeline config so the self-test plays back
		// exactly what the remote peer hears in a real call.
		pipCfg := audio.DefaultPipelineConfig()
		pip := audio.NewPipeline(pipCfg)
		if err := pip.Start(); err != nil {
			slog.Error("audio test: pipeline start failed", "error", err)
			mixer.PlayLoop("tone_dial")
			return
		}

		const sampleRate = 48000
		const seconds = 5
		recorded := make([]int16, 0, sampleRate*seconds)

		deadline := time.After(time.Duration(seconds) * time.Second)
	capture:
		for {
			select {
			case frame := <-pip.OutFrames():
				recorded = append(recorded, frame...)
			case <-deadline:
				break capture
			}
		}
		pip.Stop()
		// Drain any frames buffered between deadline and Stop
		for {
			select {
			case frame := <-pip.OutFrames():
				recorded = append(recorded, frame...)
			default:
				goto drained
			}
		}
	drained:

		var maxAmp int32
		for _, s := range recorded {
			a := int32(s)
			if a < 0 {
				a = -a
			}
			if a > maxAmp {
				maxAmp = a
			}
		}
		slog.Info("audio test: captured, playing back", "samples", len(recorded), "duration_s", float64(len(recorded))/float64(sampleRate), "peak", maxAmp)

		const dumpPath = "/tmp/audiotest_last.wav"
		if err := writePCMWav(dumpPath, recorded, sampleRate); err != nil {
			slog.Warn("audio test: wav dump failed", "path", dumpPath, "error", err)
		} else {
			slog.Info("audio test: wav dumped", "path", dumpPath)
		}
		mixer.PlayOnceSamples(recorded)
		time.Sleep(100 * time.Millisecond)
		for mixer.OncePlaying() {
			time.Sleep(100 * time.Millisecond)
		}
		doubleBeep()
		slog.Info("audio test: complete")

		// Resume dial tone
		mixer.PlayLoop("tone_dial")
	}

	// Detect SWD flash capability. Start with file existence checks; if the
	// required binaries are present, probe the SWD bus in the background to
	// confirm the Pico is actually wired up.
	var flashCapable atomic.Bool
	_, err1 := os.Stat(defaultOpenOCD)
	_, err2 := os.Stat("/usr/local/bin/flash-pico.sh")
	swdFilesPresent := err1 == nil && err2 == nil

	if swdFilesPresent {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sudo", defaultOpenOCD,
				"-f", defaultSWDConfig,
				"-f", "target/rp2040.cfg",
				"-c", "init; shutdown")
			out, err := cmd.CombinedOutput()
			if err != nil {
				slog.Warn("swd probe: Pico not detected on SWD bus", "error", err, "output", string(out))
			} else {
				slog.Info("swd probe: Pico detected on SWD bus, enabling flash capability")
				flashCapable.Store(true)
			}
			sendDeviceInfo(sig, fwVersion, fwCommit, flashCapable.Load())
		}()
	}

	svcCodes.OnRepair = func() {
		slog.Info("service code: *#0* -> awaiting confirmation")
		confirm("confirm_re_pair", func() {
			slog.Info("service code repair confirmed: clearing device token, rebooting into pairing mode")
			// Tell the server to invalidate its copy of paired_at + device_token
			// over the still-authenticated WS, BEFORE we clear the local token
			// or reboot. Without this the server keeps thinking we're paired
			// and rejects our next register-without-token as "device_token
			// required", looping forever. Brief sleep so the message lands
			// (writes are async on the WS Send channel).
			sendSignal(sig, &sigclient.Message{Type: sigclient.TypeRepair, HardwareID: deviceID})
			time.Sleep(500 * time.Millisecond)
			if cfg != nil {
				cfg.DeviceToken = ""
				cfg.PairingCode = ""
				if err := cfg.Save(); err != nil {
					slog.Warn("service code repair: save config failed", "error", err)
				}
			}
			_ = exec.Command("sudo", "reboot").Run()
		})
	}

	svcCodes.OnUpdate = func() {
		slog.Info("service code: *#UPDATE# (*#873283#) -- checking for updates")
		go runTargetedUpdate(effectiveServerURL, version.Version, fwVersion, "", "", flashCapable.Load(), nil, requeryFirmware)
	}

	svcCodes.OnFactoryReset = func() {
		slog.Info("service code: *#00000# -> awaiting confirmation")
		confirm("confirm_factory_reset", func() {
			slog.Info("service code factory reset confirmed")
			triggerFactoryReset(sig, deviceID)
		})
	}

	// 6b. Create easter egg detector
	easterEggs := phone.NewEasterEggDetector([]phone.EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}, func(clip string) {
		slog.Info("phone: playing easter egg clip", "clip", clip)
		mixer.StopTone()
		mixer.PlayOnce(clip)
	})

	// 7. Create callbacks

	// Link-health env vars: kill switch and cadence.
	linkHealthDisabled := os.Getenv("DIGITSD_LINK_HEALTH_DISABLED") == "1"
	linkHealthIntervalMs := 2000
	if v := os.Getenv("DIGITSD_LINK_HEALTH_INTERVAL_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			linkHealthIntervalMs = n
		} else {
			slog.Warn("invalid DIGITSD_LINK_HEALTH_INTERVAL_MS; using default", "raw", v)
		}
	}
	linkHealthInterval := time.Duration(linkHealthIntervalMs) * time.Millisecond

	cb := &daemonCallbacks{
		serial:              sp,
		sig:                 sig,
		mixer:               mixer,
		serviceCodes:        svcCodes,
		number:              effectiveNumber,
		cfg:                 cfg,
		debugMode:           os.Getenv("DIGITS_DEBUG") == "1",
		linkHealthDisabled:  linkHealthDisabled,
		linkHealthInterval:  linkHealthInterval,
		meshReporterCancels: make(map[string]context.CancelFunc),
	}
	if cfg.DeviceToken != "" {
		cb.paired.Store(true)
	}

	// Wire auto-update. The closure captures run()-scoped variables so that
	// HangupCall (which lives on daemonCallbacks) can trigger an update
	// without needing direct access to those locals.
	cb.autoUpdateEnabled.Store(cfg.AutoUpdate)
	cb.triggerAutoUpdate = func() {
		runAutoUpdate(cb, effectiveServerURL, version.Version, fwVersion, flashCapable.Load(), requeryFirmware)
	}
	if cb.autoUpdateEnabled.Load() {
		slog.Info("auto-update: enabled, checking for updates on startup")
		go cb.triggerAutoUpdate()
	}

	// 8. Create phone Controller
	ctrl := phone.NewController(cb, effectiveNumber)
	cb.ctrl = ctrl
	ctrl.SetSilentMode(cfg.SilentMode)
	// Wire the confirmer's onTimeout FSM-reset hook now that ctrl exists.
	// Also drop any digits the firmware accumulated while the user typed the
	// service code; same Pico-buffer concern as the confirmer wrong-key path.
	resetToDialtone = func() {
		ctrl.ResetToDialtone()
		sp.SendFire("DIAL:RESET")
	}

	// 8b. Contacts cache: optional dial safelist, persisted to disk.
	// An empty cache leaves the checker nil so no-contacts phones allow
	// every call (matching the pre-wiring behavior).
	contactsPath := filepath.Join(filepath.Dir(*configPath), "contacts.json")
	contactsCache := contacts.NewCache(contactsPath)
	if err := contactsCache.Load(); err != nil {
		slog.Warn("contacts: load failed", "path", contactsPath, "error", err)
	} else if n := contactsCache.Count(); n > 0 {
		ctrl.SetContactChecker(contactsCache)
		slog.Info("contacts: loaded safelist", "path", contactsPath, "count", n)
	} else {
		slog.Info("contacts: no local list, filter disabled", "path", contactsPath)
	}

	// 9. Start socket server (backward compat for debugging + latclient auto-answer)
	sockSrv, err := phone.NewSocketServer(*socketPath, cb)
	if err != nil {
		log.Fatalf("socket server: %v", err)
	}
	defer sockSrv.Close()

	// 10. WiFi auto-fallback supervisor. Must start before the signaling
	// connect attempt: if the network is wedged (wrong WiFi password, DNS
	// failure), the supervisor is the only path back to AP/setup mode.
	wifiSupervisor := wififallback.NewSupervisor(
		cfg.WiFiFallback,
		wififallback.NewNMCLIChecker(),
		wififallback.NewScriptAPController(),
		ctrl.IsCallActive,
		slog.Default().With("subsystem", "wifi-fallback"),
	)
	wifiCtx, wifiCancel := context.WithCancel(context.Background())
	defer wifiCancel()
	go wifiSupervisor.Run(wifiCtx)

	// Clear boot counter: digitsd reached its main daemon phase with the
	// wifi-fallback supervisor running. Even if the network is down, the
	// supervisor will eventually bring the device into AP mode, so the
	// device is not wedged. The initramfs boot-check increments this
	// counter on every boot; clearing it here prevents threshold-based
	// recovery from triggering on a device that is functioning normally.
	if err := bootcount.Clear(bootcount.DefaultPath); err != nil {
		slog.Warn("failed to clear boot counter", "err", err)
	} else {
		slog.Info("boot counter: cleared (healthy boot)")
	}

	// 11. Ready
	phone.RestoreVolume()
	slog.Info("digitsd ready")

	// Start hardware watchdog (if available)
	if wd, err := watchdog.Open("/dev/watchdog"); err == nil {
		wd.Start(5 * time.Second)
		defer wd.Close()
		slog.Info("watchdog: started", "interval", "5s")
	} else {
		slog.Debug("watchdog: not available", "err", err)
	}

	// 12. Connect signaling client (non-fatal: the main loop reconnects)
	if err := sig.Connect(); err != nil {
		slog.Warn("signald connect failed, will retry", "error", err)
	} else {
		sendDeviceInfo(sig, fwVersion, fwCommit, flashCapable.Load())
		requestICEServers(sig)
	}

	// Refresh ICE credentials periodically (TURN creds are time-limited).
	// Read cb.sig under cb.mu so we use the current client after reconnects.
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cb.mu.Lock()
			s := cb.sig
			cb.mu.Unlock()
			if s != nil {
				requestICEServers(s)
			}
		}
	}()

	// Pairing code refresh: reconnect before the code expires so the
	// server issues a fresh one. Timer starts when we receive a code.
	pairingRefresh := time.NewTimer(0)
	if !pairingRefresh.Stop() {
		<-pairingRefresh.C
	}

	// pairingAnnouncementCancel cancels the in-flight pairing-announcement
	// repeat goroutine spawned on HOOK:OFF (unpaired). nil when no goroutine
	// is running. Only the dispatcher select case touches this var, so no
	// mutex is needed.
	var pairingAnnouncementCancel context.CancelFunc

	// OS signal handling
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Main select loop
	for {
		select {
		case <-quit:
			slog.Info("digitsd shutting down")
			ctrl.Close()
			mixer.Stop()
			if err := sig.Close(); err != nil {
				slog.Warn("sig close failed", "error", err)
			}
			return

		case event := <-sp.Events():
			// Confirmer intercept: when a sensitive op is awaiting "press *
			// to confirm", KEY:* fires the action and any other key cancels
			// (with dial-tone restored). HOOK:ON cancels and falls through
			// so the existing HOOK:ON handler wipes audio + resets state.
			// Auto-cancel happens via the timer's onTimeout (also restores
			// dial tone). Service-code dispatch and FSM forwarding both run
			// AFTER this block, so the intercepted * is never re-interpreted.
			if confirmer.Active() {
				if strings.HasPrefix(event, "KEY:") && len(event) > 4 {
					key := string(event[4])
					if key == "*" {
						slog.Info("confirmer: * pressed, firing pending action")
						// StopAll BEFORE Fire so the prompt audio is silent
						// before the action goroutine runs. Otherwise the
						// action's own mixer.StopAll + PlayOnce can race
						// with this StopAll and have its queued clip wiped.
						mixer.StopAll()
						confirmer.Fire()
						continue
					}
					slog.Info("confirmer: cancelled by other key", "key", key)
					confirmer.Cancel()
					mixer.StopAll()
					mixer.PlayLoop("tone_dial")
					// Pair the audio reset with an FSM reset (same regression
					// covered by resetToDialtone above).
					ctrl.ResetToDialtone()
					// Pico keeps its own digit buffer; the cancel key + the
					// digits the user already typed for the service code are
					// still in it. Without this clear, the next ~2 dialed
					// digits push past 7-total and the firmware fires a
					// stale DIAL with the leftovers as the prefix.
					sp.SendFire("DIAL:RESET")
					continue
				}
				if event == "HOOK:ON" {
					slog.Info("confirmer: cancelled by hang-up")
					confirmer.Cancel()
					// fall through: existing HOOK:ON handler wipes audio
				}
			}

			// Unpaired phone: play pairing voice sequence instead of dial tone,
			// then re-play it on a timer so a user who keeps the handset off
			// the cradle can hear the code again without hanging up. The loop
			// goroutine exits cleanly on HOOK:ON (cancel below), on pairing
			// completion, or when the code is cleared.
			if event == "HOOK:OFF" && !cb.paired.Load() && cb.pairingCode != "" {
				mixer.StopAll()
				if pairingAnnouncementCancel != nil {
					pairingAnnouncementCancel()
				}
				ctx, cancel := context.WithCancel(context.Background())
				pairingAnnouncementCancel = cancel
				// Snapshot the pairing-code state on the dispatcher goroutine
				// (the same one TypePairingCode writes from) and pass into the
				// announcement goroutine. Avoids cross-goroutine reads of
				// cb.pairingCode + cb.pairingCodeReceivedAt. The code/receivedAt
				// won't change for this off-hook session: a new code only lands
				// after pairingRefreshInterval, by which point HOOK:ON has
				// cancelled this goroutine. cb.paired is atomic, so the
				// post-pair exit check stays accurate without a snapshot.
				code := cb.pairingCode
				receivedAt := cb.pairingCodeReceivedAt
				slog.Info("phone: playing pairing code via voice", "code", code)
				sp.LED("ON")
				go func() {
					for {
						if cb.paired.Load() || code == "" {
							return
						}
						playPairingAnnouncement(mixer, code, receivedAt)
						for mixer.OncePlaying() {
							select {
							case <-ctx.Done():
								return
							case <-time.After(50 * time.Millisecond):
							}
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(pairingAnnouncementInterval):
						}
					}
				}()
				continue // skip normal controller handling
			}

			// The controller's FSM stops the dial tone loop on the first key
			// via SendTone(ToneStop); we only queue the DTMF beep here.
			if strings.HasPrefix(event, "KEY:") && len(event) > 4 {
				key := string(event[4])
				dtmfName := dtmfToneName(key)
				if dtmfName != "" {
					mixer.PlayOnce(dtmfName)
				}
				// Forward DTMF to the remote peer if a call is connected.
				if ctrl.State() == phone.StateCONNECTED {
					cb.mu.Lock()
					peer := cb.callPeer
					cb.mu.Unlock()
					if peer != "" {
						sendSignal(sig, &sigclient.Message{
							Type:  sigclient.TypeDTMF,
							To:    peer,
							Digit: key,
						})
					}
				}
				// Mid-service-code: route the key only to svcCodes so easter
				// eggs (e.g., "0000" Rick Roll) cannot eat digits belonging
				// to a code like "*#00000#" (factory reset). Otherwise: try
				// easter eggs first, then fall through to service codes.
				inCode := svcCodes.InCode()
				if inCode || !easterEggs.AddKey(key) {
					switch svcCodes.AddKey(key) {
					case phone.ServiceCodeTerminal:
						ctrl.Reset()
						// Drop the digits the firmware accumulated while the
						// user was typing the service code (see comment near
						// the confirmer cancel above). Otherwise post-code
						// dialing fires DIAL on a stale prefix.
						sp.SendFire("DIAL:RESET")
						continue // skip forwarding to controller
					case phone.ServiceCodeNonTerminal:
						ctrl.ResetToDialtone()
						sp.SendFire("DIAL:RESET")
						continue
					}
				}
			}

			// Intercept dial easter eggs (e.g. 867-5309) before FSM processes them
			if strings.HasPrefix(event, "DIAL:") && len(event) > 5 {
				number := event[5:]
				if egg, ok := phone.DialEasterEggs[number]; ok {
					slog.Info("phone: dial easter egg", "name", egg.Name, "number", number)
					mixer.StopTone()
					mixer.PlayOnce(egg.Clip)
					continue // don't place the call
				}
			}

			// Pico rebooted (e.g. external flash, power cycle): re-query
			// firmware version and report it to the server. The Pico emits
			// STATUS:READY before its UART command loop is fully accepting
			// commands, so retry a few times in a goroutine. Running this
			// inline would block the event loop for up to ~15s during which
			// HOOK/KEY events would queue up or be dropped.
			if event == "STATUS:READY" {
				slog.Info("pico: detected reboot, re-querying firmware version")
				requeryFirmware()
				continue
			}

			// Forward all events to the FSM controller.
			// HOOK:FLASH is special: it requires the active peer from the daemon
			// layer, so it bypasses HandleEvent and goes through HandleHookFlash.
			if event == "HOOK:FLASH" {
				ctrl.HandleHookFlash(cb.currentPeer())
			} else {
				ctrl.HandleEvent(event)
			}

			// Hang-up: kill ALL audio immediately
			if event == "HOOK:ON" {
				mixer.StopAll()
				easterEggs.Reset()
				svcCodes.Reset()
				if pairingAnnouncementCancel != nil {
					pairingAnnouncementCancel()
					pairingAnnouncementCancel = nil
				}
			}

		case r := <-fwVersionCh:
			if r.version != fwVersion || r.commit != fwCommit {
				fwVersion, fwCommit = r.version, r.commit
				slog.Info("pico: firmware version changed", "version", fwVersion, "commit", fwCommit)
				hookFlash = hookFlashCapable(fwVersion)
				slog.Info("firmware capability", "version", fwVersion, "flash_capable", hookFlash)
				sp.SetFlashEnabled(hookFlash)
				sendDeviceInfo(sig, fwVersion, fwCommit, flashCapable.Load())
			} else {
				slog.Info("pico: firmware version unchanged", "version", fwVersion, "commit", fwCommit)
			}

		case msg := <-sig.Inbox():
			slog.Info("signal rx", "type", msg.Type, "from", msg.From)
			switch msg.Type {
			case sigclient.TypeRing:
				cb.mu.Lock()
				cb.pendingCaller = msg.From
				cb.mu.Unlock()
				ctrl.HandleSignal("ring", "")
			case sigclient.TypeAnswer:
				// Set remote description from the answer SDP before poking the FSM.
				cb.mu.Lock()
				if cb.peerMgr != nil && msg.SDP != "" {
					if err := cb.peerMgr.SetAnswer(msg.SDP); err != nil {
						slog.Error("webrtc: set answer failed", "error", err)
					} else {
						slog.Info("webrtc: set remote answer", "from", msg.From, "bytes", len(msg.SDP))
					}
				}
				cb.mu.Unlock()
				ctrl.HandleSignal("answer", msg.From)
			case sigclient.TypeHangup:
				ctrl.HandleSignal("hangup", msg.From)
			case sigclient.TypeBusy:
				ctrl.HandleSignal("busy", msg.From)
			case sigclient.TypeDTMF:
				// Remote peer pressed a digit during the call. Play the local
				// DTMF sample so the user hears what their peer is pressing,
				// matching real-phone behavior.
				if ctrl.State() != phone.StateCONNECTED {
					slog.Debug("dtmf: ignoring (not connected)", "from", msg.From)
					break
				}
				cb.mu.Lock()
				peer := cb.callPeer
				cb.mu.Unlock()
				if msg.From != peer {
					slog.Debug("dtmf: ignoring (wrong peer)", "from", msg.From, "expected", peer)
					break
				}
				if msg.Digit == "" {
					slog.Warn("dtmf: empty digit in message")
					break
				}
				dtmfName := dtmfToneName(msg.Digit)
				if dtmfName == "" {
					slog.Warn("dtmf: unrecognized digit", "digit", msg.Digit)
					break
				}
				mixer.PlayOnce(dtmfName)
			case sigclient.TypeError:
				slog.Warn("signal error", "error", msg.Error)
				// ADD_CALLING: route through the controller so state transitions
				// to ADD_INTERCEPT and the added peer is torn down. The user
				// flashes to return to the held party.
				if ctrl.State() == phone.StateADD_CALLING {
					ctrl.HandleSignal("error", msg.From)
					break
				}
				// 2-party CALLING: emulate real phone -- ringback -> SIT -> busy
				go func() {
					// 1. Brief silence (call setup delay, ~1s)
					time.Sleep(1 * time.Second)
					if ctrl.State() != phone.StateCALLING {
						return
					}
					// 2. Ringback for ~8s (simulates 1-2 rings)
					slog.Info("playing ringback (number unreachable)")
					mixer.PlayLoop("tone_ringback")
					time.Sleep(8 * time.Second)
					if ctrl.State() != phone.StateCALLING {
						return
					}
					// 3. SIT tones + "number not in service" announcement
					slog.Info("playing disconnected announcement")
					mixer.StopTone()
					mixer.PlayOnce("disconnected")
					// Wait for announcement to finish (poll rather than guess duration)
					for mixer.OncePlaying() {
						time.Sleep(200 * time.Millisecond)
						if ctrl.State() != phone.StateCALLING {
							return
						}
					}
					// 4. Brief silence, then reorder tone (fast busy) until hang-up
					time.Sleep(500 * time.Millisecond)
					if ctrl.State() != phone.StateCALLING {
						return
					}
					slog.Info("playing reorder tone")
					mixer.PlayLoop("tone_busy")
				}()
			case sigclient.TypeSDP:
				if msg.ConfID != "" {
					// Conference SDP: route to the mesh peer for this member.
					cb.mu.Lock()
					mesh := cb.mesh
					cb.mu.Unlock()

					if mesh == nil || mesh.GetPeer(msg.From) == nil {
						// No peer yet: we are the responder receiving the initiator's offer.
						answerSDP, err := cb.setupMeshResponder(msg.From, msg.SDP, msg.ConfID)
						if err != nil {
							slog.Error("conference: setupMeshResponder failed", "from", msg.From, "err", err)
							break
						}
						cb.mu.Lock()
						s := cb.sig
						cb.mu.Unlock()
						sendSignal(s, &sigclient.Message{
							Type:   sigclient.TypeSDP,
							To:     msg.From,
							ConfID: msg.ConfID,
							SDP:    answerSDP,
						})
						slog.Info("conference: sent SDP answer to initiator", "to", msg.From, "conf_id", msg.ConfID)
					} else {
						// Peer already exists: we were the initiator and this is the answer.
						if err := mesh.GetPeer(msg.From).SetAnswer(msg.SDP); err != nil {
							slog.Error("conference: set answer failed", "from", msg.From, "err", err)
						} else {
							slog.Info("conference: applied SDP answer from peer", "from", msg.From)
						}
					}
					break
				}
				cb.mu.Lock()
				switch {
				case cb.peerMgr == nil:
					// Incoming call: offer arrived before we've answered.
					// Stash it for AnswerCall to pick up.
					cb.pendingOffer = msg.SDP
					if cb.pendingCaller == "" && msg.From != "" {
						cb.pendingCaller = msg.From
						slog.Info("set pendingCaller from SDP", "from", msg.From)
					}
					slog.Info("stored pending SDP offer", "from", msg.From, "bytes", len(msg.SDP))
				case cb.isRestartingICE:
					// Mid-call: the only legitimate reason to receive an SDP
					// with an active peerMgr is the restart-answer we asked
					// for when we initiated an ICE restart.
					if err := cb.peerMgr.SetAnswer(msg.SDP); err != nil {
						slog.Error("webrtc: set restart answer failed", "error", err)
					} else {
						slog.Info("webrtc: applied restart answer", "from", msg.From, "bytes", len(msg.SDP))
					}
				default:
					slog.Warn("webrtc: unexpected SDP with active peer, ignoring", "from", msg.From, "bytes", len(msg.SDP))
				}
				cb.mu.Unlock()
			case sigclient.TypeICE:
				if msg.ConfID != "" {
					// Conference ICE: route to the mesh peer for this member.
					cb.mu.Lock()
					mesh := cb.mesh
					cb.mu.Unlock()
					if mesh == nil {
						slog.Warn("conference: ICE candidate before mesh initialized", "from", msg.From)
						break
					}
					pm := mesh.GetPeer(msg.From)
					if pm == nil {
						slog.Warn("conference: ICE candidate before peer created", "from", msg.From)
						break
					}
					if err := pm.AddICECandidate(msg.Candidate); err != nil {
						slog.Error("conference: add ICE candidate failed", "from", msg.From, "err", err)
					}
					break
				}
				cb.mu.Lock()
				if cb.peerMgr != nil {
					if err := cb.peerMgr.AddICECandidate(msg.Candidate); err != nil {
						slog.Warn("webrtc: add ICE candidate failed", "error", err)
					}
				} else {
					// Queue ICE candidates until peerMgr is ready (e.g. during RINGING before answer)
					cb.pendingICE = append(cb.pendingICE, msg.Candidate)
					slog.Info("queued ICE candidate (peerMgr not ready)", "total_queued", len(cb.pendingICE))
				}
				cb.mu.Unlock()
			case sigclient.TypeUpdateTrigger:
				slog.Info("signal: received update trigger from server", "target_pi", msg.TargetPiVersion, "target_fw", msg.TargetFWVersion)
				statusReporter := func(status, detail string) {
					sendSignal(sig, &sigclient.Message{
						Type:         sigclient.TypeUpdateStatus,
						UpdateStatus: status,
						UpdateDetail: detail,
					})
				}
				go runTargetedUpdate(effectiveServerURL, version.Version, fwVersion,
					msg.TargetPiVersion, msg.TargetFWVersion, flashCapable.Load(), statusReporter, requeryFirmware)

			case sigclient.TypeReleaseAvailable:
				slog.Info("signal: release_available", "pi", msg.LatestPiVersion, "fw", msg.LatestFWVersion)
				if cb.autoUpdateEnabled.Load() {
					go cb.triggerAutoUpdate()
				}

			case sigclient.TypeFactoryReset:
				slog.Info("factory reset: triggered by server")
				go triggerFactoryReset(sig, deviceID)

			case sigclient.TypeContacts, sigclient.TypeContactsUpdated:
				entries := make([]contacts.Entry, 0, len(msg.Contacts))
				for _, c := range msg.Contacts {
					entries = append(entries, contacts.Entry{Number: c.Number, Name: c.Name})
				}
				contactsCache.Update(entries)
				if len(entries) > 0 {
					ctrl.SetContactChecker(contactsCache)
				} else {
					ctrl.SetContactChecker(nil)
				}
				slog.Info("contacts: updated", "count", len(entries), "type", msg.Type)

			case sigclient.TypeICERestart:
				cb.mu.Lock()
				pm := cb.peerMgr
				peer := cb.callPeer
				cb.mu.Unlock()
				if pm == nil {
					slog.Info("ice-restart: no active peer connection, ignoring")
					break
				}
				slog.Info("ice-restart: received restart offer", "from", msg.From, "bytes", len(msg.SDP))
				answerSDP, err := pm.AcceptOffer(msg.SDP)
				if err != nil {
					slog.Error("ice-restart: accept offer failed", "error", err)
					break
				}
				cb.mu.Lock()
				cb.isRestartingICE = true
				if cb.restartTimer != nil {
					cb.restartTimer.Stop()
				}
				cb.startRestartTimeout()
				cb.mu.Unlock()
				if peer == "" {
					peer = msg.From
				}
				slog.Info("ice-restart: sending restart answer", "peer", peer, "bytes", len(answerSDP))
				sendSignal(sig, &sigclient.Message{
					Type: sigclient.TypeSDP,
					To:   peer,
					SDP:  answerSDP,
				})

			case sigclient.TypeICEServers:
				cb.mu.Lock()
				cb.iceServers = nil
				for _, s := range msg.Servers {
					cb.iceServers = append(cb.iceServers, owebrtc.ICEServerConfig{
						URLs:       s.URLs,
						Username:   s.Username,
						Credential: s.Credential,
					})
				}
				cb.mu.Unlock()
				slog.Info("ice: cached servers from signald", "count", len(msg.Servers))

			case sigclient.TypePairingCode:
				cb.pairingCode = msg.PairingCode
				cb.pairingCodeReceivedAt = time.Now()
				slog.Info("PAIRING REQUIRED: pick up handset to hear it", "code", msg.PairingCode)
				pairingRefresh.Reset(pairingRefreshInterval)

			case sigclient.TypePaired:
				pairingRefresh.Stop()
				if msg.DeviceToken != "" && cb.cfg != nil {
					cb.cfg.DeviceToken = msg.DeviceToken
					cb.cfg.PairingCode = ""
					if msg.Number != "" {
						cb.cfg.PhoneNumber = msg.Number
						cb.number = msg.Number
					}
					if err := cb.cfg.Save(); err != nil {
						slog.Warn("signal: paired -- failed to save config", "error", err)
					} else {
						slog.Info("signal: paired", "number", msg.Number, "config", cb.cfg.Path())
					}
					cb.paired.Store(true)
					cb.pairingCode = ""
					mixer.StopAll()
					mixer.PlayOnce("tone_dial")
					// Restart to reconnect with the assigned phone number
					slog.Info("signal: restarting to register", "number", msg.Number)
					go func() {
						time.Sleep(2 * time.Second) // let dial tone play briefly
						os.Exit(0)                  // systemd will restart us
					}()
				}

			case sigclient.TypeRestart:
				mode := msg.RestartMode
				slog.Info("received restart command", "mode", mode)
				switch mode {
				case "service":
					sendSignal(sig, &sigclient.Message{
						Type:         sigclient.TypeUpdateStatus,
						UpdateStatus: "restarting",
						UpdateDetail: "Service restart requested",
					})
					go func() {
						time.Sleep(500 * time.Millisecond)
						slog.Info("restarting service via exit (systemd will restart)")
						os.Exit(0)
					}()
				case "reboot":
					sendSignal(sig, &sigclient.Message{
						Type:         sigclient.TypeUpdateStatus,
						UpdateStatus: "rebooting",
						UpdateDetail: "Device reboot requested",
					})
					go func() {
						time.Sleep(500 * time.Millisecond)
						slog.Info("rebooting device")
						if err := exec.Command("sudo", "reboot").Run(); err != nil {
							slog.Error("reboot command failed", "err", err)
						}
					}()
				default:
					slog.Warn("unknown restart mode", "mode", mode)
				}

			case sigclient.TypeLineSettings:
				if msg.LineSettings == nil {
					slog.Warn("line_settings message missing payload", "from", msg.From)
					break
				}

				style := msg.LineSettings.VoiceStyle
				if style == "" {
					style = config.VoiceStyleCopper
				}
				cb.mu.Lock()
				currentStyle := cb.cfg.VoiceStyle
				currentSilent := cb.cfg.SilentMode
				cb.mu.Unlock()

				if style != currentStyle {
					slog.Info("line_settings applied", "voice_style", style)
					cb.applyVoiceStyleLive(style)
					if err := cb.setVoiceStyleConfig(style); err != nil {
						slog.Warn("line_settings: voice-style save failed", "err", err)
					}
				}

				silent := msg.LineSettings.SilentMode
				if silent != currentSilent {
					slog.Info("line_settings applied", "silent_mode", silent)
					cb.applySilentModeLive(silent)
					if err := cb.setSilentModeConfig(silent); err != nil {
						slog.Warn("line_settings: silent-mode save failed", "err", err)
					}
				}

				au := msg.LineSettings.AutoUpdate
				if au != cb.autoUpdateEnabled.Load() {
					cb.autoUpdateEnabled.Store(au)
					slog.Info("line_settings applied", "auto_update", au)
					if err := cb.setAutoUpdateConfig(au); err != nil {
						slog.Warn("line_settings: auto-update save failed", "err", err)
					}
				}

			case sigclient.TypeConferenceMember:
				ctrl.HandleConferenceMember(msg.ConfID, msg.Members)
			case sigclient.TypeConferenceConnect:
				ctrl.HandleConferenceConnect(msg.ConfID, msg.Peer, msg.Initiator)
			case sigclient.TypeConferenceLeave:
				ctrl.HandleConferenceLeave(msg.ConfID, msg.Peer, msg.Reason)
			case sigclient.TypeConferenceEnd:
				ctrl.HandleConferenceEnd(msg.ConfID, msg.Reason)
			case sigclient.TypeConferenceRejected:
				ctrl.HandleConferenceRejected(msg.ConfID, msg.Reason)

			default:
				slog.Warn("signal: unhandled message type", "type", msg.Type)
			}

		case <-pairingRefresh.C:
			if !cb.paired.Load() {
				slog.Info("signal: pairing code expiring, reconnecting for fresh code")
				_ = sig.Close()
			}

		case <-sig.Done():
			backoff := 3 * time.Second
			for {
				slog.Info("signal: connection lost, reconnecting", "backoff", backoff)
				time.Sleep(backoff)
				sig = sigclient.NewClient(effectiveServerURL, effectiveNumber, deviceID, cfg.DeviceToken)
				cb.mu.Lock()
				cb.sig = sig
				cb.mu.Unlock()
				if err := sig.Connect(); err != nil {
					slog.Warn("signal: reconnect failed", "error", err)
					if backoff < 60*time.Second {
						backoff *= 2
					}
					continue
				}
				slog.Info("signal: reconnected")
				sendDeviceInfo(sig, fwVersion, fwCommit, flashCapable.Load())
				requestICEServers(sig)
				break
			}
		}
	}
}

// requestICEServers asks signald for STUN/TURN server configs.
func requestICEServers(sig *sigclient.Client) {
	sendSignal(sig, &sigclient.Message{Type: sigclient.TypeRequestICE})
}

func sendDeviceInfo(sig *sigclient.Client, fwVersion, fwCommit string, flashCapable bool) {
	localAddr := primaryLocalAddr()
	if err := sig.Send(&sigclient.Message{
		Type:            sigclient.TypeDeviceInfo,
		PiVersion:       version.Version,
		PiCommit:        version.Commit,
		FirmwareVersion: fwVersion,
		FirmwareCommit:  fwCommit,
		FlashCapable:    flashCapable,
		LocalAddr:       localAddr,
	}); err != nil {
		slog.Warn("device_info: send failed", "error", err)
	} else {
		slog.Info("device_info sent", "pi_version", version.Version, "pi_commit", version.Commit, "fw_version", fwVersion, "fw_commit", fwCommit, "flash_capable", flashCapable, "local_addr", localAddr)
	}
}

// primaryLocalAddr returns the source IP that the OS would use to route to
// the public internet, which is the address the device should self-report
// to the signaling server. Uses a UDP "dial" to a sentinel: no packet is
// actually sent because UDP is connectionless, but the kernel resolves the
// route and assigns a local address. Returns "" when no default route
// exists or the local address cannot be parsed.
func primaryLocalAddr() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr == nil {
		return ""
	}
	return addr.IP.String()
}

// writePCMWav writes mono 16-bit PCM samples to a WAV file. Used by the
// *#TEST# service code to dump the last captured buffer for offline analysis.
func writePCMWav(path string, samples []int16, sampleRate int) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	dataSize := uint32(2 * len(samples))
	byteRate := uint32(sampleRate * 2) // mono * 2 bytes/sample

	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], 36+dataSize)
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(hdr[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(hdr[22:24], 1)  // mono
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(hdr[28:32], byteRate)
	binary.LittleEndian.PutUint16(hdr[32:34], 2)  // block align
	binary.LittleEndian.PutUint16(hdr[34:36], 16) // bits/sample
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], dataSize)

	if _, err := f.Write(hdr); err != nil {
		return err
	}
	payload := make([]byte, 2*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(payload[2*i:2*i+2], uint16(s))
	}
	_, err = f.Write(payload)
	return err
}

// dtmfToneName maps a keypad character to a WAV file name.
func dtmfToneName(key string) string {
	switch key {
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return "dtmf_" + key
	case "*":
		return "dtmf_star"
	case "#":
		return "dtmf_hash"
	default:
		return ""
	}
}
