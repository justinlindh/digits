package phone

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startFakeServer starts a Unix socket server that handles one connection using the provided handler.
// Returns the socket path and a cleanup function.
func startFakeServer(t *testing.T, handler func(conn net.Conn)) string {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		defer func() { _ = ln.Close() }()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		handler(conn)
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		_ = os.Remove(sockPath)
	})

	return sockPath
}

// echoServer reads one line and responds with a fixed reply.
func respondServer(reply string) func(conn net.Conn) {
	return func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		scanner.Scan() // read the command line
		_, _ = fmt.Fprintf(conn, "%s\n", reply)
	}
}

func TestUARTClient_SendCommand(t *testing.T) {
	sockPath := startFakeServer(t, respondServer("PONG"))
	// Give the server a moment to start listening
	time.Sleep(10 * time.Millisecond)

	client := NewUARTClient(sockPath)
	resp, err := client.SendCommand("PING", 2*time.Second)
	if err != nil {
		t.Fatalf("SendCommand failed: %v", err)
	}
	if resp != "PONG" {
		t.Errorf("expected PONG, got %q", resp)
	}
}

func TestUARTClient_Timeout(t *testing.T) {
	// Server accepts but never responds
	sockPath := startFakeServer(t, func(conn net.Conn) {
		// Read the command but never write a response
		buf := make([]byte, 64)
		_, _ = conn.Read(buf)
		time.Sleep(10 * time.Second) // hold connection open
	})
	time.Sleep(10 * time.Millisecond)

	client := NewUARTClient(sockPath)
	_, err := client.SendCommand("PING", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestUARTClient_Ring(t *testing.T) {
	tests := []struct {
		start   bool
		wantCmd string
	}{
		{true, "RING:START"},
		{false, "RING:STOP"},
	}

	for _, tt := range tests {
		t.Run(tt.wantCmd, func(t *testing.T) {
			var gotCmd string
			sockPath := startFakeServer(t, func(conn net.Conn) {
				scanner := bufio.NewScanner(conn)
				if scanner.Scan() {
					gotCmd = scanner.Text()
				}
				_, _ = fmt.Fprintf(conn, "OK\n")
			})
			time.Sleep(10 * time.Millisecond)

			client := NewUARTClient(sockPath)
			err := client.Ring(tt.start)
			if err != nil {
				t.Fatalf("Ring(%v) failed: %v", tt.start, err)
			}
			if gotCmd != tt.wantCmd {
				t.Errorf("expected command %q, got %q", tt.wantCmd, gotCmd)
			}
		})
	}
}

func TestUARTClient_Ping(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		sockPath := startFakeServer(t, respondServer("PONG"))
		time.Sleep(10 * time.Millisecond)

		client := NewUARTClient(sockPath)
		if err := client.Ping(); err != nil {
			t.Errorf("Ping() returned error: %v", err)
		}
	})

	t.Run("wrong_response", func(t *testing.T) {
		sockPath := startFakeServer(t, respondServer("NOPE"))
		time.Sleep(10 * time.Millisecond)

		client := NewUARTClient(sockPath)
		if err := client.Ping(); err == nil {
			t.Error("Ping() expected error on wrong response, got nil")
		}
	})
}
