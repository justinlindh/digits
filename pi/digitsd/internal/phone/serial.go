package phone

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// SerialPort owns /dev/serial0 and provides thread-safe read/write.
// Owns the serial port to the Pico — reads UART events, sends commands.
type SerialPort struct {
	port   serial.Port
	events chan string // parsed RX events (HOOK:OFF, KEY:5, etc.)

	mu     sync.Mutex
	respCh chan string // single-slot response channel for command/response pairs
	stop   chan struct{}
	logger *log.Logger
}

// OpenSerial opens the serial port and starts the RX reader goroutine.
func OpenSerial(device string, baud int, logger *log.Logger) (*SerialPort, error) {
	mode := &serial.Mode{BaudRate: baud}
	port, err := serial.Open(device, mode)
	if err != nil {
		return nil, fmt.Errorf("serial open %s: %w", device, err)
	}
	if err := port.SetReadTimeout(100 * time.Millisecond); err != nil {
		port.Close()
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
	sp.respCh = ch
	defer func() { sp.respCh = nil }()

	sp.logger.Printf("TX: %s", cmd)
	if _, err := sp.port.Write([]byte(cmd + "\r\n")); err != nil {
		return "", fmt.Errorf("serial write: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(timeout):
		return "", nil
	}
}

// SendFire sends a command without waiting for response (fire-and-forget).
func (sp *SerialPort) SendFire(cmd string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.logger.Printf("TX: %s", cmd)
	sp.port.Write([]byte(cmd + "\r\n"))
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

// Close stops the reader and closes the port.
func (sp *SerialPort) Close() error {
	close(sp.stop)
	return sp.port.Close()
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

				sp.logger.Printf("RX: %s", line)

				// If there's a pending command waiting for response, deliver to it
				if sp.respCh != nil {
					select {
					case sp.respCh <- line:
						continue
					default:
					}
				}

				// Otherwise, deliver as event
				select {
				case sp.events <- line:
				default:
					sp.logger.Printf("serial: events full, dropping: %s", line)
				}
			} else {
				lineBuf.WriteByte(ch)
			}
		}
	}
}
