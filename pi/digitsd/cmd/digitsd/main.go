package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/assets"
	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	"github.com/justinlindh/digits/pi/digitsd/internal/bootcount"
	"github.com/justinlindh/digits/pi/digitsd/internal/codec"
	"github.com/justinlindh/digits/pi/digitsd/internal/config"
	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	"github.com/justinlindh/digits/pi/digitsd/internal/updater"
	"github.com/justinlindh/digits/pi/digitsd/internal/version"
	"github.com/justinlindh/digits/pi/digitsd/internal/watchdog"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// iceRestartTimeout is how long to wait for an ICE restart to succeed
// before giving up and hanging up the call.
const iceRestartTimeout = 15 * time.Second

// pairingRefreshInterval is how often an unpaired device reconnects to
// obtain a fresh pairing code. Must be shorter than the server-side
// CodeTTL (10 min) so the code is refreshed before it expires.
const pairingRefreshInterval = 9 * time.Minute

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
	pipeline         *audio.Pipeline
	encoder          *codec.Encoder
	decoder          *codec.Decoder
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
}

// --- phone.Callbacks implementation ---

func (d *daemonCallbacks) SendTone(name string) {
	// Map controller tone names to WAV file names.
	switch strings.ToUpper(name) {
	case "DIAL":
		d.mixer.PlayLoop("tone_dial")
	case "RINGBACK":
		d.mixer.PlayLoop("tone_ringback")
	case "BUSY":
		d.mixer.PlayLoop("tone_busy")
	case "REORDER":
		d.mixer.PlayLoop("tone_reorder")
	case "HOWLER":
		d.mixer.PlayLoop("tone_howler")
	case "INTERCEPT":
		d.mixer.PlayOnce("intercept")
	case "STOP":
		d.mixer.StopTone()
	case "STOPALL":
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

func (d *daemonCallbacks) InitiateCall(targetNumber string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	iceCfg := owebrtc.NewICEConfig(d.iceServers)
	var err error
	d.peerMgr, err = owebrtc.NewPeerManager(iceCfg)
	if err != nil {
		slog.Error("webrtc: new peer manager failed", "error", err)
		return
	}

	d.callPeer = targetNumber
	d.isCaller = true
	d.isRestartingICE = false

	d.peerMgr.OnConnectionState = func(state webrtc.PeerConnectionState) {
		d.handleConnectionStateChange(state)
	}

	// Handle remote audio track
	d.peerMgr.OnRemoteTrack = func(track *webrtc.TrackRemote) {
		go func() {
			// Live playback — decode and feed into mixer
			var frameCount int
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					slog.Info("makeCall remote track ended", "frames", frameCount)
					return
				}
				pcm, err := d.decoder.Decode(pkt.Payload)
				if err != nil {
					continue
				}
				// Copy — Decode returns a slice of a reused internal buffer
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				frameCount++
				d.mixer.FeedWebRTC(frame)
			}
		}()
	}

	// Gate ICE candidates behind SDP send — candidates must not arrive before the offer.
	sdpSent := make(chan struct{})
	d.peerMgr.OnICECandidate = func(candidate string) {
		<-sdpSent
		d.sig.Send(&sigclient.Message{ //nolint:errcheck
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
		return
	}

	// Send call + SDP, then ungate ICE candidates
	d.sig.Send(&sigclient.Message{Type: sigclient.TypeCall, To: targetNumber}) //nolint:errcheck
	d.sig.Send(&sigclient.Message{Type: sigclient.TypeSDP, To: targetNumber, SDP: offer}) //nolint:errcheck
	close(sdpSent)

	// Start audio pipeline
	d.pipeline = audio.NewPipeline(audio.DefaultPipelineConfig())
	if err := d.pipeline.Start(); err != nil {
		slog.Error("audio pipeline start failed", "error", err)
		return
	}

	// Encode and send captured audio
	go func() {
		for frame := range d.pipeline.OutFrames() {
			encoded, err := d.encoder.Encode(frame)
			if err != nil {
				continue
			}
			d.mu.Lock()
			pm := d.peerMgr
			d.mu.Unlock()
			if pm != nil {
				pm.LocalTrack().WriteSample(media.Sample{ //nolint:errcheck
					Data:     encoded,
					Duration: 20 * time.Millisecond,
				})
			}
		}
	}()

	slog.Info("call initiated", "target", targetNumber)
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

	d.peerMgr.OnConnectionState = func(state webrtc.PeerConnectionState) {
		d.handleConnectionStateChange(state)
	}

	// Handle remote audio track — decode and feed into mixer.
	d.peerMgr.OnRemoteTrack = func(track *webrtc.TrackRemote) {
		go func() {
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
				d.decoder.Decode(pkt.Payload) //nolint:errcheck
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
				d.decoder.Decode(pkt.Payload) //nolint:errcheck
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
				pcm, err := d.decoder.Decode(pkt.Payload)
				if err != nil {
					slog.Warn("decode error", "error", err, "pkt_bytes", len(pkt.Payload))
					continue
				}
				// Copy — Decode returns a slice of a reused internal buffer
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				frameCount++
				d.mixer.FeedWebRTC(frame)

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
		d.sig.Send(&sigclient.Message{ //nolint:errcheck
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
	d.sig.Send(&sigclient.Message{ //nolint:errcheck
		Type: sigclient.TypeAnswer,
		To:   caller,
		SDP:  answerSDP,
	})
	close(sdpSent)

	// Start audio pipeline (capture only — playback goes through mixer)
	d.pipeline = audio.NewPipeline(audio.DefaultPipelineConfig())
	if err := d.pipeline.Start(); err != nil {
		slog.Error("audio pipeline (answer) start failed", "error", err)
		return
	}

	// Encode and send captured audio
	go func() {
		for frame := range d.pipeline.OutFrames() {
			encoded, err := d.encoder.Encode(frame)
			if err != nil {
				continue
			}
			d.mu.Lock()
			pm := d.peerMgr
			d.mu.Unlock()
			if pm != nil {
				pm.LocalTrack().WriteSample(media.Sample{ //nolint:errcheck
					Data:     encoded,
					Duration: 20 * time.Millisecond,
				})
			}
		}
	}()

	slog.Info("answered call", "caller", caller)
}

func (d *daemonCallbacks) HangupCall() {
	d.mu.Lock()
	defer d.mu.Unlock()

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

	d.sig.Send(&sigclient.Message{Type: sigclient.TypeHangup, To: peer}) //nolint:errcheck

	if d.pipeline != nil {
		d.pipeline.Stop()
		d.pipeline = nil
	}
	if d.peerMgr != nil {
		if err := d.peerMgr.Close(); err != nil {
			slog.Warn("peerMgr close failed", "error", err)
		}
		d.peerMgr = nil
	}

	slog.Info("call ended")
}

// handleConnectionStateChange is called (without d.mu held) from a pion
// goroutine when the WebRTC peer connection state changes.  On transient
// failures the original caller attempts a single ICE restart before giving up.
func (d *daemonCallbacks) handleConnectionStateChange(state webrtc.PeerConnectionState) {
	switch state {
	case webrtc.PeerConnectionStateConnected:
		d.mu.Lock()
		wasRestarting := d.isRestartingICE
		d.isRestartingICE = false
		if d.restartTimer != nil {
			d.restartTimer.Stop()
			d.restartTimer = nil
		}
		d.mu.Unlock()
		if wasRestarting {
			slog.Info("webrtc: ICE restart succeeded -- connection recovered")
		}

	case webrtc.PeerConnectionStateFailed:
		d.mu.Lock()
		alreadyRestarting := d.isRestartingICE
		isCaller := d.isCaller
		d.mu.Unlock()

		if alreadyRestarting {
			slog.Warn("webrtc: ICE restart failed, hanging up")
			go d.ctrl.HandleSignal("hangup")
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
		go d.ctrl.HandleSignal("hangup")
		return
	}

	d.isRestartingICE = true

	offer, err := d.peerMgr.CreateRestartOffer()
	if err != nil {
		slog.Error("ice-restart: create offer failed", "error", err)
		d.isRestartingICE = false
		go d.ctrl.HandleSignal("hangup")
		return
	}

	peer := d.callPeer
	d.startRestartTimeout()

	slog.Info("ice-restart: sending restart offer", "peer", peer, "bytes", len(offer))
	d.sig.Send(&sigclient.Message{ //nolint:errcheck
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
			d.ctrl.HandleSignal("hangup")
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
			d.ctrl.HandleEvent(event)
		}
		return "OK"
	}

	// Intercept tone commands (handled locally)
	if strings.HasPrefix(cmd, "TONE:") {
		tone := cmd[5:]
		switch tone {
		case "STOP":
			d.mixer.StopTone()
		case "DIAL":
			d.mixer.PlayLoop("tone_dial")
		case "RINGBACK":
			d.mixer.PlayLoop("tone_ringback")
		case "BUSY":
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

// Default paths for SWD flash infrastructure on the Pi.
const (
	defaultFirmwarePath = "/data/digits/firmware.elf"
	defaultSWDConfig    = "/usr/local/share/digits/swd/digits-swd.cfg"
	defaultOpenOCD      = "/usr/bin/openocd"
)

// statusFunc is a callback to report update progress back to the server.
type statusFunc func(status, detail string)

func triggerFactoryReset() {
	if err := bootcount.SetThreshold(bootcount.DefaultPath, 3); err != nil {
		slog.Error("factory reset: failed to set boot counter", "err", err)
		return
	}
	slog.Info("factory reset: boot counter set to 3, rebooting")
	_ = exec.Command("sudo", "reboot").Run()
}

// updateInProgress guards against concurrent firmware update runs.
// The startup update goroutine and server-triggered updates both check this
// to avoid racing (e.g. double-flashing the Pico).
var updateInProgress atomic.Bool


func runTargetedUpdate(serverURL, piVersion, fwVersion, targetPi, targetFW string, flashCapable bool, reportStatus statusFunc) {
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
		elfPath := defaultFirmwarePath
		swdCfg := defaultSWDConfig
		openocd := defaultOpenOCD
		if _, errElf := os.Stat(elfPath); errElf == nil {
			if _, errOcd := os.Stat(openocd); errOcd == nil {
				slog.Info("POST: attempting auto-flash", "path", elfPath)
				// Close serial port before SWD flash
				if err := sp.Close(); err != nil {
					slog.Warn("POST: close serial failed", "error", err)
				}
				cmd := exec.Command("sudo", openocd,
					"-f", swdCfg,
					"-f", "target/rp2040.cfg",
					"-c", "rp2040.core0 configure -event reset-init {}",
					"-c", fmt.Sprintf("program %s verify", elfPath),
					"-c", "reset run",
					"-c", "exit")
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					slog.Error("POST: auto-flash failed", "error", err)
				} else {
					slog.Info("POST: auto-flash succeeded")
				}
				// Re-open serial port
				time.Sleep(2 * time.Second)
				sp, err = phone.OpenSerial(*serialDev, 115200, serialLogger)
				if err != nil {
					log.Fatalf("serial re-open after flash: %v", err)
				}
				if err := sp.Ping(); err == nil {
					slog.Info("POST: PASS after auto-flash")
					postOk = true
				} else {
					slog.Warn("POST: FAIL after auto-flash", "error", err)
				}
			} else {
				slog.Warn("POST: openocd not found", "path", openocd)
			}
		} else {
			slog.Info("POST: no firmware at path, skipping auto-flash", "path", elfPath)
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

	// Configure hook inversion for PCB carrier boards
	if postOk && cfg.HookInverted {
		const hookInvertCmd = "HOOK:INVERT:ON"
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
			log.Fatal("hook invert: failed after 3 attempts — refusing to run with wrong hook polarity")
		}
	}

	// 2. Open ALSA playback (direct hardware, no dmix)
	pbDev := *alsaDevice
	if pbDev == "" {
		pbDev = audio.CodecDeviceName()
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

	// 5. Create Opus encoder and decoder
	enc, err := codec.NewEncoder(48000, 1, 24000)
	if err != nil {
		log.Fatalf("codec encoder: %v", err)
	}
	dec, err := codec.NewDecoder(48000, 1)
	if err != nil {
		log.Fatalf("codec decoder: %v", err)
	}

	// 6. Create service code handler
	// doubleBeep plays two short DTMF star tones as an audible confirmation.
	doubleBeep := func() {
		mixer.PlayOnce("dtmf_star")
		time.Sleep(150 * time.Millisecond)
		mixer.PlayOnce("dtmf_star")
		time.Sleep(300 * time.Millisecond)
	}

	svcCodes := phone.NewServiceCodeHandler()
	svcCodes.SetVolumeCallback(func(level int) {
		if err := phone.SetVolume(level); err != nil {
			slog.Warn("volume set failed", "error", err)
		}
		mixer.StopAll()
		time.Sleep(250 * time.Millisecond)
		doubleBeep()
		mixer.PlayLoop("tone_dial")
	})
	svcCodes.SetShutdownCallback(func() {
		slog.Info("service code: executing shutdown")
		_ = exec.Command("sudo", "shutdown", "-h", "now").Run()
	})
	svcCodes.SetRebootCallback(func() {
		slog.Info("service code: executing reboot")
		_ = exec.Command("sudo", "reboot").Run()
	})
	svcCodes.SetSetupCallback(func() {
		slog.Info("service code: *#SETUP# (*#73887#) -> removing wifi-configured flag, rebooting")
		err := os.Remove(phone.WifiConfiguredFlag)
		switch {
		case err == nil:
			slog.Info("service code setup: removed wifi flag -- Pi will boot into AP mode", "path", phone.WifiConfiguredFlag)
		case os.IsNotExist(err):
			slog.Info("service code setup: wifi flag already absent -- Pi will boot into AP mode", "path", phone.WifiConfiguredFlag)
		default:
			slog.Warn("service code setup: remove wifi flag failed", "path", phone.WifiConfiguredFlag, "error", err)
		}
		_ = exec.Command("sudo", "reboot").Run()
	})

	svcCodes.SetAudioTestCallback(func() {
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
	})

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

	svcCodes.SetRepairCallback(func() {
		slog.Info("service code: *#0* -> clearing device token, rebooting into pairing mode")
		if cfg != nil {
			cfg.DeviceToken = ""
			cfg.PairingCode = ""
			if err := cfg.Save(); err != nil {
				slog.Warn("service code repair: save config failed", "error", err)
			}
		}
		slog.Info("service code repair: rebooting")
		_ = exec.Command("sudo", "reboot").Run()
	})

	svcCodes.SetUpdateCallback(func() {
		slog.Info("service code: *#UPDATE# (*#873283#) -- checking for updates")
		go runTargetedUpdate(effectiveServerURL, version.Version, fwVersion, "", "", flashCapable.Load(), nil)
	})

	svcCodes.SetFactoryResetCallback(func() {
		slog.Info("service code: *#00000# -> FACTORY RESET")
		triggerFactoryReset()
	})

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
	cb := &daemonCallbacks{
		serial:       sp,
		sig:          sig,
		mixer:        mixer,
		serviceCodes: svcCodes,
		encoder:      enc,
		decoder:      dec,
		number:       effectiveNumber,
		cfg:          cfg,
		debugMode:    os.Getenv("DIGITS_DEBUG") == "1",
	}
	if cfg.DeviceToken != "" {
		cb.paired.Store(true)
	}

	// 8. Create phone Controller
	ctrl := phone.NewController(cb, effectiveNumber)
	cb.ctrl = ctrl

	// 9. Start socket server (backward compat for debugging + latclient auto-answer)
	sockSrv, err := phone.NewSocketServer(*socketPath, cb)
	if err != nil {
		log.Fatalf("socket server: %v", err)
	}
	defer sockSrv.Close()

	// 10. Connect signaling client
	if err := sig.Connect(); err != nil {
		log.Fatalf("signald connect: %v", err)
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

	// Clear boot counter (we're healthy)
	if err := bootcount.Clear(bootcount.DefaultPath); err != nil {
		slog.Warn("failed to clear boot counter", "err", err)
	} else {
		slog.Info("boot counter: cleared (healthy boot)")
	}

	sendDeviceInfo(sig, fwVersion, fwCommit, flashCapable.Load())
	requestICEServers(sig)

	// Refresh ICE credentials periodically (TURN creds are time-limited)
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			requestICEServers(sig)
		}
	}()

	// Pairing code refresh: reconnect before the code expires so the
	// server issues a fresh one. Timer starts when we receive a code.
	pairingRefresh := time.NewTimer(0)
	if !pairingRefresh.Stop() {
		<-pairingRefresh.C
	}

	// OS signal handling
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Post-reboot firmware version results. STATUS:READY spawns a goroutine
	// that retries QueryVersion (the Pico's UART command loop isn't quite
	// awake yet at the instant it emits STATUS:READY). The goroutine sends
	// the result here so the main loop can update fwVersion/fwCommit without
	// any shared-state synchronization.
	type fwVersionResult struct{ version, commit string }
	fwVersionCh := make(chan fwVersionResult, 1)

	// Main select loop
	for {
		select {
		case <-quit:
			slog.Info("digitsd shutting down")
			mixer.Stop()
			if err := sig.Close(); err != nil {
				slog.Warn("sig close failed", "error", err)
			}
			return

		case event := <-sp.Events():
			// Unpaired phone: play pairing voice sequence instead of dial tone
			if event == "HOOK:OFF" && !cb.paired.Load() && cb.pairingCode != "" {
				mixer.StopAll()
				mixer.PlayOnce("pairing_silence")
				mixer.PlayOnce("pairing_welcome")
				for _, ch := range cb.pairingCode {
					mixer.PlayOnce("spoken_" + string(ch))
				}
				minutesLeft := int(math.Ceil(pairingRefreshInterval.Minutes() - time.Since(cb.pairingCodeReceivedAt).Minutes()))
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
				slog.Info("phone: playing pairing code via voice", "code", cb.pairingCode)
				sp.LED("ON")
				continue // skip normal controller handling
			}

			// Handle DTMF tone playback for key presses
			if strings.HasPrefix(event, "KEY:") && len(event) > 4 {
				key := string(event[4])
				dtmfName := dtmfToneName(key)
				if dtmfName != "" {
					// Stop dial tone on first key
					if mixer.Active() == "tone_dial" {
						mixer.StopTone()
					}
					mixer.PlayOnce(dtmfName)
				}
				// Forward DTMF to the remote peer if a call is connected.
				if ctrl.State() == phone.StateCONNECTED {
					cb.mu.Lock()
					peer := cb.callPeer
					cb.mu.Unlock()
					if peer != "" {
						sig.Send(&sigclient.Message{ //nolint:errcheck
							Type:  sigclient.TypeDTMF,
							To:    peer,
							Digit: key,
						})
					}
				}
				// Check easter eggs, then service codes
				if !easterEggs.AddKey(key) {
					if svcCodes.AddKey(key) {
						ctrl.Reset()
						continue // skip forwarding to controller
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
				go func() {
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
				continue
			}

			// Forward all events to the FSM controller
			ctrl.HandleEvent(event)

			// Hang-up: kill ALL audio immediately
			if event == "HOOK:ON" {
				mixer.StopAll()
				easterEggs.Reset()
				svcCodes.Reset()
				svcCodes.Reset()
			}

		case r := <-fwVersionCh:
			if r.version != fwVersion || r.commit != fwCommit {
				fwVersion, fwCommit = r.version, r.commit
				slog.Info("pico: firmware version changed", "version", fwVersion, "commit", fwCommit)
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
				ctrl.HandleSignal("ring")
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
				ctrl.HandleSignal("answer")
			case sigclient.TypeHangup:
				ctrl.HandleSignal("hangup")
			case sigclient.TypeBusy:
				ctrl.HandleSignal("busy")
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
				// Number not reachable -- emulate real phone: ringback -> SIT -> busy
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
				cb.mu.Lock()
				if cb.peerMgr != nil {
					if err := cb.peerMgr.SetAnswer(msg.SDP); err != nil {
						slog.Error("webrtc: set answer failed", "error", err)
					}
				} else {
					cb.pendingOffer = msg.SDP
					// Also capture caller from SDP message if not already set by ring
					if cb.pendingCaller == "" && msg.From != "" {
						cb.pendingCaller = msg.From
						slog.Info("set pendingCaller from SDP", "from", msg.From)
					}
					slog.Info("stored pending SDP offer", "from", msg.From, "bytes", len(msg.SDP))
				}
				cb.mu.Unlock()
			case sigclient.TypeICE:
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
					sig.Send(&sigclient.Message{ //nolint:errcheck
						Type:         sigclient.TypeUpdateStatus,
						UpdateStatus: status,
						UpdateDetail: detail,
					})
				}
				go runTargetedUpdate(effectiveServerURL, version.Version, fwVersion,
					msg.TargetPiVersion, msg.TargetFWVersion, flashCapable.Load(), statusReporter)

			case sigclient.TypeFactoryReset:
				slog.Info("factory reset: triggered by server")
				go triggerFactoryReset()

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
				sig.Send(&sigclient.Message{ //nolint:errcheck
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
					sig.Send(&sigclient.Message{ //nolint:errcheck
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
					sig.Send(&sigclient.Message{ //nolint:errcheck
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
	if err := sig.Send(&sigclient.Message{Type: sigclient.TypeRequestICE}); err != nil {
		slog.Warn("ice: request failed", "error", err)
	}
}

func sendDeviceInfo(sig *sigclient.Client, fwVersion, fwCommit string, flashCapable bool) {
	if err := sig.Send(&sigclient.Message{
		Type:            sigclient.TypeDeviceInfo,
		PiVersion:       version.Version,
		PiCommit:        version.Commit,
		FirmwareVersion: fwVersion,
		FirmwareCommit:  fwCommit,
		FlashCapable:    flashCapable,
	}); err != nil {
		slog.Warn("device_info: send failed", "error", err)
	} else {
		slog.Info("device_info sent", "pi_version", version.Version, "pi_commit", version.Commit, "fw_version", fwVersion, "fw_commit", fwCommit, "flash_capable", flashCapable)
	}
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
