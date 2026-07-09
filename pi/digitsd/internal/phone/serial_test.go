package phone

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"go.bug.st/serial"
)

// fakePort is an in-memory serial.Port for driving readLoop in tests. Read
// pulls queued chunks or errors; when nothing is queued it returns (0, nil)
// after a short delay, mirroring how go.bug.st/serial reports a read timeout so
// the reader keeps polling (and can observe reopen requests) without blocking.
type fakePort struct {
	reads     chan readEvent
	closed    chan struct{}
	closeOnce sync.Once

	mu     sync.Mutex
	writes [][]byte
}

type readEvent struct {
	data []byte
	err  error
}

func newFakePort() *fakePort {
	return &fakePort{reads: make(chan readEvent, 64), closed: make(chan struct{})}
}

func (f *fakePort) feed(s string)     { f.reads <- readEvent{data: []byte(s)} }
func (f *fakePort) feedErr(err error) { f.reads <- readEvent{err: err} }

func (f *fakePort) Read(p []byte) (int, error) {
	select {
	case ev := <-f.reads:
		if ev.err != nil {
			return 0, ev.err
		}
		return copy(p, ev.data), nil
	case <-f.closed:
		return 0, io.EOF
	case <-time.After(5 * time.Millisecond):
		return 0, nil // idle poll: mirrors a read timeout (0, nil)
	}
}

func (f *fakePort) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	f.writes = append(f.writes, cp)
	return len(p), nil
}

