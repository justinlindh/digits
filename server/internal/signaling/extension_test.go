package signaling

import (
	"context"
	"testing"
)

func TestExtensionPickup_HappyPath(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	phoneA := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-a"}
	phoneB := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-b"}
	phoneY := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-y"}
	_ = hub.Register("3140001", phoneA)
	_ = hub.Register("3140001", phoneB)
	_ = hub.Register("3140002", phoneY)

	// Establish a call: line 3140001 (device A) calls line 3140002
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	drainN(t, phoneY.Send, 1) // ring

	relay.HandleMessage(context.Background(), "3140002", &Message{Type: TypeAnswer, To: "3140001", HardwareID: "hw-y"})
	drainN(t, phoneA.Send, 1) // answer forwarded
	drainN(t, phoneB.Send, 1) // ring-cancel hangup from first-answer-wins

	// Device B picks up the extension
	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:       TypeExtensionPickup,
		HardwareID: "hw-b",
	})

	// B should get ExtensionConnect (initiator)
	bMsg := drainOne(t, phoneB.Send)
	if bMsg.Type != TypeExtensionConnect {
		t.Fatalf("expected ExtensionConnect for B, got %s", bMsg.Type)
	}
	if !bMsg.Initiator {
		t.Error("B should be initiator")
	}
	if bMsg.Peer != "3140002" {
		t.Errorf("B's peer should be 3140002, got %s", bMsg.Peer)
	}

	// Y should get ExtensionConnect (non-initiator)
	yMsg := drainOne(t, phoneY.Send)
	if yMsg.Type != TypeExtensionConnect {
		t.Fatalf("expected ExtensionConnect for Y, got %s", yMsg.Type)
	}
	if yMsg.Initiator {
		t.Error("Y should not be initiator")
	}

	// A should get ExtensionActive notification
	aMsg := drainOne(t, phoneA.Send)
	if aMsg.Type != TypeExtensionActive {
		t.Fatalf("expected ExtensionActive for A, got %s", aMsg.Type)
	}
}

func TestExtensionPickup_NoActiveCall(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	phoneB := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-b"}
	_ = hub.Register("3140001", phoneB)

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:       TypeExtensionPickup,
		HardwareID: "hw-b",
	})

	msg := drainOne(t, phoneB.Send)
	if msg.Type != TypeError {
		t.Fatalf("expected error when no active call, got %s", msg.Type)
	}
}

func TestExtensionPickup_NoHardwareID(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	phone := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", phone)

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type: TypeExtensionPickup,
	})

	msg := drainOne(t, phone.Send)
	if msg.Type != TypeError {
		t.Fatalf("expected error without hardware_id, got %s", msg.Type)
	}
}

func TestExtensionHangup_OnlyExtensionCleared(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	phoneA := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-a"}
	phoneB := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-b"}
	phoneY := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-y"}
	_ = hub.Register("3140001", phoneA)
	_ = hub.Register("3140001", phoneB)
	_ = hub.Register("3140002", phoneY)

	// Establish call and extension
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	drainN(t, phoneY.Send, 1)
	relay.HandleMessage(context.Background(), "3140002", &Message{Type: TypeAnswer, To: "3140001", HardwareID: "hw-y"})
	drainCh(phoneA.Send)
	drainCh(phoneB.Send)
	drainCh(phoneY.Send)

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:       TypeExtensionPickup,
		HardwareID: "hw-b",
	})
	drainCh(phoneA.Send)
	drainCh(phoneB.Send)
	drainCh(phoneY.Send)

	// Extension device B hangs up
	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:       TypeHangup,
		To:         "3140002",
		HardwareID: "hw-b",
	})

	// The main call between A and Y should still be active
	if !tracker.Busy(context.Background(), "3140001") {
		t.Fatal("main call should still be active after extension hangup")
	}
	if !tracker.Busy(context.Background(), "3140002") {
		t.Fatal("remote peer should still be active after extension hangup")
	}
}

func TestMainCallEnd_ClearsExtensions(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	phoneA := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-a"}
	phoneB := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-b"}
	phoneY := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-y"}
	_ = hub.Register("3140001", phoneA)
	_ = hub.Register("3140001", phoneB)
	_ = hub.Register("3140002", phoneY)

	// Establish call and extension
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	drainN(t, phoneY.Send, 1)
	relay.HandleMessage(context.Background(), "3140002", &Message{Type: TypeAnswer, To: "3140001", HardwareID: "hw-y"})
	drainCh(phoneA.Send)
	drainCh(phoneB.Send)
	drainCh(phoneY.Send)

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:       TypeExtensionPickup,
		HardwareID: "hw-b",
	})
	drainCh(phoneA.Send)
	drainCh(phoneB.Send)
	drainCh(phoneY.Send)

	// Main caller (line 3140001) hangs up
	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:       TypeHangup,
		To:         "3140002",
		HardwareID: "hw-a",
	})

	// Extension device B should receive a hangup
	bMsg := drainOne(t, phoneB.Send)
	if bMsg.Type != TypeHangup {
		t.Fatalf("extension device should get hangup when main call ends, got %s", bMsg.Type)
	}
}

// drainOne reads exactly one message from ch. Fails the test if none available.
func drainOne(t *testing.T, ch <-chan []byte) *Message {
	t.Helper()
	select {
	case data := <-ch:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse message: %v", err)
		}
		return msg
	default:
		t.Fatal("expected a message but channel was empty")
		return nil
	}
}

// drainN reads exactly n messages from ch. Fails if fewer are available.
func drainN(t *testing.T, ch <-chan []byte, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		default:
			t.Fatalf("expected %d messages but only got %d", n, i)
		}
	}
}

// drainCh empties a single channel without blocking.
func drainCh(ch <-chan []byte) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
