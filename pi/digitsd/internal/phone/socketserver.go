package phone

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"
)

// SocketHandler processes commands received on the Unix socket.
type SocketHandler interface {
	HandleSocketCommand(cmd string) string
}

// MonitorHandler is implemented by handlers that can support the MONITOR
// streaming mode. When the first line on a socket connection is "MONITOR",
// the server upgrades the connection to a long-lived bidirectional stream:
// it registers a tap on the underlying serial port (every "> cmd" TX and
// "< line" RX is mirrored to the client) and forwards any subsequent lines
// the client writes through SendRaw, which goes out to the Pico verbatim
// without expecting a response.
//
// This lets an interactive UART terminal share /dev/serial0 with digitsd
// instead of having to stop the daemon to grab the port.
type MonitorHandler interface {
	AddSerialMonitor(ch chan string) func()
	SendRaw(cmd string)
}

// SocketServer listens on a Unix socket and dispatches commands.
// Provides a Unix socket API for external tools (latclient, debugging,
// digits-pico-monitor).
type SocketServer struct {
	listener net.Listener
	handler  SocketHandler
	stop     chan struct{}
}

// NewSocketServer creates and starts a Unix socket server.
func NewSocketServer(path string, handler SocketHandler) (*SocketServer, error) {
	_ = os.Remove(path) // clean up stale socket
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("chmod socket %s: %w", path, err)
	}

	s := &SocketServer{
		listener: listener,
		handler:  handler,
		stop:     make(chan struct{}),
	}

	go s.acceptLoop()
	slog.Info("socket server: listening", "path", path)
	return s, nil
}

// Close shuts down the socket server.
func (s *SocketServer) Close() {
	close(s.stop)
	_ = s.listener.Close()
}

func (s *SocketServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stop:
				return
			default:
				slog.Error("socket: accept error", "error", err)
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *SocketServer) handleConn(conn net.Conn) {
	// We need to peek the first line before deciding which mode this
	// connection is in. The default mode (one-shot command/response) has
	// a 5-second deadline; MONITOR has none. Set the short deadline only
	// for reading the first line so a stalled client can't pin the
	// goroutine forever.
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		slog.Warn("socket: set deadline", "error", err)
		_ = conn.Close()
		return
	}

	reader := bufio.NewReader(conn)
	first, err := reader.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return
	}
	first = strings.TrimSpace(first)

	if first == "MONITOR" {
		s.handleMonitor(conn, reader)
		return
	}

	defer func() { _ = conn.Close() }()
	if first == "" {
		if _, err := conn.Write([]byte("\n")); err != nil {
			slog.Warn("socket: write", "error", err)
		}
		return
	}

	// Restore the original write deadline behavior for one-shot mode.
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		slog.Warn("socket: set deadline", "error", err)
		return
	}

	resp := s.handler.HandleSocketCommand(first)
	if _, err := conn.Write([]byte(resp + "\n")); err != nil {
		slog.Warn("socket: write response", "error", err)
	}
}

// handleMonitor upgrades the connection to a streaming UART tap. The
// client sees every TX/RX line on the wire and can inject additional
// commands by writing newline-terminated strings.
//
// The connection lives until either side closes it. We keep two
// goroutines: one pumps the monitor channel out to the client, the other
// (this one) reads further lines from the client.
func (s *SocketServer) handleMonitor(conn net.Conn, reader *bufio.Reader) {
	mh, ok := s.handler.(MonitorHandler)
	if !ok {
		_, _ = conn.Write([]byte("ERROR: MONITOR not supported by this handler\n"))
		_ = conn.Close()
		return
	}

	// Buffered enough to absorb a small burst (KEYTEST stream, hook
	// chatter) without dropping. broadcastMonitor drops on full so a
	// stalled client cannot back-pressure the serial reader.
	tap := make(chan string, 64)
	unsub := mh.AddSerialMonitor(tap)

	// Clear deadlines: this connection is long-lived.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		slog.Warn("socket: clear deadline", "error", err)
	}

	if _, err := conn.Write([]byte("MONITOR:READY\n")); err != nil {
		unsub()
		_ = conn.Close()
		return
	}

	done := make(chan struct{})

	// Writer: drain tap, push to client. Exits when tap is closed (we
	// close it from the reader goroutine on shutdown).
	go func() {
		defer close(done)
		for line := range tap {
			if _, err := conn.Write([]byte(line + "\n")); err != nil {
				return
			}
		}
	}()

	// Reader: every subsequent line from the client is forwarded to the
	// Pico verbatim via SendRaw (fire-and-forget; responses arrive via
	// the tap like any other RX).
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "QUIT" || line == "EXIT" {
			break
		}
		mh.SendRaw(line)
	}

	unsub()
	close(tap)
	<-done
	_ = conn.Close()
}