func (f *fakePort) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func (f *fakePort) Drain() error                       { return nil }
func (f *fakePort) ResetInputBuffer() error            { return nil }
func (f *fakePort) ResetOutputBuffer() error           { return nil }
func (f *fakePort) SetDTR(bool) error                  { return nil }
func (f *fakePort) SetRTS(bool) error                  { return nil }
func (f *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (f *fakePort) SetMode(*serial.Mode) error         { return nil }
func (f *fakePort) Break(time.Duration) error          { return nil }
func (f *fakePort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{}, nil
}

// portFactory hands out a scripted sequence of open results (a port or an
// error) so tests can drive the reader's reopen path deterministically.
type portFactory struct {
	mu      sync.Mutex
	results []openResult
	idx     int
}

type openResult struct {
	port *fakePort
	err  error
}

func (pf *portFactory) open() (serial.Port, error) {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	if pf.idx >= len(pf.results) {
		return nil, fmt.Errorf("fake factory exhausted")
	}
	r := pf.results[pf.idx]
	pf.idx++
	if r.err != nil {
		return nil, r.err
	}
	return r.port, nil
}

// newTestSerialPort builds a SerialPort backed by the factory's first port and
// starts its reader, bypassing the real device open in OpenSerial.
func newTestSerialPort(t *testing.T, pf *portFactory) *SerialPort {
	t.Helper()
	sp := &SerialPort{
		open:      pf.open,
		device:    "fake",
		baud:      115200,
		events:    make(chan string, 64),
		reopenReq: make(chan struct{}, 1),
		stop:      make(chan struct{}),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	port, err := sp.open()
	if err != nil {
		t.Fatalf("initial open: %v", err)
	}
	sp.port = port
	sp.linkUp.Store(true)
	go sp.readLoop()
	t.Cleanup(func() { _ = sp.Close() })
	return sp
}

func recvEvent(t *testing.T, sp *SerialPort, timeout time.Duration) string {
	t.Helper()
	select {
	case e := <-sp.Events():
		return e
	case <-time.After(timeout):
		t.Fatal("timed out waiting for event")
		return ""
	}
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestReadLoopLineFramingAcrossReads(t *testing.T) {
	p := newFakePort()
	pf := &portFactory{results: []openResult{{port: p}}}
	sp := newTestSerialPort(t, pf)

	// A single logical line split across two Read calls must reassemble.
	p.feed("HOO")
	p.feed("K:OFF\r\n")
	if got := recvEvent(t, sp, time.Second); got != "HOOK:OFF" {
		t.Fatalf("split line: got %q, want HOOK:OFF", got)
	}

	// Two lines in one chunk, mixed CR/LF terminators, must split cleanly.
	p.feed("KEY:5\r\nKEY:7\n")
	if got := recvEvent(t, sp, time.Second); got != "KEY:5" {
		t.Fatalf("first framed line: got %q, want KEY:5", got)
	}
	if got := recvEvent(t, sp, time.Second); got != "KEY:7" {
		t.Fatalf("second framed line: got %q, want KEY:7", got)
	}
}

func TestReadLoopUnsolicitedEvent(t *testing.T) {
	p := newFakePort()
	pf := &portFactory{results: []openResult{{port: p}}}
	sp := newTestSerialPort(t, pf)

	p.feed("DIAL:5551234\r\n")
	if got := recvEvent(t, sp, time.Second); got != "DIAL:5551234" {
		t.Fatalf("unsolicited event: got %q, want DIAL:5551234", got)
	}
}

func TestReadLoopResponseRouting(t *testing.T) {
	p := newFakePort()
	pf := &portFactory{results: []openResult{{port: p}}}
	sp := newTestSerialPort(t, pf)

	respCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := sp.SendCommand("PING", time.Second)
		respCh <- resp
		errCh <- err
	}()

	// Give SendCommand a moment to register its response channel, then answer.
	waitFor(t, func() bool { return sp.respCh.Load() != nil }, time.Second, "respCh never registered")
	p.feed("PONG\r\n")

	if err := <-errCh; err != nil {
		t.Fatalf("SendCommand error: %v", err)
	}
	if resp := <-respCh; resp != "PONG" {
		t.Fatalf("response routing: got %q, want PONG", resp)
	}

	// A PONG must route to the waiting command, not leak onto the events channel.
	select {
	case e := <-sp.Events():
		t.Fatalf("PONG leaked to events channel: %q", e)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestReadLoopDroppedLineCount(t *testing.T) {
	p := newFakePort()
	pf := &portFactory{results: []openResult{{port: p}}}
	sp := newTestSerialPort(t, pf)

	// Unrecognized line with no pending command: dropped and counted.
	p.feed("GARBAGE:LINE\r\n")
	waitFor(t, func() bool { return sp.DroppedLines() == 1 }, time.Second, "dropped-line count did not reach 1")

	// A known unsolicited event that follows must still be delivered.
	p.feed("HOOK:ON\r\n")
	if got := recvEvent(t, sp, time.Second); got != "HOOK:ON" {
		t.Fatalf("post-drop event: got %q, want HOOK:ON", got)
	}
	if got := sp.DroppedLines(); got != 1 {
		t.Fatalf("dropped count after valid event: got %d, want 1", got)
	}
}

func TestReadLoopReconnectAfterReadError(t *testing.T) {
	p1 := newFakePort()
	p2 := newFakePort()
	// port1, then a transient open failure, then port2: exercises the backoff
	// retry as well as the eventual successful reopen.
	pf := &portFactory{results: []openResult{
		{port: p1},
		{err: errors.New("device busy")},
		{port: p2},
	}}
	sp := newTestSerialPort(t, pf)

	p1.feed("HOOK:OFF\r\n")
	if got := recvEvent(t, sp, time.Second); got != "HOOK:OFF" {
		t.Fatalf("pre-error event: got %q, want HOOK:OFF", got)
	}

	// Kill the link. The reader must not busy-spin: it marks the link down,
	// backs off through the failed reopen, then reopens onto port2.
	p1.feedErr(errors.New("read: i/o error"))
	waitFor(t, func() bool { return !sp.LinkUp() }, time.Second, "link never marked down")
	waitFor(t, func() bool { return sp.Reopens() == 1 }, 3*time.Second, "reader did not reopen onto port2")
	waitFor(t, func() bool { return sp.LinkUp() }, time.Second, "link never marked back up")

	// Events must flow again on the reopened port.
	p2.feed("HOOK:ON\r\n")
	if got := recvEvent(t, sp, time.Second); got != "HOOK:ON" {
		t.Fatalf("post-reconnect event: got %q, want HOOK:ON", got)
	}
}

func TestReadLoopReopenOnRequest(t *testing.T) {
	p1 := newFakePort()
	p2 := newFakePort()
	pf := &portFactory{results: []openResult{{port: p1}, {port: p2}}}
	sp := newTestSerialPort(t, pf)

	// No read error: the liveness check forces a reopen of a silently dead link.
	sp.RequestReopen()
	waitFor(t, func() bool { return sp.Reopens() == 1 }, 2*time.Second, "reopen request not honored")
	waitFor(t, func() bool { return sp.LinkUp() }, time.Second, "link not up after requested reopen")

	p2.feed("KEY:9\r\n")
	if got := recvEvent(t, sp, time.Second); got != "KEY:9" {
		t.Fatalf("post-reopen event: got %q, want KEY:9", got)
	}
}

func TestWritersDoNotPanicWhileLinkDown(t *testing.T) {
	p1 := newFakePort()
	// Only one port is ever handed out. After the read error the reader tries to
	// reopen, the factory is exhausted, and it settles into the backoff loop with
	// sp.port left nil: exactly the window a writer must survive without panicking.
	pf := &portFactory{results: []openResult{{port: p1}}}
	sp := newTestSerialPort(t, pf)

	// Kill the link, then wait until the port is actually nil (checked under mu,
	// so this does not race the reader's reopen). Once the factory is exhausted
	// the down state is stable: only a successful reopen would clear it.
	p1.feedErr(errors.New("read: i/o error"))
	portIsNil := func() bool {
		sp.mu.Lock()
		defer sp.mu.Unlock()
		return sp.port == nil
	}
	waitFor(t, portIsNil, 2*time.Second, "port never became nil after failed reopen")

	// SendCommand must return the link-down sentinel, not deref a nil port.
	_, err := sp.SendCommand("PING", time.Second)
	if !errors.Is(err, errLinkDown) {
		t.Fatalf("SendCommand while link down: got err %v, want errLinkDown", err)
	}

	// SendFire is fire-and-forget: it must return cleanly, no panic.
	sp.SendFire("RING:START")

	// The port is still down, so a second round must behave identically.
	if _, err := sp.SendCommand("VERSION", time.Second); !errors.Is(err, errLinkDown) {
		t.Fatalf("second SendCommand while link down: got err %v, want errLinkDown", err)
	}
	sp.LED("blink")
}

func TestReadLoopDoubleCloseNoPanic(t *testing.T) {
	p := newFakePort()
	pf := &portFactory{results: []openResult{{port: p}}}
	sp := newTestSerialPort(t, pf)

	if err := sp.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// A second Close must not panic on the already-closed stop channel.
	if err := sp.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestIsReadTimeout(t *testing.T) {
	if !isReadTimeout(nil) {
		t.Error("nil should be treated as a timeout (no error)")
	}
	if !isReadTimeout(timeoutErr{}) {
		t.Error("net.Error with Timeout()==true should be a timeout")
	}
	if !isReadTimeout(os.ErrDeadlineExceeded) {
		t.Error("os.ErrDeadlineExceeded should be a timeout")
	}
	if isReadTimeout(errors.New("i/o error")) {
		t.Error("a plain error must not be classified as a timeout")
	}
}

func TestSerialPortInterface(t *testing.T) {
	// Verify the type implements expected interface at compile time
	var _ interface {
		Events() <-chan string
		SendCommand(string, time.Duration) (string, error)
		SendFire(string)
		Ping() error
		StartRing()
		StopRing()
		LED(string)
		AddMonitor(chan string) func()
		Close() error
	} = (*SerialPort)(nil)
}

func TestIsUnsolicitedEvent(t *testing.T) {
	unsolicited := []string{
		"HOOK:OFF", "HOOK:ON",
		"STATUS:READY",
		"KEY:5", "KEY:*", "KEY:#",
		"DIAL:5551234",
		"FSM:IDLE", "FSM:DIALING", "FSM:RINGING",
	}
	for _, msg := range unsolicited {
		if !isUnsolicitedEvent(msg) {
			t.Errorf("expected %q to be unsolicited", msg)
		}
	}

	commandResponses := []string{
		"PONG",
		"VERSION:1.0.0:abc1234",
		"RING:ACK", "RING:DONE", "RING:TEST:ACK",
		"HOOK:FORCED:ON_HOOK", "HOOK:FORCED:OFF_HOOK",
		"HOOK:RELEASED",
		"HOOK:INVERT:ON", "HOOK:INVERT:OFF",
		"RST:OK",
		"STATE:IDLE",
		"MODE:KEYTEST", "MODE:NORMAL",
		// Ack for the fire-and-forget DIAL:RESET command. Shares the DIAL:
		// prefix but must not be classified as an unsolicited dialed number.
		"DIAL:RESET:OK",
	}
	for _, msg := range commandResponses {
		if isUnsolicitedEvent(msg) {
			t.Errorf("expected %q to be a command response, not unsolicited", msg)
		}
	}
}

func TestIsFireAndForgetAck(t *testing.T) {
	acks := []string{
		"HOOK:FLASH:ON", "HOOK:FLASH:OFF",
		"CALL:CONNECTED:ACK", "CALL:CONNECTED:IGNORED",
		// DIAL:RESET is sent fire-and-forget, so its ack has no waiting
		// consumer and must be dropped here rather than routed as an event.
		"DIAL:RESET:OK",
	}
	for _, msg := range acks {
		if !isFireAndForgetAck(msg) {
			t.Errorf("expected %q to be a fire-and-forget ack", msg)
		}
	}

	notAcks := []string{
		"HOOK:OFF", "HOOK:ON", "HOOK:FLASH",
		"PONG", "STATUS:READY",
		"RING:ACK", "RING:DONE",
		"VERSION:1.0.0:abc1234",
		"KEY:5", "DIAL:5551234", "FSM:IDLE",
	}
	for _, msg := range notAcks {
		if isFireAndForgetAck(msg) {
			t.Errorf("expected %q to NOT be a fire-and-forget ack", msg)
		}
	}
}
