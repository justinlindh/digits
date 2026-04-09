package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
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
	pairingCode      string   // current pairing code from server
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
		log.Printf("tone: unknown %q", name)
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

func (d *daemonCallbacks) InitiateCall(targetNumber string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Stop tones — mixer continues writing silence (DAC keepalive) until WebRTC audio arrives
	d.mixer.StopTone()

	iceCfg := owebrtc.NewICEConfig(d.iceServers)
	var err error
	d.peerMgr, err = owebrtc.NewPeerManager(iceCfg)
	if err != nil {
		log.Printf("webrtc: %v", err)
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
					log.Printf("makeCall remote track ended after %d frames", frameCount)
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
		log.Printf("webrtc offer: %v", err)
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
		log.Printf("audio pipeline: %v", err)
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

	log.Printf("call initiated to %s", targetNumber)
}

func (d *daemonCallbacks) AnswerCall() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.pendingOffer == "" {
		log.Println("answer: no pending offer")
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
		log.Printf("webrtc (answer): %v", err)
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
			log.Printf("remote track active, waiting for answer...")
			var discarded int
			for {
				d.mu.Lock()
				pip := d.pipeline
				d.mu.Unlock()
				if pip != nil {
					log.Printf("pipeline ready, discarded %d pre-answer packets", discarded)
					break
				}

				pkt, _, err := track.ReadRTP()
				if err != nil {
					log.Printf("remote track ended while waiting for answer (%d packets discarded)", discarded)
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
					log.Printf("remote track ended during drain")
					return
				}
				d.decoder.Decode(pkt.Payload) //nolint:errcheck
				drained++
				lastSeq = pkt.SequenceNumber

				if readTime > 5*time.Millisecond {
					log.Printf("drain complete: %d packets skipped in %s (last_seq=%d)",
						drained-1, time.Since(drainStart).Round(time.Microsecond), lastSeq)
					break
				}
			}

			// Phase 3: Live playback loop — feed decoded PCM into mixer.
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					log.Printf("remote track ended after %d frames", frameCount)
					return
				}
				recvTime := time.Now()
				pcm, err := d.decoder.Decode(pkt.Payload)
				if err != nil {
					log.Printf("decode error: %v (pkt %d bytes)", err, len(pkt.Payload))
					continue
				}
				// Copy — Decode returns a slice of a reused internal buffer
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				frameCount++
				d.mixer.FeedWebRTC(frame)

				if frameCount <= 10 || frameCount%50 == 0 {
					log.Printf("FEED[%d]: seq=%d recv=%s",
						frameCount, pkt.SequenceNumber,
						recvTime.Format("15:04:05.000000"))
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
		log.Printf("webrtc accept offer: %v", err)
		close(sdpSent)
		return
	}

	// Drain any ICE candidates that arrived before peerMgr was ready.
	for _, candidate := range d.pendingICE {
		if err := d.peerMgr.AddICECandidate(candidate); err != nil {
			log.Printf("webrtc add queued ICE candidate: %v", err)
		}
	}
	log.Printf("drained %d queued ICE candidates", len(d.pendingICE))
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
		log.Printf("audio pipeline (answer): %v", err)
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

	log.Printf("answered call from %s", caller)
}

