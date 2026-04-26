package phone

import (
	"bufio"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeHandler implements both SocketHandler and MonitorHandler for tests.
type fakeHandler struct {
	mu       sync.Mutex
	cmds     []string // commands seen by HandleSocketCommand
	rawCmds  []string // commands seen by SendRaw (MONITOR mode)
	monitors map[chan string]struct{}
}

func newFakeHandler() *fakeHandler {
	return &fakeHandler{monitors: make(map[chan string]struct{})}
}

func (f *fakeHandler) HandleSocketCommand(cmd string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	return "OK:" + cmd
}

func (f *fakeHandler) AddSerialMonitor(ch chan string) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.monitors[ch] = struct{}{}
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.monitors, ch)
	}
}

func (f *fakeHandler) SendRaw(cmd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rawCmds = append(f.rawCmds, cmd)
}

// emit pushes a synthetic line out to every active monitor.
func (f *fakeHandler) emit(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.monitors {
		select {
		case ch <- line:
		default:
		}
	}
}

func startServer(t *testing.T, h SocketHandler) (string, *SocketServer) {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv, err := NewSocketServer(sockPath, h)
	if err != nil {
		t.Fatalf("NewSocketServer: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	return sockPath, srv
}

func TestSocketServer_OneShotCommand(t *testing.T) {
	h := newFakeHandler()
	sockPath, _ := startServer(t, h)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("PING\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp != "OK:PING\n" {
		t.Errorf("got %q, want %q", resp, "OK:PING\n")
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.cmds) != 1 || h.cmds[0] != "PING" {
		t.Errorf("expected handler to see PING, got %v", h.cmds)
	}
}

func TestSocketServer_MonitorStreamsAndInjects(t *testing.T) {
	h := newFakeHandler()
	sockPath, _ := startServer(t, h)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("MONITOR\n")); err != nil {
		t.Fatalf("write MONITOR: %v", err)
	}
	r := bufio.NewReader(conn)
	ready, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read ready: %v", err)
	}
	if ready != "MONITOR:READY\n" {
		t.Fatalf("got ready=%q", ready)
	}

	// Wait for the monitor to actually be registered. The server adds it
	// after writing READY, but the order in handleMonitor is:
	// AddSerialMonitor -> Write READY. So once we have READY the monitor
	// is registered.
	deadline := time.Now().Add(time.Second)
	for {
		h.mu.Lock()
		n := len(h.monitors)
		h.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("monitor never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Server -> client: emit a synthetic line and expect it on the wire.
	h.emit("< KEY:5")
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	got, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read tap line: %v", err)
	}
	if got != "< KEY:5\n" {
		t.Errorf("got %q, want %q", got, "< KEY:5\n")
	}

	// Client -> server: inject a command and expect SendRaw to see it.
	if _, err := conn.Write([]byte("PING\n")); err != nil {
		t.Fatalf("write inject: %v", err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		h.mu.Lock()
		seen := append([]string(nil), h.rawCmds...)
		h.mu.Unlock()
		if len(seen) == 1 && seen[0] == "PING" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("SendRaw not invoked, got %v", seen)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// QUIT line tears the connection down.
	if _, err := conn.Write([]byte("QUIT\n")); err != nil {
		t.Fatalf("write quit: %v", err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		h.mu.Lock()
		n := len(h.monitors)
		h.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("monitor never unregistered after QUIT")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Verify the one-shot path still rejects MONITOR if the handler doesn't
// implement MonitorHandler. (This protects callers that wire only the
// command interface.)
func TestSocketServer_MonitorRejectedWithoutMonitorHandler(t *testing.T) {
	h := &struct{ SocketHandler }{SocketHandler: &simpleHandler{}}
	sockPath, _ := startServer(t, h)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("MONITOR\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp != "ERROR: MONITOR not supported by this handler\n" {
		t.Errorf("got %q", resp)
	}
}

type simpleHandler struct{}

func (s *simpleHandler) HandleSocketCommand(cmd string) string { return "OK" }
