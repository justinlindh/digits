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
	"github.com/justinlindh/digits/pi/digitsd/internal/codec"
	"github.com/justinlindh/digits/pi/digitsd/internal/config"
	"github.com/justinlindh/digits/pi/digitsd/internal/contacts"
	"github.com/justinlindh/digits/pi/digitsd/internal/devmode"
	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	"github.com/justinlindh/digits/pi/digitsd/internal/subsystem"
	"github.com/justinlindh/digits/pi/digitsd/internal/updater"
	"github.com/justinlindh/digits/pi/digitsd/internal/version"
	"github.com/justinlindh/digits/pi/digitsd/internal/voicemail"
	"github.com/justinlindh/digits/pi/digitsd/internal/watchdog"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
	"github.com/justinlindh/digits/pi/digitsd/internal/wififallback"
)

// iceRestartTimeout is how long to wait for ICE recovery to succeed before
// giving up and hanging up the call. It MUST exceed the server-side grace
// window (20s in server/internal/signaling/relay.go) plus margin, so a peer
// that stays connected does not give up before a dropped phone can reconnect
// its WebSocket and re-establish media within that window.
const iceRestartTimeout = 25 * time.Second

// disconnectDebounce is how long the daemon waits after pion reports the
// peer connection Disconnected before proactively driving ICE recovery.
// pion can recover a transient blip on its own (STUN consent / connectivity
// checks) within this window, in which case no restart is needed.
const disconnectDebounce = 4 * time.Second

// pairingRefreshInterval is the fallback reconnect cadence for obtaining a
// fresh pairing code, used only when the server does not report a TTL. When
// the server sends PairingCodeTTL (it does), the device instead refreshes
// pairingRefreshMargin before the reported expiry.
const pairingRefreshInterval = 9 * time.Minute

// pairingRefreshMargin is how far ahead of the server-reported code expiry the
// device reconnects for a fresh code, so the announced code is still valid
// while a user is typing it into the web UI.
const pairingRefreshMargin = 60 * time.Second

// pairingAnnouncementInterval is how long to wait between repeats of the
// spoken pairing-code sequence while the phone is unpaired and off-hook.
// Short enough that a user who missed the digits can re-hear them without
// hanging up; long enough to avoid talking over a listener who got it the
// first time.
const pairingAnnouncementInterval = 15 * time.Second

// activeCallReconnectBackoff is the fixed wait between WebSocket reconnect
// attempts when a call is active. Short so the caller side re-establishes
// signaling quickly enough for tryResumeAfterReconnect to send the ICE-restart
// offer within the peer's 25s recovery window.
const activeCallReconnectBackoff = 2 * time.Second

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
}

var (
	configPath  = flag.String("config", config.DefaultPath, "path to JSON config file")
	signaldURL  = flag.String("signald", "", "signald WebSocket URL (overrides config file)")
	numberFlag  = flag.String("number", "", "this phone's number, e.g. 3140001 (overrides config file)")
	serialDev   = flag.String("serial", "/dev/serial0", "serial port device")
	socketPath  = flag.String("socket", "/home/digits/digits/pi/uart.sock", "UART command socket path")
	toneDir     = flag.String("tones", "/data/digits/tones", "directory containing WAV tone files")
	alsaDevice  = flag.String("alsa-playback", "", "ALSA playback device (auto-detects Codec Zero if empty)")
	showVersion = flag.Bool("version", false, "print version and exit")
	modeFlag    = flag.String("mode", "normal", "operating mode: normal, recovery, setup, gpclk0 (diagnostic)")
)