func (d *daemonCallbacks) HangupCall() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.pendingOffer = ""
	d.pendingCaller = ""
	d.pendingICE = nil
	d.callPeer = ""
	d.isCaller = false
	d.isRestartingICE = false
	if d.restartTimer != nil {
		d.restartTimer.Stop()
		d.restartTimer = nil
	}

	d.sig.Send(&sigclient.Message{Type: sigclient.TypeHangup}) //nolint:errcheck

	if d.pipeline != nil {
		d.pipeline.Stop()
		d.pipeline = nil
	}
	if d.peerMgr != nil {
		d.peerMgr.Close()
		d.peerMgr = nil
	}

	log.Println("call ended")
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
			log.Println("webrtc: ICE restart succeeded -- connection recovered")
		}

	case webrtc.PeerConnectionStateFailed:
		d.mu.Lock()
		alreadyRestarting := d.isRestartingICE
		isCaller := d.isCaller
		d.mu.Unlock()

		if alreadyRestarting {
			log.Println("webrtc: ICE restart failed, hanging up")
			go d.ctrl.HandleSignal("hangup")
			return
		}

		if isCaller {
			log.Println("webrtc: connection failed, attempting ICE restart")
			d.attemptICERestart()
		} else {
			log.Println("webrtc: connection failed, waiting for ICE restart from caller")
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
		log.Println("ice-restart: no peer manager, hanging up")
		go d.ctrl.HandleSignal("hangup")
		return
	}

	d.isRestartingICE = true

	offer, err := d.peerMgr.CreateRestartOffer()
	if err != nil {
		log.Printf("ice-restart: create offer failed: %v", err)
		d.isRestartingICE = false
		go d.ctrl.HandleSignal("hangup")
		return
	}

	peer := d.callPeer
	d.startRestartTimeout()

	log.Printf("ice-restart: sending restart offer to %s (%d bytes)", peer, len(offer))
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
			log.Println("webrtc: ICE restart timed out, hanging up")
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
		log.Printf("test: injecting event %q", event)
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

// updateInProgress guards against concurrent firmware update runs.
// The startup update goroutine and server-triggered updates both check this
// to avoid racing (e.g. double-flashing the Pico).
var updateInProgress atomic.Bool

func runUpdate(serverURL, piVersion, fwVersion string, flashCapable bool, reportStatus statusFunc) {
	runTargetedUpdate(serverURL, piVersion, fwVersion, "", "", flashCapable, reportStatus)
}

