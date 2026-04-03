package phone

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

// SocketHandler processes commands received on the Unix socket.
type SocketHandler interface {
	HandleSocketCommand(cmd string) string
}

// SocketServer listens on a Unix socket and dispatches commands.
// Provides a Unix socket API for external tools (latclient, debugging).
type SocketServer struct {
	listener net.Listener
	handler  SocketHandler
	stop     chan struct{}
}

// NewSocketServer creates and starts a Unix socket server.
func NewSocketServer(path string, handler SocketHandler) (*SocketServer, error) {
	os.Remove(path) // clean up stale socket
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, fmt.Errorf("chmod socket %s: %w", path, err)
	}

	s := &SocketServer{
		listener: listener,
		handler:  handler,
		stop:     make(chan struct{}),
	}

	go s.acceptLoop()
	log.Printf("socket server: listening on %s", path)
	return s, nil
}

// Close shuts down the socket server.
func (s *SocketServer) Close() {
	close(s.stop)
	s.listener.Close()
}

func (s *SocketServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stop:
				return
			default:
				log.Printf("socket: accept error: %v", err)
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *SocketServer) handleConn(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}
	cmd := strings.TrimSpace(scanner.Text())
	if cmd == "" {
		conn.Write([]byte("\n"))
		return
	}

	resp := s.handler.HandleSocketCommand(cmd)
	conn.Write([]byte(resp + "\n"))
}
