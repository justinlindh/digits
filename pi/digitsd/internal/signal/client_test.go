package signal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func wsURL(s *httptest.Server) string {
	return strings.Replace(s.URL, "http", "ws", 1) + "/ws"
}

// TestClient_ConnectAndRegister verifies that Connect() sends a register message.
func TestClient_ConnectAndRegister(t *testing.T) {
	registered := make(chan *Message, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read: %v", err)
			return
		}
		msg, err := ParseMessage(data)
		if err != nil {
			t.Errorf("parse: %v", err)
			return
		}
		registered <- msg
		// Keep connection alive briefly
		time.Sleep(100 * time.Millisecond)
	}))
	defer ts.Close()

	c := NewClient(wsURL(ts), "+15550001111", "test-hw-id", "")
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	select {
	case msg := <-registered:
		if msg.Type != TypeRegister {
			t.Errorf("expected type %q, got %q", TypeRegister, msg.Type)
		}
		if msg.Number != "+15550001111" {
			t.Errorf("expected number %q, got %q", "+15550001111", msg.Number)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for register message")
	}
}

// TestClient_ReceiveMessages verifies that incoming messages appear on Inbox().
func TestClient_ReceiveMessages(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		// Consume the register message
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read register: %v", err)
			return
		}

		// Send a ring message back
		ring := &Message{Type: TypeRing, From: "+15559998888"}
		data, _ := json.Marshal(ring)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			t.Errorf("write ring: %v", err)
		}
		// Keep connection alive
		time.Sleep(200 * time.Millisecond)
	}))
	defer ts.Close()

	c := NewClient(wsURL(ts), "+15550001111", "test-hw-id", "")
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	select {
	case msg := <-c.Inbox():
		if msg.Type != TypeRing {
			t.Errorf("expected type %q, got %q", TypeRing, msg.Type)
		}
		if msg.From != "+15559998888" {
			t.Errorf("expected from %q, got %q", "+15559998888", msg.From)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ring message")
	}
}

// TestClient_ConnectFailureClosesDone verifies that a failed Connect() closes
// the Done() channel so callers can detect the failure and retry.
func TestClient_ConnectFailureClosesDone(t *testing.T) {
	c := NewClient("ws://127.0.0.1:1/ws", "+15550001111", "test-hw-id", "")
	if err := c.Connect(); err == nil {
		t.Fatal("expected Connect to fail on unreachable host")
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() channel not closed after Connect failure")
	}
}

// TestClient_ReadPumpClosesConnOnDrop verifies the read pump releases the
// socket when the server drops the connection: by the time Done() fires the
// underlying conn is closed, so a Send fails instead of writing into an
// orphaned socket that would otherwise linger until a GC finalizer.
func TestClient_ReadPumpClosesConnOnDrop(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil { // consume register
			t.Errorf("read: %v", err)
		}
		_ = conn.Close() // server-side drop without a close frame
	}))
	defer ts.Close()

	c := NewClient(wsURL(ts), "+15550001111", "test-hw-id", "")
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done() channel not closed after server drop")
	}

	if err := c.Send(&Message{Type: TypeRegister}); err == nil {
		t.Fatal("Send succeeded on a dropped connection; read pump left the socket open")
	}
}

// TestClient_SendMessage verifies that Send() delivers a message to the server.
func TestClient_SendMessage(t *testing.T) {
	received := make(chan *Message, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		// Consume register
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read register: %v", err)
			return
		}

		// Read next message (the one the client sends)
		_, data, err := conn.ReadMessage()
		if err != nil {
			// Connection may close before we read — that's fine if we already got the msg
			return
		}
		msg, err := ParseMessage(data)
		if err != nil {
			t.Errorf("parse: %v", err)
			return
		}
		received <- msg
	}))
	defer ts.Close()

	c := NewClient(wsURL(ts), "+15550001111", "test-hw-id", "")
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Small delay to let register complete before sending
	time.Sleep(50 * time.Millisecond)

	callMsg := &Message{Type: TypeCall, To: "+15559998888"}
	if err := c.Send(callMsg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case msg := <-received:
		if msg.Type != TypeCall {
			t.Errorf("expected type %q, got %q", TypeCall, msg.Type)
		}
		if msg.To != "+15559998888" {
			t.Errorf("expected to %q, got %q", "+15559998888", msg.To)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for call message")
	}
}
