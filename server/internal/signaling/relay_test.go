package signaling

import (
	"testing"
)

type mockTracker struct {
	initiated []string
	answered  []string
	ended     []string
}

func (m *mockTracker) OnCallInitiated(from, to string) error {
	m.initiated = append(m.initiated, from+"→"+to)
	return nil
}
func (m *mockTracker) OnCallAnswered(caller, callee string) error {
	m.answered = append(m.answered, caller+"→"+callee)
	return nil
}
func (m *mockTracker) OnCallEnded(caller, callee string) error {
	m.ended = append(m.ended, caller+"→"+callee)
	return nil
}

type mockCallAuthorizer struct {
	allowed map[[2]string]bool
}

func (m *mockCallAuthorizer) CanCall(fromNumber, toNumber string) (bool, error) {
	return m.allowed[[2]string{fromNumber, toNumber}], nil
}

func TestRelayCallFlow(t *testing.T) {
	hub := NewHub()
	tracker := &mockTracker{}
	relay := NewRelay(hub, tracker, nil)

	// Register two mock connections
	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn1)
	hub.Register("3140002", conn2)

	// Phone 1 calls Phone 2
	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140002"})

	// Phone 2 should receive a ring message
	select {
	case data := <-conn2.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeRing || msg.From != "3140001" {
			t.Fatalf("expected ring from 3140001, got: %+v", msg)
		}
	default:
		t.Fatal("phone 2 did not receive ring")
	}

	if len(tracker.initiated) != 1 || tracker.initiated[0] != "3140001→3140002" {
		t.Fatalf("tracker: unexpected initiated: %v", tracker.initiated)
	}
}

func TestRelayCallToOfflinePhone(t *testing.T) {
	hub := NewHub()
	relay := NewRelay(hub, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn1)

	// Call offline phone
	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140099"})

	// Should get error back
	select {
	case data := <-conn1.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeError {
			t.Fatalf("expected error, got: %+v", msg)
		}
	default:
		t.Fatal("caller did not receive error")
	}
}

func TestRelayCallAuthorizationIntegration(t *testing.T) {
	hub := NewHub()
	tracker := &mockTracker{}

	// 3140001 and 3140002 are authorized; 3140003 is NOT authorized to be called by 3140001
	authorizer := &mockCallAuthorizer{
		allowed: map[[2]string]bool{
			{"3140001", "3140002"}: true,
		},
	}

	relay := NewRelay(hub, tracker, authorizer)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn1)
	hub.Register("3140002", conn2)

	// Test 1: Phone 1 calls Phone 2 → ring delivered (authorized)
	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140002"})

	select {
	case data := <-conn2.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if msg.Type != TypeRing || msg.From != "3140001" {
			t.Fatalf("expected ring from 3140001, got: %+v", msg)
		}
	default:
		t.Fatal("phone 2 did not receive ring (should be authorized)")
	}

	if len(tracker.initiated) != 1 || tracker.initiated[0] != "3140001→3140002" {
		t.Fatalf("tracker: unexpected initiated: %v", tracker.initiated)
	}

	// Test 2: Phone 1 calls Phone 3 (offline) → "phone not connected" error
	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140003"})

	select {
	case data := <-conn1.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if msg.Type != TypeError {
			t.Fatalf("expected error for offline phone, got: %s", msg.Type)
		}
		if msg.Error != "phone not connected" {
			t.Fatalf("expected 'phone not connected', got: %q", msg.Error)
		}
	default:
		t.Fatal("phone 1 did not receive error when calling offline phone")
	}

	// Test 3: Phone 3 comes online, Phone 1 calls Phone 3 → not_authorized
	conn3 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140003", conn3)

	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140003"})

	select {
	case data := <-conn1.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if msg.Type != TypeError {
			t.Fatalf("expected error, got: %s", msg.Type)
		}
		if msg.Error != "not_authorized" {
			t.Fatalf("expected 'not_authorized', got: %q", msg.Error)
		}
	default:
		t.Fatal("phone 1 did not receive not_authorized error")
	}

	// Ensure phone 3 received nothing (call was blocked before ring)
	select {
	case data := <-conn3.Send:
		msg, _ := ParseMessage(data)
		t.Fatalf("phone 3 should not have received anything, got: %+v", msg)
	default:
		// correct: nothing delivered to phone 3
	}
}

func TestRelayICERestartForwarded(t *testing.T) {
	hub := NewHub()
	relay := NewRelay(hub, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn1)
	hub.Register("3140002", conn2)

	// Phone 1 sends ICE restart to Phone 2
	relay.HandleMessage("3140001", &Message{
		Type: TypeICERestart,
		To:   "3140002",
		SDP:  "v=0\r\nrestart-offer\r\n",
	})

	select {
	case data := <-conn2.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeICERestart {
			t.Fatalf("expected ice_restart, got %s", msg.Type)
		}
		if msg.From != "3140001" {
			t.Fatalf("expected from 3140001, got %s", msg.From)
		}
		if msg.SDP != "v=0\r\nrestart-offer\r\n" {
			t.Fatalf("unexpected SDP: %q", msg.SDP)
		}
	default:
		t.Fatal("phone 2 did not receive ice_restart")
	}
}

func TestHubOnlineNumbers(t *testing.T) {
	hub := NewHub()
	conn1 := &Conn{Send: make(chan []byte, 1)}
	conn2 := &Conn{Send: make(chan []byte, 1)}
	hub.Register("3140001", conn1)
	hub.Register("3140002", conn2)

	nums := hub.OnlineNumbers()
	if len(nums) != 2 {
		t.Fatalf("expected 2 online, got %d", len(nums))
	}
}
