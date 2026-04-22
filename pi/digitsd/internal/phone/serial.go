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
// Owns the serial port to the Pico — reads UART events, sends commands.
type SerialPort struct {
	port   serial.Port
	events chan string // parsed RX events (HOOK:OFF, KEY:5, etc.)

	mu           sync.Mutex
	respCh       atomic.Pointer[chan string] // single-slot response channel for command/response pairs
	flashEnabled atomic.Bool                 // whether HOOK:FLASH should be forwarded (requires firmware v1.5.0+)
	stop         chan struct{}
	logger       *slog.Logger
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

// SendCommand sends a command and waits for a response line.
func (sp *SerialPort) SendCommand(cmd string, timeout time.Duration) (string, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	ch := make(chan string, 1)
	sp.respCh.Store(&ch)
	defer sp.respCh.Store(nil)

	sp.logger.Info("TX", "cmd", cmd)
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
func (sp *SerialPort) SendFire(cmd string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.logger.Info("TX", "cmd", cmd)
	if _, err := sp.port.Write([]byte(cmd + "\r\n")); err != nil {
		sp.logger.Warn("serial: write failed", "cmd", cmd, "error", err)
	}
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

// SetFlashEnabled toggles whether HOOK:FLASH events emitted by the Pico should
// be forwarded through to the controller. When disabled (pre-v1.5.0 firmware),
// any stray HOOK:FLASH is dropped with a warning log.
func (sp *SerialPort) SetFlashEnabled(v bool) {
	sp.flashEnabled.Store(v)
}

func (sp *SerialPort) flashEnabledNow() bool {
	return sp.flashEnabled.Load()
}

// Ring sends RING:START or RING:STOP to the Pico.
func (sp *SerialPort) Ring(start bool) {
	if start {
		sp.SendFire("RING:START")
	} else {
		sp.SendFire("RING:STOP")
	}
}

// LED sends LED:<mode> to the Pico.
func (sp *SerialPort) LED(mode string) {
	sp.SendFire("LED:" + mode)
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
				// Read timeout — normal, just retry
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

				// Unsolicited Pico events (hook, keypad, boot) always go to
				// the events channel, never to a pending command response.
				if isUnsolicitedEvent(line) {
					if line == "HOOK:FLASH" && !sp.flashEnabledNow() {
						sp.logger.Warn("HOOK:FLASH received but firmware is not flash-capable; ignoring")
						continue
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

				// No pending command — deliver as event (shouldn't normally happen).
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
