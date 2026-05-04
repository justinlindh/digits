package phonekit

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Event is a decoded line from the Pico firmware.
type Event struct {
	Type  string // "KEY", "HOOK"
	Value string // "0"-"9", "*", "#" for KEY; "ON", "OFF" for HOOK
}

// Serial communicates with the Pico firmware over a serial port.
type Serial struct {
	port   io.ReadWriteCloser
	events chan Event
	stop   chan struct{}
	mu     sync.Mutex
	respCh atomic.Pointer[chan string]
}

// Open opens the serial device at baud and starts the read loop.
func Open(device string, baud int) (*Serial, error) {
	port, err := openSerialPort(device, baud)
	if err != nil {
		return nil, err
	}
	return newSerial(port), nil
}

func newSerial(port io.ReadWriteCloser) *Serial {
	s := &Serial{
		port:   port,
		events: make(chan Event, 16),
		stop:   make(chan struct{}),
	}
	go s.readLoop()
	return s
}

// Events returns the channel of decoded firmware events.
func (s *Serial) Events() <-chan Event {
	return s.events
}

// SendCommand writes cmd followed by a newline and waits up to timeout for a
// non-event response line. Returns the response or an error if the deadline
// expires.
func (s *Serial) SendCommand(cmd string, timeout time.Duration) (string, error) {
	ch := make(chan string, 1)
	s.respCh.Store(&ch)
	defer s.respCh.Store(nil)

	s.mu.Lock()
	_, err := fmt.Fprintf(s.port, "%s\n", cmd)
	s.mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("write: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("timeout waiting for response to %q", cmd)
	}
}

// SendFire writes cmd followed by a newline without waiting for a response.
func (s *Serial) SendFire(cmd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := fmt.Fprintf(s.port, "%s\n", cmd)
	return err
}

// Close stops the read loop and closes the underlying port.
func (s *Serial) Close() error {
	close(s.stop)
	return s.port.Close()
}

func (s *Serial) readLoop() {
	for {
		scanner := bufio.NewScanner(s.port)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if ev, ok := parseEvent(line); ok {
				select {
				case s.events <- ev:
				default:
				}
				continue
			}
			if chp := s.respCh.Load(); chp != nil {
				select {
				case *chp <- line:
				default:
				}
			}
		}
		select {
		case <-s.stop:
			return
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// parseEvent parses a KEY:x or HOOK:ON/HOOK:OFF line into an Event.
func parseEvent(line string) (Event, bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return Event{}, false
	}
	typ, val := parts[0], parts[1]
	switch typ {
	case "KEY":
		switch val {
		case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "*", "#":
			return Event{Type: "KEY", Value: val}, true
		}
	case "HOOK":
		switch val {
		case "ON", "OFF":
			return Event{Type: "HOOK", Value: val}, true
		}
	}
	return Event{}, false
}
