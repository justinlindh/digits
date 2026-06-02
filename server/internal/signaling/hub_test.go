package signaling

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestUnregisterOnlyRemovesMatchingConnection(t *testing.T) {
	hub := NewHub()
	number := "3140001"
	oldConn := &Conn{Send: make(chan []byte, 1)}
	newConn := &Conn{Send: make(chan []byte, 1)}

	hub.conns[number] = []*Conn{newConn}

	hub.Unregister(number, oldConn)
	if got := hub.Get(number); got != newConn {
		t.Fatalf("expected new connection to remain registered")
	}
}

func TestUnregisterRemovesMatchingConnection(t *testing.T) {
	hub := NewHub()
	number := "3140001"
	conn := &Conn{Send: make(chan []byte, 1)}
	hub.conns[number] = []*Conn{conn}

	hub.Unregister(number, conn)
	if got := hub.Get(number); got != nil {
		t.Fatalf("expected connection to be removed")
	}
}

func TestIsOnlineReturnsFalseForUnpaired(t *testing.T) {
	hub := NewHub()
	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register(UnpairedPrefix+"test-hw-xyz", conn)
	if hub.IsOnline(UnpairedPrefix + "test-hw-xyz") {
		t.Fatal("IsOnline should return false for unpaired devices")
	}
}

func TestIsOnlineReturnsTrueForPairedConnected(t *testing.T) {
	hub := NewHub()
	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)
	if !hub.IsOnline("3140001") {
		t.Fatal("IsOnline should return true for a paired, connected device")
	}
}

func TestIsOnlineReturnsFalseForOffline(t *testing.T) {
	hub := NewHub()
	if hub.IsOnline("3140099") {
		t.Fatal("IsOnline should return false for a number with no connection")
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
	_ = hub.Register("3140001", conn)
	if hub.Get("3140001") == nil {
		t.Fatal("expected connection for registered number")
	}
}

func TestRegisterHandlesAlreadyClosedSendChannel(t *testing.T) {
	hub := NewHub()
	number := "3140001"
	oldConn := &Conn{Send: make(chan []byte), HardwareID: "hw-001"}
	close(oldConn.Send)
	hub.conns[number] = []*Conn{oldConn}
	hub.hwConns["hw-001"] = oldConn

	newConn := &Conn{Send: make(chan []byte, 1), HardwareID: "hw-001"}
	_ = hub.Register(number, newConn)

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
	_ = hub.Register("3140001", conn)

	all := hub.AllDeviceInfo("3140001")
	if len(all) == 0 {
		t.Fatal("AllDeviceInfo returned no devices for registered conn")
	}
	info := all[0]
	if info.RemoteAddr != "192.168.1.42" {
		t.Errorf("RemoteAddr = %q, want %q", info.RemoteAddr, "192.168.1.42")
	}
	if info.PiVersion != "1.2.3" {
		t.Errorf("PiVersion = %q, want %q", info.PiVersion, "1.2.3")
	}
}

func TestAllDeviceInfoEmptyWhenOffline(t *testing.T) {
	hub := NewHub()
	if got := hub.AllDeviceInfo("3140002"); len(got) != 0 {
		t.Errorf("AllDeviceInfo for unregistered number = %+v, want empty", got)
	}
}

func TestRegisterOverwritesRemoteAddrOnReconnect(t *testing.T) {
	hub := NewHub()
	first := &Conn{Send: make(chan []byte, 1), HardwareID: "hw-003", RemoteAddr: "192.168.1.42"}
	_ = hub.Register("3140003", first)

	second := &Conn{Send: make(chan []byte, 1), HardwareID: "hw-003", RemoteAddr: "192.168.1.99"}
	_ = hub.Register("3140003", second)

	all := hub.AllDeviceInfo("3140003")
	if len(all) == 0 {
		t.Fatal("expected device info after reconnect")
	}
	info := all[0]
	if info.RemoteAddr != "192.168.1.99" {
		t.Errorf("RemoteAddr after reconnect = %q, want %q", info.RemoteAddr, "192.168.1.99")
	}
}

func TestBroadcastSendsToAllConnected(t *testing.T) {
	hub := NewHub()

	c1 := &Conn{Send: make(chan []byte, 10)}
	c2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", c1)
	_ = hub.Register("3140002", c2)

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
	_ = hub.Register("3140001", full)
	_ = hub.Register("3140002", ok)

	msg := &Message{Type: TypeReleaseAvailable, LatestPiVersion: "2.0.0"}
	hub.Broadcast(msg)

	select {
	case <-ok.Send:
		// good
	default:
		t.Error("buffered conn should have received broadcast")
	}
}

func TestAllDeviceInfoMultipleDevices(t *testing.T) {
	hub := NewHub()
	c1 := &Conn{Send: make(chan []byte, 1), HardwareID: "hw-001", PiVersion: "1.0.0", FirmwareVersion: "0.5.0"}
	c2 := &Conn{Send: make(chan []byte, 1), HardwareID: "hw-002", PiVersion: "1.2.0", FirmwareVersion: "0.8.0"}
	_ = hub.Register("3140001", c1)
	_ = hub.Register("3140001", c2)

	infos := hub.AllDeviceInfo("3140001")
	if len(infos) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(infos))
	}
	versions := map[string]string{}
	for _, info := range infos {
		versions[info.HardwareID] = info.PiVersion
	}
	if versions["hw-001"] != "1.0.0" {
		t.Errorf("hw-001 PiVersion = %q, want 1.0.0", versions["hw-001"])
	}
	if versions["hw-002"] != "1.2.0" {
		t.Errorf("hw-002 PiVersion = %q, want 1.2.0", versions["hw-002"])
	}
}

