package phonekit

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockPort separates read and write paths so commands written by SendCommand
// do not loop back to the read loop. Reads drain from rxBuf; writes go to
// txBuf. When rxBuf is empty, Read returns 0 bytes (simulating the VTIME=2
// serial timeout). Close signals done so the read loop exits.
type mockPort struct {
	mu     sync.Mutex
	rxBuf  bytes.Buffer
	txBuf  bytes.Buffer
	done   chan struct{}
	closed bool
}

func newMockPort() *mockPort {
	return &mockPort{done: make(chan struct{})}
}

func (m *mockPort) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.txBuf.Write(p)
}

func (m *mockPort) Read(p []byte) (int, error) {
	m.mu.Lock()
	n, _ := m.rxBuf.Read(p)
	m.mu.Unlock()
	if n > 0 {
		return n, nil
	}
	select {
	case <-m.done:
		return 0, io.EOF
	default:
		return 0, nil
	}
}

func (m *mockPort) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.done)
	}
	return nil
}

// feed queues data to be returned by subsequent Read calls.
func (m *mockPort) feed(s string) {
	m.mu.Lock()
	m.rxBuf.WriteString(s)
	m.mu.Unlock()
}

// sent returns everything written to the port by the serial layer.
func (m *mockPort) sent() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.txBuf.String()
}

func TestEventParsing(t *testing.T) {
	cases := []struct {
		line  string
		ok    bool
		event Event
	}{
		{"KEY:5", true, Event{Type: "KEY", Value: "5"}},
		{"KEY:0", true, Event{Type: "KEY", Value: "0"}},
		{"KEY:*", true, Event{Type: "KEY", Value: "*"}},
		{"KEY:#", true, Event{Type: "KEY", Value: "#"}},
		{"HOOK:OFF", true, Event{Type: "HOOK", Value: "OFF"}},
		{"HOOK:ON", true, Event{Type: "HOOK", Value: "ON"}},
		{"PONG", false, Event{}},
		{"STATE:SET:OK", false, Event{}},
		{"", false, Event{}},
		{"KEY:A", false, Event{}},
		{"HOOK:MAYBE", false, Event{}},
	}
	for _, c := range cases {
		ev, ok := parseEvent(c.line)
		if ok != c.ok {
			t.Errorf("parseEvent(%q): got ok=%v, want %v", c.line, ok, c.ok)
		}
		if ok && ev != c.event {
			t.Errorf("parseEvent(%q): got %+v, want %+v", c.line, ev, c.event)
		}
	}
}

func TestEventDelivery(t *testing.T) {
	port := newMockPort()
	s := newSerial(port)
	defer func() { _ = s.Close() }()

	lines := []string{"KEY:5\n", "HOOK:OFF\n", "HOOK:ON\n", "KEY:*\n"}
	want := []Event{
		{Type: "KEY", Value: "5"},
		{Type: "HOOK", Value: "OFF"},
		{Type: "HOOK", Value: "ON"},
		{Type: "KEY", Value: "*"},
	}

	for _, l := range lines {
		port.feed(l)
	}

	for i, w := range want {
		select {
		case ev := <-s.Events():
			if ev != w {
				t.Errorf("event %d: got %+v, want %+v", i, ev, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for event %d", i)
		}
	}
}

func TestSendCommand(t *testing.T) {
	port := newMockPort()
	s := newSerial(port)
	defer func() { _ = s.Close() }()

	go func() {
		time.Sleep(20 * time.Millisecond)
		port.feed("PONG\n")
	}()

	resp, err := s.SendCommand("PING", time.Second)
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if resp != "PONG" {
		t.Errorf("got response %q, want %q", resp, "PONG")
	}

	if !strings.Contains(port.sent(), "PING\n") {
		t.Errorf("port did not receive PING command; sent=%q", port.sent())
	}
}

func TestSendCommandTimeout(t *testing.T) {
	port := newMockPort()
	s := newSerial(port)
	defer func() { _ = s.Close() }()

	_, err := s.SendCommand("PING", 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
