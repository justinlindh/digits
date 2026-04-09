package signaling

import "testing"

func TestUnregisterOnlyRemovesMatchingConnection(t *testing.T) {
	hub := NewHub()
	number := "3140001"
	oldConn := &Conn{Send: make(chan []byte, 1)}
	newConn := &Conn{Send: make(chan []byte, 1)}

	hub.conns[number] = newConn

	hub.Unregister(number, oldConn)
	if got := hub.Get(number); got != newConn {
		t.Fatalf("expected new connection to remain registered")
	}
}

func TestUnregisterRemovesMatchingConnection(t *testing.T) {
	hub := NewHub()
	number := "3140001"
	conn := &Conn{Send: make(chan []byte, 1)}
	hub.conns[number] = conn

	hub.Unregister(number, conn)
	if got := hub.Get(number); got != nil {
		t.Fatalf("expected connection to be removed")
	}
}

func TestHubGetReturnsNilForOffline(t *testing.T) {
	hub := NewHub()
	if hub.Get("3140099") != nil {
		t.Fatal("expected nil for unregistered number")
	}
}

func TestHubGetReturnsConnForOnline(t *testing.T) {
	hub := NewHub()
	conn := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn)
	if hub.Get("3140001") == nil {
		t.Fatal("expected connection for registered number")
	}
}

func TestRegisterHandlesAlreadyClosedSendChannel(t *testing.T) {
	hub := NewHub()
	number := "3140001"
	oldConn := &Conn{Send: make(chan []byte)}
	close(oldConn.Send)
	hub.conns[number] = oldConn

	newConn := &Conn{Send: make(chan []byte, 1)}
	hub.Register(number, newConn)

	if got := hub.Get(number); got != newConn {
		t.Fatalf("expected new connection to be registered")
	}
}