func TestAllDeviceInfoReturnsNilForOffline(t *testing.T) {
	hub := NewHub()
	infos := hub.AllDeviceInfo("3140099")
	if infos != nil {
		t.Errorf("expected nil for unregistered number, got %d items", len(infos))
	}
}

func TestAllDeviceInfoAfterUnregister(t *testing.T) {
	hub := NewHub()
	c1 := &Conn{Send: make(chan []byte, 1), HardwareID: "hw-001", PiVersion: "1.0.0"}
	c2 := &Conn{Send: make(chan []byte, 1), HardwareID: "hw-002", PiVersion: "1.2.0"}
	_ = hub.Register("3140001", c1)
	_ = hub.Register("3140001", c2)

	hub.Unregister("3140001", c1)

	infos := hub.AllDeviceInfo("3140001")
	if len(infos) != 1 {
		t.Fatalf("expected 1 device after unregister, got %d", len(infos))
	}
	if infos[0].HardwareID != "hw-002" {
		t.Errorf("remaining device = %q, want hw-002", infos[0].HardwareID)
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

func TestSetVoicemailUnheardSumsAcrossHandsets(t *testing.T) {
	hub := NewHub()
	hub.SetVoicemailUnheard("3140001", "hw-a", 3)
	hub.SetVoicemailUnheard("3140001", "hw-b", 5)
	if got := hub.LineVoicemailUnheard("3140001"); got != 8 {
		t.Errorf("LineVoicemailUnheard: got %d, want 8", got)
	}
}

func TestSetVoicemailUnheardOverwritesPerHandset(t *testing.T) {
	hub := NewHub()
	hub.SetVoicemailUnheard("3140001", "hw-a", 3)
	hub.SetVoicemailUnheard("3140001", "hw-a", 7)
	if got := hub.LineVoicemailUnheard("3140001"); got != 7 {
		t.Errorf("LineVoicemailUnheard: got %d, want 7 (overwrite)", got)
	}
}

func TestSetVoicemailUnheardRejectsEmptyHardwareID(t *testing.T) {
	hub := NewHub()
	hub.SetVoicemailUnheard("3140001", "", 5)
	if got := hub.LineVoicemailUnheard("3140001"); got != 0 {
		t.Errorf("empty hwID should be dropped, got %d", got)
	}
}

func TestSetVoicemailUnheardClampsNegative(t *testing.T) {
	hub := NewHub()
	hub.SetVoicemailUnheard("3140001", "hw-a", -3)
	if got := hub.LineVoicemailUnheard("3140001"); got != 0 {
		t.Errorf("negative count should clamp to 0, got %d", got)
	}
}

func TestLineVoicemailUnheardZeroForUnknownNumber(t *testing.T) {
	hub := NewHub()
	if got := hub.LineVoicemailUnheard("3140099"); got != 0 {
		t.Errorf("unknown number: got %d, want 0", got)
	}
}

func TestUnregisterClearsVoicemailUnheardForHandset(t *testing.T) {
	hub := NewHub()
	number := "3140001"
	conn := &Conn{Send: make(chan []byte, 1), HardwareID: "hw-a"}
	hub.conns[number] = []*Conn{conn}
	hub.hwConns["hw-a"] = conn
	hub.SetVoicemailUnheard(number, "hw-a", 4)
	hub.SetVoicemailUnheard(number, "hw-b", 2)

	hub.Unregister(number, conn)
	// Only hw-a's count should drop; hw-b's lingers (it's still online).
	if got := hub.LineVoicemailUnheard(number); got != 2 {
		t.Errorf("after unregister hw-a: got %d, want 2 (hw-b's only)", got)
	}
}

func TestRekeyNumberMovesVoicemailUnheard(t *testing.T) {
	hub := NewHub()
	hub.SetVoicemailUnheard("3140001", "hw-a", 3)
	hub.SetVoicemailUnheard("3140001", "hw-b", 4)

	hub.RekeyNumber("3140001", "3140002")

	if got := hub.LineVoicemailUnheard("3140001"); got != 0 {
		t.Errorf("old number should have 0 after rekey, got %d", got)
	}
	if got := hub.LineVoicemailUnheard("3140002"); got != 7 {
		t.Errorf("new number should have summed counts, got %d, want 7", got)
	}
}

// TestSendToWithTimeoutDeliversWhenBufferHasRoom verifies that
// SendToWithTimeout successfully enqueues a message when the channel has
// capacity.
func TestSendToWithTimeoutDeliversWhenBufferHasRoom(t *testing.T) {
	hub := NewHub()
	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)

	err := hub.SendToWithTimeout("3140001", &Message{Type: TypeRing, From: "3140002"}, time.Second)
	if err != nil {
		t.Fatalf("SendToWithTimeout returned unexpected error: %v", err)
	}
	select {
	case data := <-conn.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeRing {
			t.Errorf("got type %q, want ring", msg.Type)
		}
	default:
		t.Fatal("no message in send channel after SendToWithTimeout")
	}
}

// TestSendToWithTimeoutReturnsErrorWhenBufferFullAndNotDrained verifies that
// SendToWithTimeout returns ErrSendTimeout promptly when the buffer is full
// and no reader drains it, rather than blocking forever.
func TestSendToWithTimeoutReturnsErrorWhenBufferFullAndNotDrained(t *testing.T) {
	hub := NewHub()
	// Unbuffered channel: any send blocks immediately.
	conn := &Conn{Send: make(chan []byte)}
	_ = hub.Register("3140001", conn)

	timeout := 50 * time.Millisecond
	start := time.Now()
	err := hub.SendToWithTimeout("3140001", &Message{Type: TypeRing}, timeout)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from SendToWithTimeout on full buffer, got nil")
	}
	if !errors.Is(err, ErrSendTimeout) {
		t.Errorf("expected ErrSendTimeout, got: %v", err)
	}
	// Must return within roughly 2x the timeout, not hang.
	if elapsed > 5*timeout {
		t.Errorf("SendToWithTimeout took %v, expected ~%v", elapsed, timeout)
	}
}

