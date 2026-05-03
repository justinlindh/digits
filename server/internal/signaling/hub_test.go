package signaling

import (
	"bytes"
	"encoding/json"
	"testing"
)

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

func TestDeviceInfoIncludesRemoteAddr(t *testing.T) {
	hub := NewHub()
	conn := &Conn{
		Send:            make(chan []byte, 1),
		PiVersion:       "1.2.3",
		FirmwareVersion: "0.4.0",
		RemoteAddr:      "192.168.1.42",
	}
	hub.Register("3140001", conn)

	info := hub.DeviceInfo("3140001")
	if info == nil {
		t.Fatal("DeviceInfo returned nil for registered conn")
	}
	if info.RemoteAddr != "192.168.1.42" {
		t.Errorf("DeviceInfo.RemoteAddr = %q, want %q", info.RemoteAddr, "192.168.1.42")
	}
	if info.PiVersion != "1.2.3" {
		t.Errorf("DeviceInfo.PiVersion = %q, want %q", info.PiVersion, "1.2.3")
	}
}

func TestDeviceInfoNilWhenOffline(t *testing.T) {
	hub := NewHub()
	if got := hub.DeviceInfo("3140002"); got != nil {
		t.Errorf("DeviceInfo for unregistered number = %+v, want nil", got)
	}
}

func TestRegisterOverwritesRemoteAddrOnReconnect(t *testing.T) {
	hub := NewHub()
	first := &Conn{Send: make(chan []byte, 1), RemoteAddr: "192.168.1.42"}
	hub.Register("3140003", first)

	second := &Conn{Send: make(chan []byte, 1), RemoteAddr: "192.168.1.99"}
	hub.Register("3140003", second)

	info := hub.DeviceInfo("3140003")
	if info == nil {
		t.Fatal("expected DeviceInfo after reconnect")
	}
	if info.RemoteAddr != "192.168.1.99" {
		t.Errorf("RemoteAddr after reconnect = %q, want %q", info.RemoteAddr, "192.168.1.99")
	}
}

func TestBroadcastSendsToAllConnected(t *testing.T) {
	hub := NewHub()

	c1 := &Conn{Send: make(chan []byte, 10)}
	c2 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", c1)
	hub.Register("3140002", c2)

	msg := &Message{
		Type:            TypeReleaseAvailable,
		LatestPiVersion: "2.0.0",
		LatestFWVersion: "1.5.0",
	}
	hub.Broadcast(msg)

	for _, tc := range []struct {
		name string
		conn *Conn
	}{
		{"device 1", c1},
		{"device 2", c2},
	} {
		select {
		case data := <-tc.conn.Send:
			got, err := ParseMessage(data)
			if err != nil {
				t.Fatalf("%s: parse: %v", tc.name, err)
			}
			if got.Type != TypeReleaseAvailable {
				t.Errorf("%s: Type = %q, want %q", tc.name, got.Type, TypeReleaseAvailable)
			}
			if got.LatestPiVersion != "2.0.0" {
				t.Errorf("%s: LatestPiVersion = %q, want %q", tc.name, got.LatestPiVersion, "2.0.0")
			}
			if got.LatestFWVersion != "1.5.0" {
				t.Errorf("%s: LatestFWVersion = %q, want %q", tc.name, got.LatestFWVersion, "1.5.0")
			}
		default:
			t.Errorf("%s: did not receive broadcast", tc.name)
		}
	}
}

func TestBroadcastSkipsFullBuffers(t *testing.T) {
	hub := NewHub()

	full := &Conn{Send: make(chan []byte)} // unbuffered, will be full
	ok := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", full)
	hub.Register("3140002", ok)

	msg := &Message{Type: TypeReleaseAvailable, LatestPiVersion: "2.0.0"}
	hub.Broadcast(msg)

	select {
	case <-ok.Send:
		// good
	default:
		t.Error("buffered conn should have received broadcast")
	}
}

func TestDeviceInfoSnapshotJSONOmitsRemoteAddr(t *testing.T) {
	snap := DeviceInfoSnapshot{
		PiVersion:       "1.2.3",
		FirmwareVersion: "0.4.0",
		RemoteAddr:      "192.168.1.42",
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(data, []byte("192.168.1.42")) {
		t.Errorf("RemoteAddr value leaked into JSON: %s", data)
	}
	if bytes.Contains(data, []byte("RemoteAddr")) {
		t.Errorf("RemoteAddr field name appeared in JSON: %s", data)
	}
}
