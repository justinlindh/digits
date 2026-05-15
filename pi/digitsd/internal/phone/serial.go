package phone

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.bug.st/serial"
)

// SerialPort owns /dev/serial0 and provides thread-safe read/write.
// Owns the serial port to the Pico: reads UART events, sends commands.
type SerialPort struct {
	port   serial.Port
	events chan string // parsed RX events (HOOK:OFF, KEY:5, etc.)

	mu           sync.Mutex
	respCh       atomic.Pointer[chan string] // single-slot response channel for command/response pairs
	flashEnabled atomic.Bool                 // whether HOOK:FLASH should be forwarded (requires firmware v1.5.0+)
	droppedLines atomic.Int64                // count of unrecognized UART lines dropped
	stop         chan struct{}
	logger       *slog.Logger

	monitorMu sync.Mutex
	monitors  map[chan string]struct{} // tap subscribers (e.g. interactive UART terminal)
}

// OpenSerial opens the serial port and starts the RX reader goroutine.
func OpenSerial(device string, baud int, logger *slog.Logger) (*SerialPort, error) {
	mode := &serial.Mode{BaudRate: baud}
	port, err := serial.Open(device, mode)
	if err != nil {
		return nil, fmt.Errorf("serial open %s: %w", device, err)
	}
	if err := port.SetReadTimeout(100 * time.Millisecond); err != nil {
		_ = port.Close()
		return nil, fmt.Errorf("serial set timeout: %w", err)
	}

	sp := &SerialPort{
		port:   port,
		events: make(chan string, 64),
		stop:   make(chan struct{}),
		logger: logger,
	}

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

// Ring sends RING:START or RING:STOP to the Pico.
func (sp *SerialPort) Ring(start bool) {
	if start {
		sp.SendFire("RING:START")
	} else {
		sp.SendFire("RING:STOP")
	}
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

// FlashEnabled toggles flash-window detection on the Pico. When disabled,
// hangup is instantaneous; when enabled the Pico waits up to 600ms after on-hook
// to distinguish a flash from a hangup. Pi enables it only while in a call.
func (sp *SerialPort) FlashEnabled(enabled bool) {
	if enabled {
		sp.SendFire("HOOK:FLASH:ON")
	} else {
		sp.SendFire("HOOK:FLASH:OFF")
	}
}

// Close stops the reader and closes the port.
func (sp *SerialPort) Close() error {
	close(sp.stop)
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
	case strings.HasPrefix(line, "DIAL:"):
		return true
	case strings.HasPrefix(line, "FSM:"):
		return true
	default:
		return false
	}
}

// isFireAndForgetAck returns true for protocol acks the Pico emits in
// response to fire-and-forget commands (HOOK:FLASH:ON/OFF, CALL:CONNECTED).
// These have no waiting consumer on the Pi side. Without this filter they
// fall through to the events channel and trigger spurious "unhandled event"
// warnings for every call.
func isFireAndForgetAck(line string) bool {
	switch line {
	case "HOOK:FLASH:ON", "HOOK:FLASH:OFF":
		return true
	case "CALL:CONNECTED:ACK", "CALL:CONNECTED:IGNORED":
		return true
	case "STATE:SET:OK":
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
	case line == "RST:OK", line == "REBOOT:OK", line == "DIAL:RESET:OK":
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

func (sp *SerialPort) readLoop() {
	buf := make([]byte, 256)
	var lineBuf strings.Builder

	for {
		select {
		case <-sp.stop:
			return
		default:
		}

		n, err := sp.port.Read(buf)
		if err != nil {
			select {
			case <-sp.stop:
				return
			default:
				// Read timeout, normal, just retry
				continue
			}
		}

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