// TestSendToWithTimeoutReturnsNotConnectedWhenNoDevice verifies the offline
// case.
func TestSendToWithTimeoutReturnsNotConnectedWhenNoDevice(t *testing.T) {
	hub := NewHub()
	err := hub.SendToWithTimeout("3140099", &Message{Type: TypeRing}, time.Second)
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected for offline number, got: %v", err)
	}
}

// TestSendToInvokesDropHookOnFullBuffer verifies that the best-effort SendTo
// path calls the drop hook when a device's send buffer is full.
func TestSendToInvokesDropHookOnFullBuffer(t *testing.T) {
	hub := NewHub()
	var drops int
	hub.SetDropHook(func() { drops++ })

	// Unbuffered channel: send immediately triggers the default branch.
	conn := &Conn{Send: make(chan []byte)}
	_ = hub.Register("3140001", conn)

	_ = hub.SendTo("3140001", &Message{Type: TypeRing})

	if drops != 1 {
		t.Errorf("drop hook called %d times, want 1", drops)
	}
}

// TestSendToDoesNotInvokeDropHookOnSuccessfulSend verifies the hook is NOT
// called when the message is delivered.
func TestSendToDoesNotInvokeDropHookOnSuccessfulSend(t *testing.T) {
	hub := NewHub()
	var drops int
	hub.SetDropHook(func() { drops++ })

	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)

	_ = hub.SendTo("3140001", &Message{Type: TypeRing})

	if drops != 0 {
		t.Errorf("drop hook called %d times on successful send, want 0", drops)
	}
}
