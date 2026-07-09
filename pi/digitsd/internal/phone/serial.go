package phone

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.bug.st/serial"
)

// Read-error recovery backoff bounds. go.bug.st/serial reports a plain read
// timeout as (0, nil), so any non-nil Read error means the link is actually
// broken (the Pico crashed, dropped into BOOTSEL, or the cable came loose).
// The reader closes and reopens the port, backing off between attempts so a
// permanently dead /dev/serial0 does not busy-spin a core.
const (
	readErrInitialBackoff = 200 * time.Millisecond
	readErrMaxBackoff     = 5 * time.Second
)

// SerialPort owns /dev/serial0 and provides thread-safe read/write.
// Owns the serial port to the Pico: reads UART events, sends commands.
type SerialPort struct {
	// open re-establishes a fully configured port. Retained so the reader can
	// reopen after a disconnect and so tests can inject a fake transport.
	open   func() (serial.Port, error)
	device string
	baud   int

	events chan string // parsed RX events (HOOK:OFF, KEY:5, etc.)

	mu           sync.Mutex
	port         serial.Port                 // reopened only by readLoop; read by writers under mu
	respCh       atomic.Pointer[chan string] // single-slot response channel for command/response pairs
	flashEnabled atomic.Bool                 // whether HOOK:FLASH should be forwarded (requires firmware v1.5.0+)
	droppedLines atomic.Int64                // count of unrecognized UART lines dropped
	reopens      atomic.Int64                // count of successful port reopens (field-triage signal)
	linkUp       atomic.Bool                 // false while the port is down or a read error is outstanding
	reopenReq    chan struct{}               // liveness check asks the reader to reopen a silently dead link
	stop         chan struct{}
	closeOnce    sync.Once
	logger       *slog.Logger

	monitorMu sync.Mutex
	monitors  map[chan string]struct{} // tap subscribers (e.g. interactive UART terminal)
}

// openConfiguredPort opens device at baud and applies the read timeout that
// makes readLoop poll rather than block forever.
func openConfiguredPort(device string, baud int) (serial.Port, error) {
	mode := &serial.Mode{BaudRate: baud}
	port, err := serial.Open(device, mode)
	if err != nil {
		return nil, fmt.Errorf("serial open %s: %w", device, err)
	}
	if err := port.SetReadTimeout(100 * time.Millisecond); err != nil {
		_ = port.Close()
		return nil, fmt.Errorf("serial set timeout: %w", err)
	}
	return port, nil
}

// OpenSerial opens the serial port and starts the RX reader goroutine.
func OpenSerial(device string, baud int, logger *slog.Logger) (*SerialPort, error) {
	sp := &SerialPort{
		open:      func() (serial.Port, error) { return openConfiguredPort(device, baud) },
		device:    device,
		baud:      baud,
		events:    make(chan string, 64),
		reopenReq: make(chan struct{}, 1),
		stop:      make(chan struct{}),
		logger:    logger,
	}

	port, err := sp.open()
	if err != nil {
		return nil, err
	}
	sp.port = port
	sp.linkUp.Store(true)

	go sp.readLoop()
	return sp, nil
}

// Events returns channel of parsed RX events (HOOK:OFF, KEY:5, etc.)
func (sp *SerialPort) Events() <-chan string {
	return sp.events
}

// AddMonitor registers a subscriber that will receive every TX command and
// every RX line from the Pico, prefixed with "> " (TX) or "< " (RX). Used by
// the interactive UART terminal to tail traffic without disturbing the main
// event loop. Send is non-blocking: if the channel buffer is full, the line
// is dropped silently.
//
// The returned function unregisters the channel. The caller is responsible
// for draining and (eventually) closing the channel.
func (sp *SerialPort) AddMonitor(ch chan string) func() {
	sp.monitorMu.Lock()
	defer sp.monitorMu.Unlock()
	if sp.monitors == nil {
		sp.monitors = make(map[chan string]struct{})
	}
	sp.monitors[ch] = struct{}{}
	return func() {
		sp.monitorMu.Lock()
		defer sp.monitorMu.Unlock()
		delete(sp.monitors, ch)
	}
}

