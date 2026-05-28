package signaling

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestStartDrainingBlocksNewRegistrations(t *testing.T) {
	hub := NewHub()
	conn := &Conn{Send: make(chan []byte, 1)}
	_ = hub.Register("3140001", conn)

	hub.StartDraining()

	if !hub.IsDraining() {
		t.Fatal("expected IsDraining to return true after StartDraining")
	}

	newConn := &Conn{Send: make(chan []byte, 1)}
	err := hub.Register("3140002", newConn)
	if !errors.Is(err, ErrDraining) {
		t.Fatalf("expected ErrDraining, got %v", err)
	}

	// Original connection should still be present.
	if hub.Get("3140001") == nil {
		t.Fatal("expected original connection to remain")
	}
	// Rejected connection should not be in the hub.
	if hub.Get("3140002") != nil {
		t.Fatal("expected rejected connection to not be registered")
	}
}

func TestIsDrainingDefaultsFalse(t *testing.T) {
	hub := NewHub()
	if hub.IsDraining() {
		t.Fatal("new hub should not be draining")
	}
}

func TestDrainAndCloseNoConnections(t *testing.T) {
	hub := NewHub()
	hub.StartDraining()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Should return immediately without error.
	hub.DrainAndClose(ctx)
}

func TestDrainAndCloseSendsCloseFrame(t *testing.T) {
	hub := NewHub()

	// Channel signalled when the server-side read pump exits and unregisters.
	unregistered := make(chan struct{})

	// Start a test WebSocket server that registers with the hub.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		conn := &Conn{
			WS:   ws,
			Send: make(chan []byte, 32),
		}
		_ = hub.Register("3140001", conn)

		// Read pump: exits on close frame or error.
		for {
			_, _, err := ws.ReadMessage()
			if err != nil {
				break
			}
		}
		hub.Unregister("3140001", conn)
		close(unregistered)
	}))
	defer srv.Close()

	// Connect a WebSocket client.
	wsURL := "ws" + srv.URL[len("http"):]
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = clientWS.Close() }()

	// Wait for the server to register the connection.
	deadline := time.Now().Add(2 * time.Second)
	for hub.Get("3140001") == nil {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for connection to register")
		}
		time.Sleep(10 * time.Millisecond)
	}

	hub.StartDraining()

	// Track whether the client receives a close frame.
	closeCh := make(chan int, 1)
	clientWS.SetCloseHandler(func(code int, _ string) error {
		closeCh <- code
		// Respond with a close frame so the server side detects the close.
		msg := websocket.FormatCloseMessage(code, "")
		_ = clientWS.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
		return nil
	})

	drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// DrainAndClose in background so we can read the close frame on the client.
	done := make(chan struct{})
	go func() {
		hub.DrainAndClose(drainCtx)
		close(done)
	}()

	// Client must read to receive the close frame.
	_, _, _ = clientWS.ReadMessage()

	select {
	case code := <-closeCh:
		if code != websocket.CloseGoingAway {
			t.Errorf("expected close code %d (Going Away), got %d", websocket.CloseGoingAway, code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for close frame")
	}

	// Wait for server-side unregister so DrainAndClose can observe zero conns.
	select {
	case <-unregistered:
	case <-time.After(3 * time.Second):
		t.Fatal("server-side unregister did not happen")
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("DrainAndClose did not return")
	}
}

func TestDrainAndCloseForceClosesOnDeadline(t *testing.T) {
	hub := NewHub()

	// Register a connection with no real WebSocket (Send-only fake).
	conn := &Conn{Send: make(chan []byte, 1)}
	_ = hub.Register("3140001", conn)

	hub.StartDraining()

	// Use a very short deadline so forceCloseAll triggers quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	hub.DrainAndClose(ctx)

	// The fake connection has no WS, so force-close is a no-op on the socket,
	// but DrainAndClose should still return without hanging.
}