func runTargetedUpdate(serverURL, piVersion, fwVersion, targetPi, targetFW string, flashCapable bool, reportStatus statusFunc) {
	if !updateInProgress.CompareAndSwap(false, true) {
		log.Println("updater: skipping -- another update is already in progress")
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

	log.Printf("updater: checking for updates (target pi=%q fw=%q)", targetPi, targetFW)
	result, err := up.CheckVersion(targetPi, targetFW)
	if err != nil {
		log.Printf("updater: check failed: %v", err)
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
		log.Println("updater: already up to date")
		reportStatus("up_to_date", "Already running latest version")
		return
	}
	log.Printf("updater: update available -- pi=%v(%s) fw=%v(%s)",
		result.PiAvailable, result.PiVersion,
		result.FWAvailable, result.FWVersion)

	fwSkipped := false
	if result.FWAvailable {
		if !flashCapable {
			log.Println("updater: firmware update available but SWD flash not supported on this device, skipping")
			fwSkipped = true
		} else {
			reportStatus("downloading", "Downloading firmware "+result.FWVersion)
			path, err := up.Download(result.FWURL, "firmware.elf", result.FWSHA256)
			if err != nil {
				log.Printf("updater: firmware download failed: %v", err)
				reportStatus("failed", fmt.Sprintf("Firmware download failed: %v", err))
				return
			}
			reportStatus("applying", "Flashing firmware "+result.FWVersion)
			if err := up.ApplyFirmwareUpdate(path); err != nil {
				log.Printf("updater: firmware apply failed: %v", err)
				reportStatus("failed", fmt.Sprintf("Firmware flash failed: %v", err))
				return
			}
		}
	}
	if result.PiAvailable {
		reportStatus("downloading", "Downloading digitsd "+result.PiVersion)
		path, err := up.Download(result.PiURL, "digitsd-aarch64", result.PiSHA256)
		if err != nil {
			log.Printf("updater: pi download failed: %v", err)
			reportStatus("failed", fmt.Sprintf("Download failed: %v", err))
			return
		}
		reportStatus("rebooting", "Installing digitsd "+result.PiVersion+" -- restarting...")
		if err := up.ApplyPiUpdate(path, result.PiVersion); err != nil {
			log.Printf("updater: pi update failed: %v", err)
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
		log.Println("digitsd: no phone number configured — starting in pairing mode")
	}

	log.Printf("digitsd: server=%s number=%s config=%s", effectiveServerURL, effectiveNumber, *configPath)

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
	}
	if err := extractor.Extract(version.Version); err != nil {
		log.Printf("WARNING: asset extraction failed: %v", err)
	}

	// 1. Open serial port directly (log to both stdout and uart.log file)
	uartLogPath := filepath.Join(filepath.Dir(*socketPath), "uart.log")
	uartLogFile, err := os.OpenFile(uartLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("warning: cannot open uart log %s: %v (logging to stdout only)", uartLogPath, err)
		uartLogFile = nil
	}
	var serialWriter io.Writer = os.Stdout
	if uartLogFile != nil {
		serialWriter = io.MultiWriter(os.Stdout, uartLogFile)
		defer uartLogFile.Close()
	}
	serialLogger := log.New(serialWriter, "", log.Ldate|log.Ltime|log.Lmicroseconds)
	sp, err := phone.OpenSerial(*serialDev, 115200, serialLogger)
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	defer sp.Close()

	// POST: verify Pico is alive
	postRetries := 3
	postOk := false
	for i := 1; i <= postRetries; i++ {
		if err := sp.Ping(); err == nil {
			postOk = true
			break
		}
		log.Printf("POST attempt %d/%d: no PONG", i, postRetries)
		time.Sleep(1 * time.Second)
	}
	if postOk {
		log.Println("POST: PASS — Pico UART healthy")
	} else {
		log.Println("POST: FAIL — Pico not responding.")
		elfPath := defaultFirmwarePath
		swdCfg := defaultSWDConfig
		openocd := defaultOpenOCD
		if _, errElf := os.Stat(elfPath); errElf == nil {
			if _, errOcd := os.Stat(openocd); errOcd == nil {
				log.Printf("POST: attempting auto-flash from %s", elfPath)
				// Close serial port before SWD flash
				sp.Close()
				cmd := exec.Command("sudo", openocd,
					"-f", swdCfg,
					"-f", "target/rp2040.cfg",
					"-c", fmt.Sprintf("program %s verify reset exit", elfPath))
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					log.Printf("POST: auto-flash failed: %v", err)
				} else {
					log.Println("POST: auto-flash succeeded")
				}
				// Re-open serial port
				time.Sleep(2 * time.Second)
				sp, err = phone.OpenSerial(*serialDev, 115200, serialLogger)
				if err != nil {
					log.Fatalf("serial re-open after flash: %v", err)
				}
				if err := sp.Ping(); err == nil {
					log.Println("POST: PASS after auto-flash")
					postOk = true
				} else {
					log.Printf("POST: FAIL after auto-flash: %v", err)
				}
			} else {
				log.Printf("POST: openocd not found at %s", openocd)
			}
		} else {
			log.Printf("POST: no firmware at %s, skipping auto-flash", elfPath)
		}
		if !postOk {
			log.Println("POST: Continuing without Pico. Phone will not function.")
		}
	}

	// Query firmware version (best-effort)
	var fwVersion, fwCommit string
	if postOk {
		if v, c, err := sp.QueryVersion(); err != nil {
			log.Printf("firmware version: %v", err)
		} else {
			fwVersion, fwCommit = v, c
			log.Printf("firmware: version=%s commit=%s", fwVersion, fwCommit)
		}
	}

	// Configure hook inversion for PCB carrier boards
	if postOk && cfg.HookInverted {
		const hookInvertCmd = "HOOK:INVERT:ON"
		var hookOk bool
		for i := 1; i <= 3; i++ {
			resp, err := sp.SendCommand(hookInvertCmd, 2*time.Second)
			if err == nil && resp == hookInvertCmd {
				log.Printf("hook invert: configured (%s)", resp)
				hookOk = true
				break
			}
			log.Printf("hook invert: attempt %d/3 failed (resp=%q err=%v)", i, resp, err)
			time.Sleep(500 * time.Millisecond)
		}
		if !hookOk {
			log.Fatal("hook invert: failed after 3 attempts — refusing to run with wrong hook polarity")
		}
	}

	// 2. Open ALSA playback (direct hardware, no dmix)
	pbDev := *alsaDevice
	if pbDev == "" {
		dev, err := audio.CodecDeviceName()
		if err != nil {
			log.Fatalf("alsa: %v", err)
		}
		pbDev = dev
		log.Printf("alsa playback: auto-detected %s", pbDev)
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
			log.Printf("WARNING: could not load pairing tones: %v", err)
		}
	}
	mixer.Start()
	defer mixer.Stop()

	// Debug: capture raw PCM output if CAPTURE_PCM is set
	if capPath := os.Getenv("CAPTURE_PCM"); capPath != "" {
		if err := mixer.EnableCapture(capPath); err != nil {
			log.Printf("WARNING: PCM capture failed: %v", err)
		} else {
			defer mixer.DisableCapture()
		}
	}

	deviceID, err := config.LoadOrCreateDeviceID()
	if err != nil {
		log.Printf("WARNING: device ID unavailable: %v", err)
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
			log.Printf("volume: %v", err)
		}
		mixer.StopAll()
		time.Sleep(250 * time.Millisecond)
		doubleBeep()
		mixer.PlayLoop("tone_dial")
	})
	svcCodes.SetShutdownCallback(func() {
		log.Println("service code: executing shutdown")
		exec.Command("sudo", "shutdown", "-h", "now").Run()
	})
	svcCodes.SetRebootCallback(func() {
		log.Println("service code: executing reboot")
		exec.Command("sudo", "reboot").Run()
	})
	svcCodes.SetSetupCallback(func() {
		log.Println("service code: *#SETUP# (*#73887#) → removing wifi-configured flag, rebooting")
		err := os.Remove(phone.WifiConfiguredFlag)
		switch {
		case err == nil:
			log.Printf("service code setup: removed %s — Pi will boot into AP mode", phone.WifiConfiguredFlag)
		case os.IsNotExist(err):
			log.Printf("service code setup: %s already absent — Pi will boot into AP mode", phone.WifiConfiguredFlag)
		default:
			log.Printf("service code setup: remove %s: %v", phone.WifiConfiguredFlag, err)
		}
		exec.Command("sudo", "reboot").Run()
	})

	svcCodes.SetAudioTestCallback(func() {
		log.Println("service code: *#TEST# → audio test (record 5s, playback)")
		mixer.StopAll()

		doubleBeep()
		for mixer.OncePlaying() {
			time.Sleep(50 * time.Millisecond)
		}

		// Open capture AFTER beeps so there's no startup delay
		pipCfg := audio.DefaultPipelineConfig()
		pipCfg.Denoise = false // raw mic -- hear exactly what the hardware picks up
		pip := audio.NewPipeline(pipCfg)
		if err := pip.Start(); err != nil {
			log.Printf("audio test: pipeline start: %v", err)
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
		log.Printf("audio test: captured %d samples (%.1fs), peak=%d, playing back", len(recorded), float64(len(recorded))/float64(sampleRate), maxAmp)
		mixer.PlayOnceSamples(recorded)
		time.Sleep(100 * time.Millisecond)
		for mixer.OncePlaying() {
			time.Sleep(100 * time.Millisecond)
		}
		doubleBeep()
		log.Println("audio test: complete")

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
				log.Printf("swd probe: Pico not detected on SWD bus: %v (output: %s)", err, out)
			} else {
				log.Println("swd probe: Pico detected on SWD bus, enabling flash capability")
				flashCapable.Store(true)
			}
			sendDeviceInfo(sig, fwVersion, fwCommit, flashCapable.Load())
		}()
	}

	svcCodes.SetRepairCallback(func() {
		log.Println("service code: *#0* → clearing device token, rebooting into pairing mode")
		if cfg != nil {
			cfg.DeviceToken = ""
			cfg.PairingCode = ""
			if err := cfg.Save(); err != nil {
				log.Printf("service code repair: save config: %v", err)
			}
		}
		log.Println("service code repair: rebooting")
		exec.Command("sudo", "reboot").Run()
	})

	svcCodes.SetUpdateCallback(func() {
		log.Println("service code: *#UPDATE# (*#873283#) — checking for updates")
		go runUpdate(effectiveServerURL, version.Version, fwVersion, flashCapable.Load(), nil)
	})

	svcCodes.SetFactoryResetCallback(func() {
		log.Println("service code: *#00000# -> FACTORY RESET")
		if err := bootcount.SetThreshold(bootcount.DefaultPath, 3); err != nil {
			log.Printf("factory reset: failed to set boot counter: %v", err)
		}
		log.Println("factory reset: rebooting into recovery")
		exec.Command("sudo", "reboot").Run()
	})

	// 6b. Create easter egg detector
	easterEggs := phone.NewEasterEggDetector([]phone.EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}, func(clip string) {
		log.Printf("phone: playing easter egg clip: %s", clip)
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
	log.Println("digitsd ready")

	// Start hardware watchdog (if available)
	if wd, err := watchdog.Open("/dev/watchdog"); err == nil {
		wd.Start(5 * time.Second)
		defer wd.Close()
		log.Println("watchdog: started (5s interval)")
	} else {
		log.Printf("watchdog: not available: %v", err)
	}

	// Clear boot counter (we're healthy)
	if err := bootcount.Clear(bootcount.DefaultPath); err != nil {
		log.Printf("WARNING: failed to clear boot counter: %v", err)
	} else {
		log.Println("boot counter: cleared (healthy boot)")
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

	// Check for updates on startup (non-blocking)
	go func() {
		time.Sleep(10 * time.Second) // let things settle
		runUpdate(effectiveServerURL, version.Version, fwVersion, flashCapable.Load(), nil)
	}()

	// OS signal handling
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Main select loop
	for {
		select {
		case <-quit:
			log.Println("digitsd shutting down")
			mixer.Stop()
			sig.Close()
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
				mixer.PlayOnce("pairing_expires")
				log.Printf("phone: playing pairing code %s via voice", cb.pairingCode)
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
					log.Printf("phone: dial easter egg: %s (%s)", egg.Name, number)
					mixer.StopTone()
					mixer.PlayOnce(egg.Clip)
					continue // don't place the call
				}
			}

			// Pico rebooted (e.g. external flash, power cycle): re-query
			// firmware version and report it to the server.
			if event == "STATUS:READY" {
				log.Println("pico: detected reboot, re-querying firmware version")
				if v, c, err := sp.QueryVersion(); err != nil {
					log.Printf("pico: version query after reboot: %v", err)
				} else if v != fwVersion || c != fwCommit {
					fwVersion, fwCommit = v, c
					log.Printf("pico: firmware version changed: %s (%s)", fwVersion, fwCommit)
					sendDeviceInfo(sig, fwVersion, fwCommit, flashCapable.Load())
				} else {
					log.Printf("pico: firmware version unchanged: %s (%s)", fwVersion, fwCommit)
				}
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

		case msg := <-sig.Inbox():
			log.Printf("signal rx: type=%s from=%s", msg.Type, msg.From)
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
						log.Printf("webrtc set answer: %v", err)
					} else {
						log.Printf("webrtc: set remote answer from %s (%d bytes)", msg.From, len(msg.SDP))
					}
				}
				cb.mu.Unlock()
				ctrl.HandleSignal("answer")
			case sigclient.TypeHangup:
				ctrl.HandleSignal("hangup")
			case sigclient.TypeBusy:
				ctrl.HandleSignal("busy")
			case sigclient.TypeError:
				log.Printf("signal error: %s", msg.Error)
				// Number not reachable — emulate real phone: ringback → SIT → busy
				go func() {
					// 1. Brief silence (call setup delay, ~1s)
					time.Sleep(1 * time.Second)
					if ctrl.State() != phone.StateCALLING {
						return
					}
					// 2. Ringback for ~8s (simulates 1-2 rings)
					log.Println("playing ringback (number unreachable)")
					mixer.PlayLoop("tone_ringback")
					time.Sleep(8 * time.Second)
					if ctrl.State() != phone.StateCALLING {
						return
					}
					// 3. SIT tones + "number not in service" announcement
					log.Println("playing disconnected announcement")
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
					log.Println("playing reorder tone")
					mixer.PlayLoop("tone_busy")
				}()
			case sigclient.TypeSDP:
				cb.mu.Lock()
				if cb.peerMgr != nil {
					if err := cb.peerMgr.SetAnswer(msg.SDP); err != nil {
						log.Printf("webrtc set answer: %v", err)
					}
				} else {
					cb.pendingOffer = msg.SDP
					// Also capture caller from SDP message if not already set by ring
					if cb.pendingCaller == "" && msg.From != "" {
						cb.pendingCaller = msg.From
						log.Printf("set pendingCaller from SDP: %s", msg.From)
					}
					log.Printf("stored pending SDP offer from %s (%d bytes)", msg.From, len(msg.SDP))
				}
				cb.mu.Unlock()
			case sigclient.TypeICE:
				cb.mu.Lock()
				if cb.peerMgr != nil {
					if err := cb.peerMgr.AddICECandidate(msg.Candidate); err != nil {
						log.Printf("webrtc add ICE candidate: %v", err)
					}
				} else {
					// Queue ICE candidates until peerMgr is ready (e.g. during RINGING before answer)
					cb.pendingICE = append(cb.pendingICE, msg.Candidate)
					log.Printf("queued ICE candidate (peerMgr not ready, total queued: %d)", len(cb.pendingICE))
				}
				cb.mu.Unlock()
			case sigclient.TypeUpdateTrigger:
				log.Printf("signal: received update trigger from server (target_pi=%q target_fw=%q)",
					msg.TargetPiVersion, msg.TargetFWVersion)
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
				log.Println("factory reset: triggered by server")
				go func() {
					if err := bootcount.SetThreshold(bootcount.DefaultPath, 3); err != nil {
						log.Printf("factory reset: failed to set boot counter: %v", err)
						return
					}
					log.Println("factory reset: boot counter set to 3, rebooting")
					exec.Command("sudo", "reboot").Run()
				}()

			case sigclient.TypeICERestart:
				cb.mu.Lock()
				pm := cb.peerMgr
				peer := cb.callPeer
				cb.mu.Unlock()
				if pm == nil {
					log.Println("ice-restart: no active peer connection, ignoring")
					break
				}
				log.Printf("ice-restart: received restart offer from %s (%d bytes)", msg.From, len(msg.SDP))
				answerSDP, err := pm.AcceptOffer(msg.SDP)
				if err != nil {
					log.Printf("ice-restart: accept offer failed: %v", err)
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
				log.Printf("ice-restart: sending restart answer to %s (%d bytes)", peer, len(answerSDP))
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
				log.Printf("ice: cached %d server(s) from signald", len(msg.Servers))

			case sigclient.TypePairingCode:
				cb.pairingCode = msg.PairingCode
				
				log.Printf("PAIRING REQUIRED: code %q — pick up handset to hear it",
					msg.PairingCode)

			case sigclient.TypePaired:
				if msg.DeviceToken != "" && cb.cfg != nil {
					cb.cfg.DeviceToken = msg.DeviceToken
					cb.cfg.PairingCode = ""
					if msg.Number != "" {
						cb.cfg.PhoneNumber = msg.Number
						cb.number = msg.Number
					}
					if err := cb.cfg.Save(); err != nil {
						log.Printf("signal: paired — failed to save config: %v", err)
					} else {
						log.Printf("signal: paired as %s — config saved to %s", msg.Number, cb.cfg.Path())
					}
					cb.paired.Store(true)
					cb.pairingCode = ""
					mixer.StopAll()
					mixer.PlayOnce("tone_dial")
					// Restart to reconnect with the assigned phone number
					log.Printf("signal: restarting to register as %s", msg.Number)
					go func() {
						time.Sleep(2 * time.Second) // let dial tone play briefly
						os.Exit(0)                  // systemd will restart us
					}()
				}

			default:
				log.Printf("signal: unhandled message type %q", msg.Type)
			}

		case <-sig.Done():
			backoff := 3 * time.Second
			for {
				log.Printf("signal: connection lost, reconnecting in %s...", backoff)
				time.Sleep(backoff)
				sig = sigclient.NewClient(effectiveServerURL, effectiveNumber, deviceID, cfg.DeviceToken)
				cb.mu.Lock()
				cb.sig = sig
				cb.mu.Unlock()
				if err := sig.Connect(); err != nil {
					log.Printf("signal: reconnect failed: %v", err)
					if backoff < 60*time.Second {
						backoff *= 2
					}
					continue
				}
				log.Println("signal: reconnected")
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
		log.Printf("ice: request failed: %v", err)
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
		log.Printf("device_info: send failed: %v", err)
	} else {
		log.Printf("device_info: pi=%s(%s) fw=%s(%s) flash_capable=%v", version.Version, version.Commit, fwVersion, fwCommit, flashCapable)
	}
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