// broadcastMonitor delivers a line to every monitor subscriber, dropping on
// any subscriber whose buffer is full so a stuck consumer cannot stall TX/RX.
func (sp *SerialPort) broadcastMonitor(line string) {
	sp.monitorMu.Lock()
	defer sp.monitorMu.Unlock()
	for ch := range sp.monitors {
		select {
		case ch <- line:
		default:
		}
	}
}

// SendCommand sends a command and waits for a response line.
func (sp *SerialPort) SendCommand(cmd string, timeout time.Duration) (string, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	ch := make(chan string, 1)
	sp.respCh.Store(&ch)
	defer sp.respCh.Store(nil)

	sp.logger.Info("TX", "cmd", cmd)
	sp.broadcastMonitor("> " + cmd)
	if _, err := sp.port.Write([]byte(cmd + "\r\n")); err != nil {
		return "", fmt.Errorf("serial write: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("serial: timeout waiting for response to %q", cmd)
	}
}

// SendFire sends a command without waiting for response (fire-and-forget).
// A short post-write delay gives the Pico time to process the command
// before the next one arrives; without it, back-to-back fires during
// init can overflow the Pico's UART RX buffer and silently drop commands.
func (sp *SerialPort) SendFire(cmd string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.logger.Info("TX", "cmd", cmd)
	sp.broadcastMonitor("> " + cmd)
	if _, err := sp.port.Write([]byte(cmd + "\r\n")); err != nil {
		sp.logger.Warn("serial: write failed", "cmd", cmd, "error", err)
	}
	time.Sleep(15 * time.Millisecond)
}

// Ping sends PING and expects PONG.
func (sp *SerialPort) Ping() error {
	resp, err := sp.SendCommand("PING", 2*time.Second)
	if err != nil {
		return err
	}
	if resp != "PONG" {
		return fmt.Errorf("expected PONG, got %q", resp)
	}
	return nil
}

// QueryVersion sends VERSION command and parses the response.
// Returns (version, commit, error).
func (sp *SerialPort) QueryVersion() (string, string, error) {
	resp, err := sp.SendCommand("VERSION", 3*time.Second)
	if err != nil {
		return "", "", err
	}
	// Response format: VERSION:<version>:<commit>
	parts := strings.SplitN(resp, ":", 3)
	if len(parts) != 3 || parts[0] != "VERSION" {
		return "", "", fmt.Errorf("unexpected version response: %q", resp)
	}
	return parts[1], parts[2], nil
}

// QueryBoard sends BOARD? and parses the response.
// Returns (name, raw, error). `name` is the firmware-active profile name
// (e.g. "v1", "v2"). `raw` is the full response line, retained for logging
// since it also carries the rev byte the firmware read from flash.
func (sp *SerialPort) QueryBoard() (string, string, error) {
	resp, err := sp.SendCommand("BOARD?", 1*time.Second)
	if err != nil {
		return "", "", err
	}
	// Response format: BOARD:<name>:0x<rev_byte_hex>
	parts := strings.SplitN(resp, ":", 3)
	if len(parts) < 2 || parts[0] != "BOARD" {
		return "", resp, fmt.Errorf("unexpected board response: %q", resp)
	}
	return parts[1], resp, nil
}

// SetFlashEnabled toggles whether HOOK:FLASH events emitted by the Pico should
// be forwarded through to the controller. When disabled (pre-v1.5.0 firmware),
// any stray HOOK:FLASH is dropped with a warning log.
func (sp *SerialPort) SetFlashEnabled(v bool) {
	sp.flashEnabled.Store(v)
}

func (sp *SerialPort) flashEnabledNow() bool {
	return sp.flashEnabled.Load()
}

// DroppedLines returns the number of unrecognized UART lines that were dropped.
func (sp *SerialPort) DroppedLines() int64 {
	return sp.droppedLines.Load()
}

// LinkUp reports whether the reader currently holds a live port. It goes false
// the instant a read error is seen and true again once a reopen succeeds. A
// silently wedged Pico (Read keeps timing out with no error) still reads true
// here; use a PING to probe that case and call RequestReopen on failure.
func (sp *SerialPort) LinkUp() bool {
	return sp.linkUp.Load()
}

// Reopens returns how many times the reader has re-established the port after a
// disconnect. A climbing count is the field signal that the UART link is flapping.
func (sp *SerialPort) Reopens() int64 {
	return sp.reopens.Load()
}

// RequestReopen asks the reader goroutine to close and reopen the port on its
// next poll. Used by the liveness check when PING stops answering but Read has
// not returned an error (the Pico is wedged, not disconnected), a case the
// read-error path cannot detect on its own. Non-blocking and idempotent: a
// pending request already queued is left as-is.
func (sp *SerialPort) RequestReopen() {
	select {
	case sp.reopenReq <- struct{}{}:
	default:
	}
}

// StartRing sends RING:START to the Pico to begin ringing.
func (sp *SerialPort) StartRing() {
	sp.SendFire("RING:START")
}

// StopRing sends RING:STOP to the Pico to stop ringing.
func (sp *SerialPort) StopRing() {
	sp.SendFire("RING:STOP")
}

// RingPattern sends RING:PATTERN:<id> to the Pico for distinctive ring cadences.
func (sp *SerialPort) RingPattern(id int) {
	sp.SendFire(fmt.Sprintf("RING:PATTERN:%d", id))
}

// QueryPhase sends PHASE? and returns the raw phase byte as a uint8.
// Response format: PHASE:0xNN
func (sp *SerialPort) QueryPhase() (uint8, error) {
	resp, err := sp.SendCommand("PHASE?", 1*time.Second)
	if err != nil {
		return 0, err
	}
	parts := strings.SplitN(resp, ":", 2)
	if len(parts) != 2 || parts[0] != "PHASE" {
		return 0, fmt.Errorf("unexpected phase response: %q", resp)
	}
	var val uint8
	if _, err := fmt.Sscanf(parts[1], "0x%02X", &val); err != nil {
		return 0, fmt.Errorf("parse phase byte: %w (raw=%q)", err, parts[1])
	}
	return val, nil
}

// Phase byte constants matching firmware/src/phase.h.
const (
	PhasePaired   uint8 = 0x01
	PhaseUnpaired uint8 = 0x02
	PhaseSetup    uint8 = 0x03
	PhaseRecovery uint8 = 0x04
)

// LED sends LED:<mode> to the Pico.
func (sp *SerialPort) LED(mode string) {
	sp.SendFire("LED:" + mode)
}

// StateSet sends STATE:SET:<state> to the Pico to persist the phase byte.
func (sp *SerialPort) StateSet(state string) {
	sp.SendFire("STATE:SET:" + state)
}

// CallConnected sends CALL:CONNECTED to the Pico.
func (sp *SerialPort) CallConnected() {
	sp.SendFire("CALL:CONNECTED")
}

// Reset sends RESET to the Pico, returning its FSM to IDLE (RST:OK). Used to
// release a CALL:CONNECTED hold taken for a peerless local session (voicemail)
// so the Pico re-arms its dial path and can place the next call without a hook
// cycle.
func (sp *SerialPort) Reset() {
	sp.SendFire("RESET")
}

// EnableFlashDetection opens the flash-detection window on the Pico: after
// on-hook it waits up to 600ms to distinguish a flash from a hangup. The Pi
// enables this only while in a call.
func (sp *SerialPort) EnableFlashDetection() {
	sp.SendFire("HOOK:FLASH:ON")
}

// DisableFlashDetection closes the flash-detection window so the Pico treats
// on-hook as an instantaneous hangup.
func (sp *SerialPort) DisableFlashDetection() {
	sp.SendFire("HOOK:FLASH:OFF")
}

// Close stops the reader and closes the port. Safe to call more than once:
// the stop channel is closed at most once, so a double Close never panics.
func (sp *SerialPort) Close() error {
	sp.closeOnce.Do(func() { close(sp.stop) })
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.port == nil {
		return nil
	}
	return sp.port.Close()
}

// isUnsolicitedEvent returns true for messages the Pico sends on its own
// (not in response to a command). These must always be delivered to the
// events channel, even when a synchronous command is in flight, so that
// e.g. a STATUS:READY boot message doesn't get swallowed by a pending
// VERSION query.
func isUnsolicitedEvent(line string) bool {
	switch {
	case line == "HOOK:OFF" || line == "HOOK:ON" || line == "HOOK:FLASH":
		return true
	case line == "STATUS:READY":
		return true
	case strings.HasPrefix(line, "BOOT:"):
		return true
	case strings.HasPrefix(line, "KEY:"):
		return true
	case line == "DIAL:RESET:OK":
		// Ack for the fire-and-forget DIAL:RESET command, not a dialed
		// number. It shares the DIAL: prefix but must not be forwarded as an
		// unsolicited event, or it reaches onDial as if the user dialed
		// "RESET:OK". Handled as a fire-and-forget ack below instead.
		return false
	case strings.HasPrefix(line, "DIAL:"):
		return true
	case strings.HasPrefix(line, "FSM:"):
		return true
	default:
		return false
	}
}

// isFireAndForgetAck returns true for protocol acks the Pico emits in
// response to fire-and-forget commands (HOOK:FLASH:ON/OFF, CALL:CONNECTED,
// DIAL:RESET). These have no waiting consumer on the Pi side. Without this
// filter they fall through to the events channel and trigger spurious
// "unhandled event" warnings for every call.
func isFireAndForgetAck(line string) bool {
	switch line {
	case "HOOK:FLASH:ON", "HOOK:FLASH:OFF":
		return true
	case "CALL:CONNECTED:ACK", "CALL:CONNECTED:IGNORED":
		return true
	case "STATE:SET:OK":
		return true
	case "DIAL:RESET:OK":
		return true
	default:
		return false
	}
}

// isKnownResponsePrefix returns true for lines that are valid command
// responses from the Pico. Lines that reach the fallthrough path (no pending
// command consumer) are checked against this allowlist. Anything unrecognized
// is dropped with a debug log rather than forwarded to the events channel.
func isKnownResponsePrefix(line string) bool {
	switch {
	case line == "PONG":
		return true
	case line == "RST:OK", line == "REBOOT:OK":
		return true
	case line == "RING:ACK", line == "RING:TEST:ACK", line == "RING:DONE":
		return true
	case strings.HasPrefix(line, "VERSION:"):
		return true
	case strings.HasPrefix(line, "BOARD:"):
		return true
	case strings.HasPrefix(line, "CONFIG:PCB_REV="):
		return true
	case strings.HasPrefix(line, "STATE:"):
		return true
	case strings.HasPrefix(line, "HOOK:"):
		return true
	case strings.HasPrefix(line, "MODE:"):
		return true
	case strings.HasPrefix(line, "ROWS:"), strings.HasPrefix(line, "COLS:"), strings.HasPrefix(line, "SCAN "):
		return true
	default:
		return false
	}
}

// isReadTimeout reports whether err is a benign read timeout that the reader
// should retry immediately, versus a real error that means the link is broken.
// go.bug.st/serial signals a plain timeout as (0, nil), so in practice any
// non-nil Read error is a real one; this still recognizes deadline/timeout
// style errors so a future transport that surfaces them cannot be mistaken for
// a disconnect and trigger needless reopen churn.
func isReadTimeout(err error) bool {
	if err == nil {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, os.ErrDeadlineExceeded)
}

// nextBackoff doubles d up to readErrMaxBackoff.
func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > readErrMaxBackoff {
		return readErrMaxBackoff
	}
	return d
}