// daemonCallbacks implements phone.Callbacks and wires hardware + WebRTC.
type daemonCallbacks struct {
	serial       *phone.SerialPort
	sig          *sigclient.Client
	mixer        *audio.Mixer
	serviceCodes *phone.ServiceCodeHandler
	ctrl         *phone.Controller
	// ctrlSignal is the controller seen by the signaling dispatch. It points
	// at ctrl in the running daemon; tests inject a recording fake to verify
	// routing without a real FSM, serial port, or audio path.
	ctrlSignal    signalController
	mu            sync.Mutex
	peerMgr       *owebrtc.PeerManager
	mesh          *owebrtc.MeshManager // conference-only peer pool; 2-party calls use peerMgr
	pipeline      *audio.Pipeline
	number        string
	cfg           *config.Config
	pendingOffer  string
	pendingCaller string
	pendingICE    []string // ICE candidates received before peerMgr is created
	// preAnswer holds a PeerConnection created during the ring phase to
	// reduce call-answer latency. Promoted into the active call state on
	// HOOK:OFF; torn down if the caller hangs up before we answer.
	preAnswer struct {
		peerMgr    *owebrtc.PeerManager
		answerSDP  string
		webrtcCh   chan []int16
		candidates []string // local ICE candidates gathered during ring, sent on pickup
		caller     string   // pendingCaller at time of preparation
	}
	iceServers           []owebrtc.ICEServerConfig // cached STUN/TURN servers from signald
	debugMode            bool                      // read from DIGITS_DEBUG env at startup
	paired               atomic.Bool
	pairingCode          string      // current pairing code from server
	pairingCodeExpiresAt time.Time   // server-reported expiry of the current pairing code
	callPeer             string      // number of the remote party during an active call
	isCaller             bool        // true if we initiated the current call
	callReturnOrigin     atomic.Bool // true when the current call was initiated via *69
	isRestartingICE      bool        // true while an ICE restart is in progress
	restartTimer         *time.Timer // timeout for ICE restart attempt
	disconnectTimer      *time.Timer // debounce before reacting to pion Disconnected

	// Link-health reporter: spawned when a call reaches Connected, canceled on teardown.
	// Protected by mu.
	reporterCancel     context.CancelFunc
	linkHealthDisabled bool
	linkHealthInterval time.Duration

	// meshReporterCancels holds one CancelFunc per mesh peer's link-health
	// reporter. Keyed by peer phone. Protected by mu, same as mesh.
	meshReporterCancels map[string]context.CancelFunc

	// Firmware version strings, protected by mu. Read/write via
	// getFirmwareVersion / setFirmwareVersion so that goroutines outside the
	// main event loop (SWD probe, service-code update, auto-update) never
	// race with the main loop's writes.
	fwVer string
	fwCom string

	// Auto-update state. The atomic bools are safe for concurrent read from
	// the update goroutine. triggerAutoUpdate is set once at startup and
	// captures the run()-scoped variables needed by runAutoUpdate.
	autoUpdateEnabled atomic.Bool
	pendingAutoUpdate atomic.Bool
	triggerAutoUpdate func() // set in run(), calls runAutoUpdate with captured vars

	// Signaling-dispatch dependencies owned by the run loop and wired once
	// before the event loop starts. These are stable for the life of the
	// daemon (set-once) and are read by handleSignal; flashCapable and
	// pairingRefresh are shared by identity with the run loop so a reset or
	// load in either place is seen by the other.
	deviceID        string          // hardware device ID, reported on factory reset
	serverURL       string          // effective signaling server URL
	flashCapable    *atomic.Bool    // SWD-flash capability (shared with run loop)
	requeryFirmware func()          // re-query Pico firmware version off the main loop
	pairingRefresh  *time.Timer     // pairing-code refresh timer (shared with run loop)
	devMode         *devModeManager // dev-mode (SSH + dev web UI) lifecycle; nil outside normal mode

	// Voicemail state. voicemailStore is opened once at startup and is nil
	// when the feature is disabled or initialization failed. recorder is the
	// active answering-machine recording (one at a time), protected by
	// recorderMu so the FSM callbacks (auto-answer, pickup, record-ended)
	// never race with the audio path. voicemailWebRTCCh receives decoded PCM
	// frames from the caller during voicemail; the channel is not connected
	// to the mixer (the earpiece stays silent), but on homeowner pickup
	// VoicemailPickup hands it to the mixer so the caller's audio routes to
	// the earpiece without rebuilding the WebRTC pipeline.
	voicemailStore    *voicemail.Store
	recorder          *voicemail.Recorder
	recorderMu        sync.Mutex
	voicemailWebRTCCh chan []int16

	// voicemailRecordArmed gates the OnRemoteTrack tee into the recorder.
	// The remote track reaches its live phase right after the call
	// connects, well before the outgoing greeting and prompt beep finish,
	// so appending from that point would prepend the whole greeting
	// duration of caller-side silence to every message. The greeting
	// goroutine sets this true at the moment it transitions into the
	// recording state, so the recorder only ever sees the actual message.
	voicemailRecordArmed atomic.Bool

	// Greeting recording (separate from message recording above). Active
	// only between *97 entry and either # / hook-on / max-duration. Lives
	// under its own mutex so it doesn't contend with the message recorder
	// path. The encoder is paired with the recorder: cleared together.
	greetingRecorder   *voicemail.Recorder
	greetingEncoder    *codec.Encoder
	greetingRecorderMu sync.Mutex

	// Voicemail retrieval (*98) playback state. Distinct from d.mu and
	// from the recorder mutexes above: capture and retrieval are mutually
	// exclusive at the FSM level (recording happens only in RINGING /
	// VOICEMAIL_RECORDING, retrieval only in VOICEMAIL_PLAYBACK), but
	// they share the same mixer key, so a dedicated mutex keeps the
	// teardown / advance / DTMF paths from racing each other.
	//
	// Lock-ordering invariant: voicemailMu MUST NOT be held while calling
	// into d.ctrl (which takes c.mu). All controller callbacks fired from
	// playback paths run after voicemailMu is released; the FSM
	// callbacks (VoicemailEnterPlayback, VoicemailExitPlayback) are
	// themselves invoked under c.mu by the controller, so any
	// d.ctrl.ResetToDialtone() they need is deferred to a goroutine to
	// avoid c.mu recursion.
	voicemailMu       sync.Mutex
	voicemailPlayback *voicemailPlaybackSession // current message; nil = idle
	voicemailMixerCh  chan []int16              // mixer source channel for "voicemail" key; nil between sessions

	// Per-message announcement state for a *98 retrieval session. When two
	// or more messages are queued, each one is preceded by a spoken
	// "Message N". voicemailAnnounceNumbers is set once at session entry
	// (false for a lone message, which the count intro already identifies);
	// voicemailMessageSeq is the running 1-based counter, reset on entry and
	// bumped by openNextUnheardLocked as each new message opens. Both are
	// guarded by voicemailMu.
	voicemailAnnounceNumbers bool
	voicemailMessageSeq      int

	// Saved-message review state for a *98 retrieval session. After the
	// unheard messages play, *98 continues into the messages that were
	// already heard before this session began. voicemailSavedQueue is the
	// snapshot of those message IDs, taken at session entry (ascending, so
	// oldest first) before any new message gets auto-marked-heard.
	// voicemailSavedCursor indexes the currently playing saved message; it
	// starts at -1 and openNextSavedLocked advances it. Both guarded by
	// voicemailMu.
	voicemailSavedQueue  []int64
	voicemailSavedCursor int

	// Voicemail-state publish bookkeeping. publishVMMu serializes calls to
	// publishVoicemailState so two trigger sites firing concurrently (e.g.
	// recorder finalize racing a retrieval MarkHeard) cannot both send the
	// same unheard count, and so dedup against publishVMLast is correct
	// even under contention. publishVMLast holds the last successfully
	// sent count; the sentinel -1 forces the first publish to fire
	// regardless of value, which is what the post-connect snapshot needs
	// to seed server-side state.
	publishVMMu   sync.Mutex
	publishVMLast int64

	// reexec restarts the process so it re-registers from a freshly saved
	// config (used by the line-renumber self-heal path). Nil in production,
	// where reexecProcess does the os.Exit; tests inject a recorder to assert
	// the restart was requested without killing the test binary.
	reexec func()
}

// reexecProcess exits so systemd restarts digitsd, which re-registers using
// the current on-disk config. Indirected through d.reexec so tests can
// substitute a no-op recorder.
func (d *daemonCallbacks) reexecProcess() {
	if d.reexec != nil {
		d.reexec()
		return
	}
	os.Exit(0)
}

// getFirmwareVersion returns the current firmware version and commit under mu.
func (d *daemonCallbacks) getFirmwareVersion() (version, commit string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fwVer, d.fwCom
}

// setFirmwareVersion stores a new firmware version and commit under mu.
func (d *daemonCallbacks) setFirmwareVersion(version, commit string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fwVer = version
	d.fwCom = commit
}

// autoUpdateAllowed reports whether an automatic update may run right now:
// the line's auto-update setting is on AND the dev-mode skip-auto-update flag
// is not present. Every place that decides to trigger an update gates on this
// so the two-part policy stays in one spot.
func (d *daemonCallbacks) autoUpdateAllowed() bool {
	return d.autoUpdateEnabled.Load() && !devmode.IsSet(devmode.DefaultSkipAutoUpdatePath)
}

// sendSignal sends a signaling message and logs failures.
func sendSignal(sig *sigclient.Client, msg *sigclient.Message) {
	if err := sig.Send(msg); err != nil {
		slog.Warn("signal send failed", "type", msg.Type, "to", msg.To, "error", err)
	}
}