// sleepStop waits for d or for Close, whichever comes first. It returns true if
// Close fired so the caller can stop the reader promptly instead of sleeping
// out a long backoff during shutdown.
func (sp *SerialPort) sleepStop(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-sp.stop:
		return true
	case <-t.C:
		return false
	}
}

// reopen closes the current port and opens a fresh one. Called only from the
// reader goroutine, so it never races another reopen; it takes sp.mu so an
// in-flight writer never touches a port mid-swap. On failure sp.port is left
// nil and linkUp false; the reader retries with backoff.
func (sp *SerialPort) reopen() error {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.port != nil {
		_ = sp.port.Close()
		sp.port = nil
	}
	port, err := sp.open()
	if err != nil {
		sp.linkUp.Store(false)
		return err
	}
	sp.port = port
	sp.reopens.Add(1)
	sp.linkUp.Store(true)
	return nil
}

// recoverLink reopens the port and, on failure, sleeps out the current backoff
// so a dead device does not busy-spin. It updates *backoff in place (reset on
// success, doubled on failure) and returns true if Close fired during the wait
// so the reader can exit promptly.
func (sp *SerialPort) recoverLink(backoff *time.Duration) (stopped bool) {
	if err := sp.reopen(); err != nil {
		sp.logger.Warn("serial: reopen failed, backing off", "device", sp.device, "error", err, "backoff", *backoff)
		if sp.sleepStop(*backoff) {
			return true
		}
		*backoff = nextBackoff(*backoff)
		return false
	}
	sp.logger.Warn("serial: reopened UART link", "device", sp.device, "reopens", sp.reopens.Load())
	*backoff = readErrInitialBackoff
	return false
}

func (sp *SerialPort) readLoop() {
	buf := make([]byte, 256)
	var lineBuf strings.Builder
	backoff := readErrInitialBackoff

	for {
		select {
		case <-sp.stop:
			return
		default:
		}

		// The liveness check declared the link dead even though Read has not
		// errored (a wedged Pico still lets the fd time out cleanly). Honor
		// the reopen request before the next Read so a stuck port is cycled.
		select {
		case <-sp.reopenReq:
			sp.logger.Warn("serial: reopen requested by liveness check", "device", sp.device)
			sp.linkUp.Store(false)
			lineBuf.Reset()
			if sp.recoverLink(&backoff) {
				return
			}
			continue
		default:
		}

		port := sp.port
		if port == nil {
			// A prior reopen failed; keep retrying with backoff.
			if sp.recoverLink(&backoff) {
				return
			}
			continue
		}

		n, err := port.Read(buf)
		if err != nil {
			if isReadTimeout(err) {
				continue
			}
			select {
			case <-sp.stop:
				return
			default:
			}
			// A real error: the Pico crashed, dropped into BOOTSEL, or the
			// cable is gone. Mark the link down, discard any partial line, and
			// reopen (backoff bounds the retry).
			sp.linkUp.Store(false)
			lineBuf.Reset()
			sp.logger.Warn("serial: read error, reopening link", "device", sp.device, "error", err)
			if sp.recoverLink(&backoff) {
				return
			}
			continue
		}
		// A read that returns without error (n may be 0 on a timeout) proves
		// the link is healthy; reset the backoff.
		backoff = readErrInitialBackoff

		for i := 0; i < n; i++ {
			ch := buf[i]
			if ch == '\n' || ch == '\r' {
				line := strings.TrimSpace(lineBuf.String())
				lineBuf.Reset()
				if line == "" {
					continue
				}

				sp.logger.Info("RX", "line", line)
				sp.broadcastMonitor("< " + line)

				// Unsolicited Pico events (hook, keypad, boot) always go to
				// the events channel, never to a pending command response.
				if isUnsolicitedEvent(line) {
					if line == "HOOK:FLASH" && !sp.flashEnabledNow() {
						sp.logger.Warn("HOOK:FLASH received but firmware is not flash-capable; ignoring")
						continue
					}
					if line == "HOOK:FLASH" {
						sp.logger.Info("HOOK:FLASH forwarded from firmware")
					}
					select {
					case sp.events <- line:
					default:
						sp.logger.Warn("serial: events full, dropping", "line", line)
					}
					continue
				}

				// Command response: deliver to pending command if one is waiting.
				if chp := sp.respCh.Load(); chp != nil {
					select {
					case *chp <- line:
						continue
					default:
					}
				}

				// Fire-and-forget acks have no waiting consumer; drop silently
				// rather than routing to the events channel (which would fire
				// "unhandled event" warnings on every call).
				if isFireAndForgetAck(line) {
					continue
				}

				// Unrecognized line: drop with a debug log and bump the counter
				// so operators can spot noisy or rogue UART traffic.
				if !isKnownResponsePrefix(line) {
					sp.droppedLines.Add(1)
					sp.logger.Debug("serial: dropping unrecognized UART line",
						"line", line, "total_dropped", sp.droppedLines.Load())
					continue
				}

				// Known response with no pending command. Deliver as event.
				select {
				case sp.events <- line:
				default:
					sp.logger.Warn("serial: events full, dropping", "line", line)
				}
			} else {
				lineBuf.WriteByte(ch)
			}
		}
	}
}