// currentSig returns the active signaling client under cb.mu. The reconnect
// goroutine swaps d.sig on every reconnect, so pion callbacks and FSM
// goroutines must read it through this accessor rather than touching d.sig
// directly, which would race with that writer.
func (d *daemonCallbacks) currentSig() *sigclient.Client {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sig
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

func (d *daemonCallbacks) StartRing() {
	d.serial.StartRing()
}

func (d *daemonCallbacks) StopRing() {
	d.serial.StopRing()
}

func (d *daemonCallbacks) SendRingPattern(id int) {
	d.serial.RingPattern(id)
}

// SendLED forwards the controller's LED command to the firmware. The
// "OFF" mode is rewritten to "SLOWER_PULSE" when voicemail is enabled
// and there are unheard messages, so the idle LED also serves as a
// message-waiting indicator. Other modes (ON, BLINK, FAST_PULSE, etc.)
// pass through untouched: when the phone rings or is connected, those
// states take precedence over the message-waiting hint.
//
// SLOWER_PULSE (firmware: 150ms on, 3000ms off) is deliberately distinct
// from SLOW_PULSE (200ms on, 1500ms off), which the firmware uses for
// the boot-unpaired phase. Two patterns means the user can tell at a
// glance whether the phone is unprovisioned or has unheard voicemail.
func (d *daemonCallbacks) SendLED(mode string) {
	d.serial.LED(d.ledModeWithVoicemailHint(mode))
}

func (d *daemonCallbacks) NotifyCallConnected() {
	d.serial.CallConnected()
}

func (d *daemonCallbacks) NotifyPicoReset() {
	d.serial.Reset()
}

func (d *daemonCallbacks) EnableFlashDetection() {
	d.serial.EnableFlashDetection()
}

func (d *daemonCallbacks) OnCallReturn() {
	d.callReturnOrigin.Store(true)
	if err := d.currentSig().Send(&sigclient.Message{Type: sigclient.TypeCallReturn}); err != nil {
		slog.Error("call_return: server unreachable", "error", err)
		d.callReturnOrigin.Store(false)
		d.mixer.PlayOnce("disconnected")
		d.ctrl.ResetToDialtone()
		d.mixer.PlayLoop("tone_dial")
	}
}

func (d *daemonCallbacks) OnCallReturnCancel() {
	if err := d.currentSig().Send(&sigclient.Message{Type: sigclient.TypeCallReturnCancel}); err != nil {
		slog.Error("call_return_cancel: server unreachable", "error", err)
	}
}

func (d *daemonCallbacks) OnCallReturnAbandon() {
	d.callReturnOrigin.Store(false)
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

// publishVoicemailStateOnce reads the current unheard-message count from store
// and sends a voicemail_state message via sender. When force is false, the
// send is skipped if the count equals *last (dedup against repeated change
// notifications). When force is true, the send is unconditional, used after
// (re)connect to seed server-side state regardless of local cache parity.
// Returns true when a message was actually sent.
//
// The caller is responsible for serializing concurrent invocations (the daemon
// uses publishVMMu); inside this function, sender.Send and store.UnheardCount
// can both block on I/O so neither runs under d.mu.
//
// On a nil store (voicemail feature disabled at boot) or any UnheardCount
// error, no message is sent and *last is left unchanged so the next trigger
// will retry from the same baseline.
func publishVoicemailStateOnce(sender sigSender, store *voicemail.Store, last *int64, force bool) bool {
	if store == nil {
		return false
	}
	n, err := store.UnheardCount()
	if err != nil {
		slog.Warn("voicemail_state: unheard count failed, skipping publish", "error", err)
		return false
	}
	if !force && int64(n) == *last {
		return false
	}
	if err := sender.Send(&sigclient.Message{
		Type:                  sigclient.TypeVoicemailState,
		VoicemailUnheardCount: n,
	}); err != nil {
		slog.Warn("voicemail_state: send failed", "count", n, "error", err)
		return false
	}
	*last = int64(n)
	slog.Info("voicemail_state: published", "unheard_count", n, "forced", force)
	return true
}

// publishVoicemailState snapshots the current store, serializes against
// concurrent callers via publishVMMu, and publishes the unheard count.
//
// force=false is the change-driven path used by mutation triggers (recording
// finalize, MarkHeard, delete): it dedups against publishVMLast so a no-op
// mutation does not produce a redundant wire message. force=true is the
// (re)connect path: it sends the current count unconditionally so the server
// can seed its per-phone cache on every fresh WS session, even when the local
// count has not changed since the previous publish.
func (d *daemonCallbacks) publishVoicemailState(force bool) {
	d.mu.Lock()
	store := d.voicemailStore
	sig := d.sig
	d.mu.Unlock()

	d.publishVMMu.Lock()
	defer d.publishVMMu.Unlock()
	publishVoicemailStateOnce(sig, store, &d.publishVMLast, force)
}

// setVoicemailConfig replaces the local voicemail config under d.mu and
// persists it to disk outside the lock. Server pushes are treated as a full
// replacement of the Voicemail sub-blob; per-field diffing for log granularity
// happens at the call site before this helper runs.
//
// Live consumption: both fields, Enabled and RingTimeout, are read per ring,
// so they take effect on the next inbound call without any further wiring.
// The storage cap is no longer pushed; it is the fixed
// config.VoicemailMaxStoredMessages constant and never flows through this
// helper. The retrieval code lives in the phone package as a constant.
func (d *daemonCallbacks) setVoicemailConfig(vm config.Voicemail) error {
	d.mu.Lock()
	d.cfg.Voicemail = vm
	d.mu.Unlock()
	return d.cfg.Save()
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

// runPicoFlashScript invokes flash-pico.sh (RESCUE retry, FLASHSIZE override,
// PCB-rev marker write at 0x101FF000) against the bundled firmware.
// SKIP_SERVICE_CONTROL=1 stops the script from systemctl-stopping us (we ARE
// digitsd) and from doing its own post-flash PING; callers own the serial port
// and re-PING themselves. Returns an error if there is no firmware to flash or
// the script fails, so callers can decide whether to retry the serial link.
func runPicoFlashScript(reason string) error {
	if _, err := os.Stat(defaultFirmwarePath); err != nil {
		return fmt.Errorf("no firmware at %s: %w", defaultFirmwarePath, err)
	}
	slog.Info("reflash: starting", "path", defaultFirmwarePath, "reason", reason)
	cmd := exec.Command("setsid", "bash", defaultFlashScript, defaultFirmwarePath)
	cmd.Env = append(os.Environ(), "SKIP_SERVICE_CONTROL=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("flash script failed: %w", err)
	}
	slog.Info("reflash: flash script succeeded", "reason", reason)
	return nil
}

// reflashPico delegates to runPicoFlashScript and re-establishes the serial
// port. Aborts on serial-reopen failure: nothing else digitsd does works
// without UART. Returns the reopened port and whether the post-flash PING
// passed.
func reflashPico(sp *phone.SerialPort, serialDev string, serialLogger *slog.Logger, reason string) (*phone.SerialPort, bool) {
	if _, err := os.Stat(defaultFirmwarePath); err != nil {
		slog.Info("reflash: no firmware at path, skipping", "path", defaultFirmwarePath, "reason", reason)
		return sp, false
	}
	if err := sp.Close(); err != nil {
		slog.Warn("reflash: close serial failed", "error", err)
	}
	if err := runPicoFlashScript(reason); err != nil {
		slog.Error("reflash: flash failed", "error", err, "reason", reason)
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

// picoStateResyncer is the subset of *phone.SerialPort that the Pico
// hardware-reset and state-resync helpers need. Declaring it as an interface
// lets tests fake the serial boundary and record the commands emitted, the same
// way linkhealth fakes its sigSender.
type picoStateResyncer interface {
	StopRing()
	LED(mode string)
	StateSet(state string)
}

// resetPicoHardware clears any residual ring or LED state on the Pico in case
// the Pi or Pico rebooted mid-call. Safe no-op on clean boots where none of
// these hardware states were active.
func resetPicoHardware(sp picoStateResyncer) {
	slog.Info("pico: clearing residual hardware state")
	sp.StopRing()
	sp.LED("UNLOCK")
}

// picoStateForToken derives the persisted Pico phase from the pairing state the
// same way the startup path does: an empty device token means the phone is not
// yet paired. Kept as a pure function so the mapping has a single source of
// truth shared by startup and the post-flash resync.
func picoStateForToken(deviceToken string) string {
	if deviceToken == "" {
		return "UNPAIRED"
	}
	return "PAIRED"
}

// resyncPicoState restores the Pico's residual hardware state and persisted
// phase byte, mirroring exactly what the startup path does after POST. The Pico
// boots PHASE_PAIRED as LED_MODE_BREATHING and relies on the Pi to clear it; a
// runtime firmware flash reboots the chip without a daemon restart, so without
// this the LED is left breathing at idle. deviceToken is read live (pairing can
// change it at runtime) and mapped through picoStateForToken so this stays in
// lockstep with the startup StateSet.
func resyncPicoState(sp picoStateResyncer, deviceToken string) {
	resetPicoHardware(sp)
	sp.StateSet(picoStateForToken(deviceToken))
}

// playPairingAnnouncement queues one full pairing-voice sequence on mixer:
// silence pad, welcome, the code digits, and the "expires in N minute(s)"
// tail. code and expiresAt are passed explicitly (not read from cb) so the
// caller controls the cross-goroutine read: the announcement runs from a
// goroutine spawned on HOOK:OFF and reading cb.pairingCode there would race
// the dispatcher's TypePairingCode handler. minutesLeft is computed from the
// server-reported expiry on each call so a long-listening user hears an
// accurate countdown that matches when the server actually invalidates it.
// pairingAnnouncementClips returns the ordered mixer clip names for one pairing
// announcement: silence pad, welcome, the code digits, "expires in", the minute
// count, and the singular/plural unit. minutesLeft is capped at 9 because the
// spoken number clips only exist for spoken_0..spoken_9; a fresh code (TTL 10m)
// would otherwise reference a nonexistent spoken_10 and play a silent number.
// Pure (no mixer) so the clamp and sequence are unit-testable.
func pairingAnnouncementClips(code string, minutesLeft int) []string {
	if minutesLeft < 1 {
		minutesLeft = 1
	} else if minutesLeft > 9 {
		minutesLeft = 9
	}
	clips := []string{"pairing_silence", "pairing_welcome"}
	for _, ch := range code {
		clips = append(clips, "spoken_"+string(ch))
	}
	unitClip := "pairing_expires_minutes"
	if minutesLeft == 1 {
		unitClip = "pairing_expires_minute"
	}
	return append(clips, "pairing_expires_prefix", fmt.Sprintf("spoken_%d", minutesLeft), unitClip)
}

func playPairingAnnouncement(mixer *audio.Mixer, code string, expiresAt time.Time) {
	minutesLeft := int(math.Ceil(time.Until(expiresAt).Minutes()))
	for _, clip := range pairingAnnouncementClips(code, minutesLeft) {
		mixer.PlayOnce(clip)
	}
}

// recoverySerialDevice returns the raw UART node for the Pico. Recovery mode
// runs as PID 1 without udev, so the /dev/serial0 symlink that normal boot
// relies on does not exist; only the kernel-created device node does. Under
// disable-bt the Pico is on the PL011 (/dev/ttyAMA0). Falls back to the
// configured -serial value if neither raw node is present.
func recoverySerialDevice() string {
	for _, dev := range []string{"/dev/ttyAMA0", "/dev/ttyS0"} {
		if _, err := os.Stat(dev); err == nil {
			return dev
		}
	}
	return *serialDev
}

func recoveryRegistrations() ([]subsystem.Registration, *subsystem.WebModule, *subsystem.SerialModule, *subsystem.AudioModule) {
	web := subsystem.NewWebModule()
	gpclk0 := subsystem.NewGPCLK0Module()
	serial := subsystem.NewSerialModule(subsystem.SerialConfig{Device: recoverySerialDevice(), Baud: 115200})
	audio := subsystem.NewAudioModule(subsystem.AudioConfig{
		ToneDir:         "/tones",
		MixerStateFile:  "/mixer.state",
		GPCLK0Retrigger: gpclk0.Retrigger,
	})

	regs := []subsystem.Registration{
		{Module: subsystem.NewMountsModule(), Required: true},
		{Module: subsystem.NewKernModsModule(), Deps: []string{"mounts"}, Required: true},
		{Module: gpclk0, Deps: []string{"kernel-modules"}},
		{Module: serial, Deps: []string{"kernel-modules"}},
		// WiFi-AP is best-effort in recovery: the captive portal is a
		// convenience, but the primary recovery path is the handset voice
		// menu, which needs only serial + audio. A failed AP (no wlan0, driver
		// wedged, rfkill) must not error mgr.Run and short-circuit to
		// syncAndHalt before runRecoveryMode ever starts the voice loop.
		{Module: subsystem.NewWiFiAPModule(subsystem.WiFiAPConfig{SSID: "Digits-Recovery"}), Deps: []string{"kernel-modules"}},
		{Module: audio, Deps: []string{"gpclk0", "serial"}},
		{Module: web, Required: true},
		{Module: subsystem.NewReaperModule(), Required: true},
	}
	return regs, web, serial, audio
}

// reapplyCodecMixerState re-runs `alsactl restore` for the codec once the
// playback device is open and its output is powered. See the call site in
// main: the boot-time restore runs before playback opens, so the TLV320's
// DAPM-gated output controls revert to register defaults afterward. Re-applying
// with the output live makes them stick. Best-effort: a failure is logged at
// Warn and we continue, since the boot-time restore and RestoreVolume have
// already run.
func reapplyCodecMixerState(card, statePath string) {
	if _, err := os.Stat(statePath); err != nil {
		slog.Info("mixer re-apply: no state file, skipping", "path", statePath)
		return
	}
	out, err := exec.Command("alsactl", "restore", card, "-f", statePath).CombinedOutput()
	if err != nil {
		slog.Warn("mixer re-apply after playback open failed", "card", card, "err", err, "output", strings.TrimSpace(string(out)))
		return
	}
	slog.Info("mixer re-apply after playback open: applied", "card", card)
}

func setupRegistrations() ([]subsystem.Registration, *subsystem.WebModule, *subsystem.SerialModule, *subsystem.AudioModule) {
	web := subsystem.NewWebModule()
	gpclk0 := subsystem.NewGPCLK0Module()
	// A fresh device boots straight into setup/AP mode and never runs the
	// normal-mode reflash path, so a board whose RP2040 was never programmed
	// has a dead UART: no hook events, no setup voice prompts. Flash the Pico
	// on the way up if it doesn't answer.
	serial := subsystem.NewSerialModule(subsystem.SerialConfig{
		Device:      *serialDev,
		Baud:        115200,
		FlashOnFail: runPicoFlashScript,
	})
	audio := subsystem.NewAudioModule(subsystem.AudioConfig{
		ToneDir:         *toneDir,
		GPCLK0Retrigger: gpclk0.Retrigger,
	})
	// AP is managed by digits-dnsmasq-ap.service (systemd), not our module,
	// so this registration is Disabled below: it exists only so the module
	// shows up as "wifi-ap: disabled" on the setup-mode status page.
	wifiAP := subsystem.NewWiFiAPModule(subsystem.WiFiAPConfig{SSID: "Digits-Setup"})

	regs := []subsystem.Registration{
		{Module: gpclk0, Required: true},
		{Module: serial, Deps: []string{"gpclk0"}},
		{Module: audio, Deps: []string{"gpclk0"}},
		{Module: wifiAP, Disabled: true},
		{Module: web, Required: true},
	}
	return regs, web, serial, audio
}

// queryPicoPhase reads the Pico's persisted phase byte, retrying briefly since
// the link may still be settling right after POST.
func queryPicoPhase(sp *phone.SerialPort) (uint8, error) {
	const phaseRetries = 5
	var phase uint8
	var err error
	for i := 1; i <= phaseRetries; i++ {
		phase, err = sp.QueryPhase()
		if err == nil {
			return phase, nil
		}
		slog.Warn("phase query failed", "attempt", i, "max", phaseRetries, "error", err)
		time.Sleep(500 * time.Millisecond)
	}
	return 0, err
}

// enterPanicRecovery handles a Pico phase of RECOVERY (the user held * at
// power-on): flag recovery for the next boot, clear the panic phase to a
// non-recovery value so it does not loop, and reboot. Called from both normal
// and setup mode so the panic button works regardless of whether WiFi is
// configured: a device that boots into setup/AP mode must still reach recovery.
func enterPanicRecovery(sp *phone.SerialPort, deviceToken string) {
	slog.Info("panic button: Pico phase is RECOVERY (* held at boot), entering recovery mode")
	if err := bootcount.SetThreshold(bootcount.DefaultPath, 3); err != nil {
		slog.Warn("panic button: failed to set boot counter", "error", err)
	}
	_ = os.WriteFile("/data/digits/recovery-mode", []byte("panic-button\n"), 0644)
	if deviceToken == "" {
		sp.StateSet("UNPAIRED")
	} else {
		sp.StateSet("PAIRED")
	}
	time.Sleep(500 * time.Millisecond)
	doReboot()
}

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		os.Exit(0)
	}

	if *modeFlag == "gpclk0" {
		if err := subsystem.EnableGPCLK0(); err != nil {
			log.Fatalf("gpclk0: %v", err)
		}
		return
	}

	if *modeFlag == "recovery" || os.Getpid() == 1 {
		// Set a known PATH/LD_LIBRARY_PATH for the recovery rootfs. As PID 1
		// after switch_root we inherit the environment from the initramfs
		// /init, whose PATH is not contractually guaranteed to include the
		// /bin and /sbin where the recovery partition stages its tools. Every
		// exec.Command in the recovery path (insmod, ip, alsactl, mkfs.ext4,
		// hostapd, dnsmasq, rfkill, ...) is invoked by bare name, so pin the
		// search path here rather than relying on whatever the initramfs left
		// behind. LD_LIBRARY_PATH=/lib matches where the build copies the
		// shared libraries for those dynamically linked tools.
		if os.Getpid() == 1 {
			_ = os.Setenv("PATH", "/sbin:/bin:/usr/sbin:/usr/bin")
			_ = os.Setenv("LD_LIBRARY_PATH", "/lib")
			// Crash log to /data for SD card post-mortem. Must happen before
			// the mounts module since /data might already be mounted (normal
			// boot triggering recovery) or will be mounted by the mounts module.
			_ = os.MkdirAll("/tmp", 0755)
			_ = syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "size=64M")
			_ = os.MkdirAll("/data", 0755)
			_ = syscall.Mount("/dev/mmcblk0p4", "/data", "ext4", 0, "")
		}
		setupCrashLog("/data/digits/crash.log")
		setupModeLog("/tmp/recovery.log")

		regs, web, serial, audioMod := recoveryRegistrations()
		mgr := subsystem.NewManager(regs)
		web.SetLogPath("/tmp/recovery.log")
		web.SetManager(mgr)
		if err := mgr.Run(context.Background()); err != nil {
			slog.Error("recovery init failed", "error", err)
			syncAndHalt()
		}
		runRecoveryMode(web, serial, audioMod)
		return
	}

	if *modeFlag == "setup" {
		// Crash log to /data survives reboots for post-mortem.
		setupCrashLog("/data/digits/crash.log")

		regs, web, serial, audioMod := setupRegistrations()
		mgr := subsystem.NewManager(regs)
		web.SetLogPath("/tmp/setup.log")
		web.SetManager(mgr)
		setupModeLog("/tmp/setup.log")
		if err := mgr.Run(context.Background()); err != nil {
			slog.Error("setup init failed", "error", err)
			os.Exit(1)
		}
		// Honor the panic button (* held at power-on) even with no WiFi
		// configured. A fresh device boots straight into setup/AP mode, which
		// never reaches the normal-mode phase check, so without this the LED
		// flashes RECOVERY but the device just proceeds to AP config. The
		// serial subsystem has brought the Pico up (flashing it if needed), so
		// the phase byte the firmware wrote when * was held is readable here.
		// Setup mode is pre-pairing, so pass an empty device token.
		if serial.IsReady() {
			if sp := serial.Port(); sp != nil {
				if phase, err := queryPicoPhase(sp); err != nil {
					slog.Error("setup: phase query failed, proceeding without panic-button check", "error", err)
				} else if phase == phone.PhaseRecovery {
					enterPanicRecovery(sp, "")
				}
			}
		}
		runSetupMode(web, serial, audioMod)
		return
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
		Sync: func() error {
			syscall.Sync()
			return nil
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
		if err := extractor.RemountReadOnly(); err != nil {
			slog.Warn("swd render: remount ro failed", "err", err)
		}
	}

	// Render the active mixer state from the per-codec embedded file. Picked
	// by detectCodec() walking /proc/asound, so this naturally tracks V1/V2
	// hardware swaps. The on-disk file is applied by the ExecStartPre
	// alsactl restore in digitsd.service. On the first boot after an OTA
	// that changed the embedded state, the previous-version file was
	// applied; we update the file here so the next reboot picks up the
	// new canonical.
	mixerEmbedSrc := fmt.Sprintf("mixer/v%s.state", audio.CodecPCBVariant())
	mixerCard := audio.CodecCardName()
	if data, err := fs.ReadFile(assets.SubFS(), mixerEmbedSrc); err != nil {
		slog.Warn("mixer render: read embed failed", "src", mixerEmbedSrc, "err", err)
	} else if existing, _ := os.ReadFile(mixerStatePath); bytes.Equal(existing, data) {
		slog.Info("mixer render: already current", "card", mixerCard)
	} else if err := extractor.RootfsWriteFile(data, mixerStatePath, 0644); err != nil {
		slog.Warn("mixer render: write failed", "dest", mixerStatePath, "err", err)
	} else {
		slog.Info("mixer render: wrote canonical state, applies on next reboot", "card", mixerCard, "size", len(data))
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
		needsReflash := firmwareNeedsReflash(fwVersion, bundled)
		skipReflash := devmode.IsSet(devmode.DefaultSkipFWReflashPath)
		if needsReflash && skipReflash {
			slog.Info("firmware reflash: skip flag present, keeping current Pico firmware",
				"pico", fwVersion, "bundled", bundled)
		} else if needsReflash {
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

	// Query the Pico's persisted phase byte. If the user held * during
	// power-on, the Pico wrote PHASE_RECOVERY to flash before the Pi
	// even started booting. We read it here, act on it, then clear it
	// so the device doesn't loop back into recovery on every boot.
	cachedPhase := "unknown"
	if postOk {
		phase, phaseErr := queryPicoPhase(sp)
		if phaseErr != nil {
			slog.Error("phase query: all retries exhausted, proceeding without phase check", "error", phaseErr)
		} else {
			switch phase {
			case phone.PhasePaired:
				cachedPhase = "paired"
			case phone.PhaseUnpaired:
				cachedPhase = "unpaired"
			case phone.PhaseSetup:
				cachedPhase = "setup"
			case phone.PhaseRecovery:
				cachedPhase = "recovery"
			default:
				cachedPhase = fmt.Sprintf("0x%02X", phase)
			}
			slog.Info("pico phase", "phase", fmt.Sprintf("0x%02X", phase))
			if phase == phone.PhaseRecovery {
				enterPanicRecovery(sp, cfg.DeviceToken)
			}
		}
	}

	// Clear any residual Pico hardware state from before the last reboot.
	if postOk {
		resyncPicoState(sp, cfg.DeviceToken)
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
	// Debug: capture raw PCM output if CAPTURE_PCM is set. Bounded so a forgotten
	// capture cannot fill /data (raw 48kHz stereo S16LE is ~345 MB/hr). Default
	// cap 512 MB; override the megabyte limit with CAPTURE_PCM_MAX_MB (0 = off).
	// Set up before Start() so the render goroutine observes the capture fields
	// from its first iteration without any cross-goroutine write to race on.
	if capPath := os.Getenv("CAPTURE_PCM"); capPath != "" {
		maxBytes := int64(512) * 1024 * 1024
		if v := os.Getenv("CAPTURE_PCM_MAX_MB"); v != "" {
			if mb, err := strconv.ParseInt(v, 10, 64); err == nil {
				maxBytes = mb * 1024 * 1024
			} else {
				slog.Warn("CAPTURE_PCM_MAX_MB invalid, using default", "value", v)
			}
		}
		if err := mixer.EnableCapture(capPath, maxBytes); err != nil {
			slog.Warn("PCM capture failed", "error", err)
		} else {
			defer mixer.DisableCapture()
		}
	}

	mixer.Start()
	defer mixer.Stop()

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
	var getPhoneState func() phone.State
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
		if getPhoneState != nil && !getPhoneState().IsDialPhase() {
			doubleBeep()
			return
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
			// /data/wifi-configured is owned by root (digitsd writes it as
			// root from --mode=setup); /data itself is mode 755 root:root.
			// digitsd in normal mode runs as the 'digits' user, which lacks
			// write access to /data and so cannot unlink the flag directly.
			// The digits-updater sudoers entry grants NOPASSWD on rm -f for
			// this exact path.
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
		// Drain any frames buffered before Stop closed the channel. Ranging
		// over the now-closed OutFrames yields the remaining buffered frames
		// and then returns.
		for frame := range pip.OutFrames() {
			recorded = append(recorded, frame...)
		}

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
		go func(fwVer, fwCom string) {
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
			sendDeviceInfo(sig, fwVer, fwCom)
			// Voicemail-state publish skipped here: this goroutine is
			// launched before vmStore exists and before cb is constructed.
			// The sig.Connect path at the next site (sendDeviceInfo
			// after Connect) publishes the initial snapshot once cb and
			// the store are both ready.
		}(fwVersion, fwCommit)
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

	// svcCodes.OnUpdate is set after cb is created so the closure can read
	// fwVersion through the synchronized getter (OnUpdate runs in its own
	// goroutine and would otherwise race with the main loop).

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

	// Voicemail store. Opened once at daemon startup so the FSM callbacks
	// can begin and finalize recordings without any per-call I/O setup.
	// Failures log and disable the feature; the daemon still runs.
	var vmStore *voicemail.Store
	if cfg.Voicemail.Enabled {
		vmDir := filepath.Join(filepath.Dir(*configPath), "voicemail")
		var err error
		vmStore, err = voicemail.Open(vmDir, voicemail.Options{
			MaxMessages: config.VoicemailMaxStoredMessages,
		})
		if err != nil {
			slog.Error("voicemail store open failed", "dir", vmDir, "error", err)
			vmStore = nil
		}
	}

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
		fwVer:               fwVersion,
		fwCom:               fwCommit,
		voicemailStore:      vmStore,
		// Sentinel that forces the first publishVoicemailState call to
		// fire regardless of count, so the post-connect snapshot seeds
		// server-side state on every startup.
		publishVMLast: -1,
	}
	if cfg.DeviceToken != "" {
		cb.paired.Store(true)
	}

	// Deferred from above: OnUpdate runs in its own goroutine, so it reads
	// fwVersion through the synchronized getter to avoid racing with the
	// main event loop.
	svcCodes.OnUpdate = func() {
		slog.Info("service code: *#UPDATE# (*#873283#) -- checking for updates")
		fwVer, _ := cb.getFirmwareVersion()
		go runTargetedUpdate(effectiveServerURL, version.Version, fwVer, "", "", flashCapable.Load(), nil, requeryFirmware)
	}

	// Wire auto-update. The closure reads fwVersion via the synchronized
	// getter so that HangupCall (which runs off the main goroutine) never
	// races with the main loop's writes.
	cb.autoUpdateEnabled.Store(cfg.AutoUpdate)
	cb.triggerAutoUpdate = func() {
		fwVer, _ := cb.getFirmwareVersion()
		runAutoUpdate(cb, effectiveServerURL, version.Version, fwVer, flashCapable.Load(), requeryFirmware)
	}
	if cb.autoUpdateAllowed() {
		slog.Info("auto-update: enabled, checking for updates on startup")
		go cb.triggerAutoUpdate()
	} else if devmode.IsSet(devmode.DefaultSkipAutoUpdatePath) {
		slog.Info("auto-update: suppressed by dev-mode skip flag")
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
	getPhoneState = func() phone.State { return ctrl.State() }

	// 8b. Contacts cache: optional dial safelist, loaded from a hand-placed
	// contacts.json. An empty cache leaves the checker nil so no-contacts
	// phones allow every call (matching the pre-wiring behavior).
	contactsPath := filepath.Join(filepath.Dir(*configPath), "contacts.json")
	contactsCache := contacts.NewCache(contactsPath)
	if err := contactsCache.Load(); err != nil {
		slog.Warn("contacts: load failed", "path", contactsPath, "error", err)
	} else if n := contactsCache.Count(); n > 0 {
		ctrl.SetContactChecker(contactsCache.IsContact)
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
	// Re-apply the codec mixer state now that the render loop has powered the
	// output. The ExecStartPre `alsactl restore` in digitsd.service runs before
	// we open playback, so the TLV320's DAPM-gated output controls (HP DAC, HP,
	// HPCOM) revert to register defaults when the output powers up, leaving the
	// earpiece path ~19 dB quiet. Re-applying with the output live makes them
	// stick. This also resets PCM to the stored value, so RestoreVolume() below
	// immediately re-applies the persisted user volume level.
	reapplyCodecMixerState(audio.CodecCardName(), mixerStatePath)
	phone.RestoreVolume()
	slog.Info("digitsd ready")

	// Dev-mode web UI on :8080. The manager is always constructed so a runtime
	// dev_mode command can start it without a restart; the listener only comes
	// up when the flag is present (now, at boot) or when enabled later.
	//
	// Snapshot the phase once at startup; it rarely changes during normal
	// operation and querying UART on every HTTP poll is wasteful.
	startupPhase := cachedPhase
	devCfg := &devModeConfig{
		FlagPath:           devmode.DefaultFlagPath,
		SkipFWReflashPath:  devmode.DefaultSkipFWReflashPath,
		SkipAutoUpdatePath: devmode.DefaultSkipAutoUpdatePath,
		UARTLogPath:        uartLogPath,
		CaptureDevice:      audio.CodecCaptureDevice(),
		StatusFunc: func() devModeStatus {
			fwVer, fwCom := cb.getFirmwareVersion()
			return devModeStatus{
				DigitsdVersion:   version.Version,
				FirmwareVersion:  fwVer,
				FirmwareCommit:   fwCom,
				Phase:            startupPhase,
				Online:           cb.paired.Load(),
				PhoneNumber:      effectiveNumber,
				ConfigAutoUpdate: cb.autoUpdateEnabled.Load(),
			}
		},
	}
	if flashCapable.Load() {
		devCfg.FlashFunc = func(elfPath string) error {
			// Move the uploaded ELF to the standard firmware path, then
			// invoke the same flash script the OTA updater uses.
			if err := os.Rename(elfPath, defaultFirmwarePath); err != nil {
				return fmt.Errorf("stage firmware: %w", err)
			}
			cmd := exec.Command("setsid", "bash", defaultFlashScript, defaultFirmwarePath)
			cmd.Env = append(os.Environ(), "SKIP_SERVICE_CONTROL=1")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("flash script: %w", err)
			}
			slog.Info("devmode: flash script succeeded")
			requeryFirmware()
			return nil
		}
	}
	cb.devMode = newDevModeManager(devCfg)
	defer cb.devMode.Close()
	if devmode.IsSet(devmode.DefaultFlagPath) {
		slog.Info("devmode: flag present, starting dev-mode web UI")
		if err := cb.devMode.EnsureListener(); err != nil {
			slog.Warn("devmode: failed to start web UI", "error", err)
		}
	}

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
		sendDeviceInfo(sig, fwVersion, fwCommit)
		cb.publishVoicemailState(true)
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

	// Wire the signaling-dispatch dependencies. handleSignal reads these off
	// daemonCallbacks; flashCapable and pairingRefresh are shared by identity
	// so a Store/Reset in the run loop and in a handler refer to the same one.
	cb.ctrlSignal = ctrl
	cb.deviceID = deviceID
	cb.serverURL = effectiveServerURL
	cb.flashCapable = &flashCapable
	cb.requeryFirmware = requeryFirmware
	cb.pairingRefresh = pairingRefresh

	// pairingAnnouncementCancel cancels the in-flight pairing-announcement
	// repeat goroutine spawned on HOOK:OFF (unpaired). nil when no goroutine
	// is running. Only the dispatcher select case touches this var, so no
	// mutex is needed.
	var pairingAnnouncementCancel context.CancelFunc

	// OS signal handling
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// reconnected hands a freshly connected client back from the reconnect
	// goroutine to the main loop. Buffered so the goroutine never blocks if
	// the loop is momentarily busy. reconnecting is true while that goroutine
	// is in flight: it gates sig.Done() out of the select so the loop neither
	// spins on the dead client's already-closed done channel nor blocks on
	// time.Sleep, leaving it free to service sp.Events() and quit throughout
	// the backoff/connect.
	reconnected := make(chan *sigclient.Client, 1)
	reconnecting := false

	// Main select loop
	for {
		// During reconnect, sig points at the dead client whose Done() is
		// already closed; nil out doneCh so the select does not busy-loop on
		// it. Completion arrives on the reconnected channel instead.
		var doneCh <-chan struct{}
		if !reconnecting {
			doneCh = sig.Done()
		}

		select {
		case <-quit:
			slog.Info("digitsd shutting down")
			ctrl.Close()
			mixer.Stop()
			if err := sig.Close(); err != nil {
				slog.Warn("sig close failed", "error", err)
			}
			return

		case newSig := <-reconnected:
			// The reconnect goroutine established a new client and ran the
			// post-reconnect resume/teardown. Swap it in as the loop's active
			// client; subsequent iterations select on its channels.
			sig = newSig
			reconnecting = false

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

			if event == "HOOK:ON" && !cb.paired.Load() && pairingAnnouncementCancel != nil {
				pairingAnnouncementCancel()
				pairingAnnouncementCancel = nil
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
				// cb.pairingCode + cb.pairingCodeExpiresAt. The code/expiry
				// won't change for this off-hook session: a new code only lands
				// on the next refresh, by which point HOOK:ON has cancelled
				// this goroutine. cb.paired is atomic, so the post-pair exit
				// check stays accurate without a snapshot.
				code := cb.pairingCode
				expiresAt := cb.pairingCodeExpiresAt
				slog.Info("phone: playing pairing code via voice", "code", code)
				go func() {
					for {
						// Check cancellation at the top too: without this a
						// cancel landing just after the interval wait would
						// still queue one more full announcement before the
						// next ctx.Done() check, so a service code that stops
						// the announcement could be talked over.
						select {
						case <-ctx.Done():
							return
						default:
						}
						if cb.paired.Load() || code == "" {
							return
						}
						playPairingAnnouncement(mixer, code, expiresAt)
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
				// An unpaired phone loops the pairing announcement on off-hook
				// and skips the dialing FSM, which otherwise drowns out service
				// codes and their confirmation prompts. The '*' that begins
				// every service code (e.g. *#73887# to clear Wi-Fi, *#00000#
				// to factory reset) doubles as the cue to stop the announcement
				// so the keypad flow is usable while unpaired. Re-lifting the
				// handset restarts the announcement via the HOOK:OFF branch.
				if key == "*" && !cb.paired.Load() && pairingAnnouncementCancel != nil {
					slog.Info("service code: '*' pressed while unpaired, stopping pairing announcement")
					pairingAnnouncementCancel()
					pairingAnnouncementCancel = nil
					mixer.StopAll()
				}
				playKeyTone(mixer, key)
				// Forward DTMF to the remote peer if a call is connected.
				state := ctrl.State()
				if state == phone.StateCONNECTED {
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
				// Easter eggs only fire during the dialing phase and only
				// when not mid-service-code. Service codes are always
				// processed regardless of call state, but the FSM reset
				// is suppressed when a call is active.
				inCode := svcCodes.InCode()
				dialPhase := state.IsDialPhase()
				eggTriggered := false
				if !inCode && dialPhase {
					eggTriggered = easterEggs.AddKey(key)
				}
				if !eggTriggered {
					switch svcCodes.AddKey(key) {
					case phone.ServiceCodeTerminal:
						ctrl.Reset()
						sp.SendFire("DIAL:RESET")
						continue
					case phone.ServiceCodeNonTerminal:
						if dialPhase {
							ctrl.ResetToDialtone()
							sp.SendFire("DIAL:RESET")
						}
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
				cb.setFirmwareVersion(fwVersion, fwCommit)
				slog.Info("pico: firmware version changed", "version", fwVersion, "commit", fwCommit)
				hookFlash = hookFlashCapable(fwVersion)
				slog.Info("firmware capability", "version", fwVersion, "flash_capable", hookFlash)
				sp.SetFlashEnabled(hookFlash)
				sendDeviceInfo(sig, fwVersion, fwCommit)
				cb.publishVoicemailState(true)
			} else {
				slog.Info("pico: firmware version unchanged", "version", fwVersion, "commit", fwCommit)
			}

			// A successful version re-query proves the Pico finished rebooting
			// and is answering UART again. Any reboot (runtime firmware flash,
			// external SWD, power cycle) leaves the chip booted into
			// PHASE_PAIRED as LED_MODE_BREATHING, expecting the Pi to clear it.
			// Startup does this after POST; without it here, a runtime
			// firmware-only OTA (no daemon restart) leaves the LED breathing at
			// idle. Skip while a call is active so a mid-call Pico power cycle
			// does not stomp the call LED; the OTA path is already idle-gated by
			// runAutoUpdate, so this only guards the external-reboot case.
			cb.mu.Lock()
			inCall := cb.callPeer != ""
			cb.mu.Unlock()
			if inCall {
				slog.Info("pico: skipping state resync after reboot, call in progress")
			} else {
				slog.Info("pico: resyncing state after reboot")
				resyncPicoState(sp, cfg.DeviceToken)
			}

		case msg := <-sig.Inbox():
			cb.handleSignal(msg)

		case <-pairingRefresh.C:
			if !cb.paired.Load() {
				slog.Info("signal: pairing code expiring, reconnecting for fresh code")
				_ = sig.Close()
			}

		case <-doneCh:
			// Reconnect off the main loop so the select keeps servicing UART
			// events and quit during the backoff/connect. The goroutine runs
			// the connect with backoff, performs the post-reconnect resume or
			// teardown, then hands the live client back via reconnected.
			reconnecting = true
			go reconnectLoop(reconnected, cb, ctrl, effectiveServerURL, effectiveNumber, deviceID, cfg.DeviceToken, fwVersion, fwCommit)
		}
	}
}

// reconnectLoop re-establishes the signald WebSocket after a drop, applying
// the active-call fast backoff vs idle exponential backoff, runs the resume or
// teardown for any in-progress call, then hands the connected client back to
// the main loop on done. It runs on its own goroutine so the main select loop
// stays free to service UART events and shutdown during the backoff/connect.
func reconnectLoop(
	done chan<- *sigclient.Client,
	cb *daemonCallbacks,
	ctrl *phone.Controller,
	serverURL, number, deviceID, deviceToken, fwVersion, fwCommit string,
) {
	backoff := 3 * time.Second
	first := true
	for {
		if first {
			// Attempt the first reconnect immediately: no sleep on the
			// first try so a transient blip resolves as fast as possible.
			// This is especially important during an active call so the
			// caller can re-deliver the ICE-restart offer before the peer's
			// 25s recovery window expires.
			first = false
		} else {
			// Active calls use a short fixed interval to stay within the
			// recovery window. Idle paths use standard exponential backoff.
			backoff = nextReconnectBackoff(backoff, ctrl.IsCallActive())
			slog.Info("signal: connection lost, reconnecting", "backoff", backoff)
			time.Sleep(backoff)
		}
		sig := sigclient.NewClient(serverURL, number, deviceID, deviceToken)
		cb.mu.Lock()
		cb.sig = sig
		cb.mu.Unlock()
		if err := sig.Connect(); err != nil {
			slog.Warn("signal: reconnect failed", "error", err)
			continue
		}
		slog.Info("signal: reconnected")

		// A 2-party call whose media survived the WebSocket drop is
		// resumed in place; if media also dropped, drive ICE recovery.
		// Anything else (conference, voicemail, ringing) is torn down:
		// the server already cleared its side or will after its grace
		// window, and stale renegotiation would error.
		if ctrl.State() != phone.StateIDLE {
			if cb.tryResumeAfterReconnect(ctrl.State()) {
				slog.Info("signal: resumed call after reconnect", "state", ctrl.State())
			} else {
				slog.Info("signal: tearing down stale call after reconnect", "state", ctrl.State())
				cb.TearDownAllMeshPeers()
				cb.HangupCall()
				ctrl.Reset()
			}
		}

		sendDeviceInfo(sig, fwVersion, fwCommit)
		cb.publishVoicemailState(false)
		requestICEServers(sig)
		done <- sig
		return
	}
}

// nextReconnectBackoff returns the backoff duration to use before the next
// WebSocket reconnect attempt. When a call is active, a short fixed interval
// keeps the caller within the peer's ICE-restart recovery window. When idle,
// the backoff doubles each attempt up to a 60s cap (standard exponential).
func nextReconnectBackoff(current time.Duration, callActive bool) time.Duration {
	if callActive {
		return activeCallReconnectBackoff
	}
	next := current * 2
	if next > 60*time.Second {
		return 60 * time.Second
	}
	return next
}

// requestICEServers asks signald for STUN/TURN server configs.
func requestICEServers(sig *sigclient.Client) {
	sendSignal(sig, &sigclient.Message{Type: sigclient.TypeRequestICE})
}

func sendDeviceInfo(sig *sigclient.Client, fwVersion, fwCommit string) {
	localAddr := primaryLocalAddr()
	devModeOn := devmode.IsSet(devmode.DefaultFlagPath)
	if err := sig.Send(&sigclient.Message{
		Type:            sigclient.TypeDeviceInfo,
		PiVersion:       version.Version,
		PiCommit:        version.Commit,
		FirmwareVersion: fwVersion,
		FirmwareCommit:  fwCommit,
		LocalAddr:       localAddr,
		DevMode:         devModeOn,
	}); err != nil {
		slog.Warn("device_info: send failed", "error", err)
	} else {
		slog.Info("device_info sent",
			"pi_version", version.Version, "pi_commit", version.Commit,
			"fw_version", fwVersion, "fw_commit", fwCommit,
			"local_addr", localAddr,
			"dev_mode", devModeOn)
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

// playKeyTone plays the keypad feedback beep for a pressed key and reports
// whether a tone was played. Keys that map to no tone (anything but 0-9, *, #)
// are a no-op and return false.
func playKeyTone(mixer *audio.Mixer, key string) bool {
	tone := dtmfToneName(key)
	if tone == "" {
		return false
	}
	mixer.PlayOnce(tone)
	return true
}
