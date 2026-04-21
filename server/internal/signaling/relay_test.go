package signaling

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/justinlindh/digits/server/internal/calls"
)

type mockTracker struct {
	initiated   []string
	answered    []string
	ended       []string
	calls       map[string]bool  // "a→b" keys for active calls
	callIDs     map[string]int64 // "a→b" keys for call IDs
	conferences *calls.ConferenceTracker
}

func newMockTracker() *mockTracker {
	return &mockTracker{
		calls:       make(map[string]bool),
		callIDs:     make(map[string]int64),
		conferences: calls.NewConferenceTracker(),
	}
}

// onCallInitiated primes an active call between from and to (test helper).
func (m *mockTracker) onCallInitiated(from, to string) {
	m.calls[from+"→"+to] = true
}

// setCallID sets the call ID for an active call (test helper).
func (m *mockTracker) setCallID(from, to string, id int64) {
	m.callIDs[from+"→"+to] = id
	m.callIDs[to+"→"+from] = id
}

func (m *mockTracker) OnCallInitiated(from, to string) (int64, error) {
	m.initiated = append(m.initiated, from+"→"+to)
	m.calls[from+"→"+to] = true
	return 0, nil
}
func (m *mockTracker) OnCallAnswered(caller, callee string) error {
	m.answered = append(m.answered, caller+"→"+callee)
	return nil
}
func (m *mockTracker) OnCallEnded(caller, callee string) error {
	m.ended = append(m.ended, caller+"→"+callee)
	delete(m.calls, caller+"→"+callee)
	delete(m.calls, callee+"→"+caller)
	return nil
}
func (m *mockTracker) ClearByNumber(number string) {
	for k := range m.calls {
		a, b, _ := strings.Cut(k, "→")
		if a == number || b == number {
			delete(m.calls, k)
		}
	}
}
func (m *mockTracker) Busy(number string) bool {
	for k := range m.calls {
		a, b, _ := strings.Cut(k, "→")
		if a == number || b == number {
			return true
		}
	}
	return false
}

func (m *mockTracker) InCall(a, b string) bool {
	return m.calls[a+"→"+b] || m.calls[b+"→"+a]
}

func (m *mockTracker) PeerOf(number string) string {
	for k := range m.calls {
		a, b, _ := strings.Cut(k, "→")
		if a == number {
			return b
		}
		if b == number {
			return a
		}
	}
	return ""
}

func (m *mockTracker) Conferences() *calls.ConferenceTracker {
	return m.conferences
}

func (m *mockTracker) CreateConferencePersistent(host string, originatingCallID int64, addedMembers []string) (*calls.Conference, error) {
	return m.conferences.CreateConference(host, originatingCallID, addedMembers)
}

func (m *mockTracker) CallIDFor(a, b string) int64 {
	if id, ok := m.callIDs[a+"→"+b]; ok {
		return id
	}
	if id, ok := m.callIDs[b+"→"+a]; ok {
		return id
	}
	return 0
}

func (m *mockTracker) EndConferencePersistent(id uuid.UUID, reason string) error {
	_, err := m.conferences.EndConference(id, reason)
	return err
}

func (m *mockTracker) DropMemberPersistent(id uuid.UUID, phone, reason string) ([]string, bool, error) {
	return m.conferences.DropMember(id, phone, reason)
}

type mockCallAuthorizer struct {
	allowed map[[2]string]bool
	denyAll bool
}

func (m *mockCallAuthorizer) CanCall(fromNumber, toNumber string) (bool, error) {
	if m.denyAll {
		return false, nil
	}
	return m.allowed[[2]string{fromNumber, toNumber}], nil
}

type errorCallAuthorizer struct {
	err error
}

func (m *errorCallAuthorizer) CanCall(fromNumber, toNumber string) (bool, error) {
	return false, m.err
}

func TestRelayCallFlow(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

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
	relay := NewRelay(hub, nil, nil, nil)

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
	tracker := newMockTracker()

	// 3140001 and 3140002 are authorized; 3140003 is NOT authorized to be called by 3140001
	authorizer := &mockCallAuthorizer{
		allowed: map[[2]string]bool{
			{"3140001", "3140002"}: true,
		},
	}

	relay := NewRelay(hub, tracker, authorizer, nil)

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

func TestRelayCallDeniedOnAuthorizerError(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()

	authorizer := &errorCallAuthorizer{err: errors.New("db connection failed")}
	relay := NewRelay(hub, tracker, authorizer, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn1)
	hub.Register("3140002", conn2)

	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140002"})

	select {
	case data := <-conn1.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if msg.Type != TypeError || msg.Error != "not_authorized" {
			t.Fatalf("expected not_authorized error, got: %+v", msg)
		}
	default:
		t.Fatal("caller did not receive error when authorizer fails")
	}

	// Callee should not receive a ring
	select {
	case data := <-conn2.Send:
		msg, _ := ParseMessage(data)
		t.Fatalf("callee should not have received anything, got: %+v", msg)
	default:
		// correct
	}
}

func TestRelayBusySignal(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	conn3 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn1)
	hub.Register("3140002", conn2)
	hub.Register("3140003", conn3)

	// Phone 1 calls Phone 2 (establishes active call)
	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

	// Phone 3 calls Phone 2 (busy) -- should get busy signal
	relay.HandleMessage("3140003", &Message{Type: TypeCall, To: "3140002"})

	select {
	case data := <-conn3.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if msg.Type != TypeBusy {
			t.Fatalf("expected busy, got: %+v", msg)
		}
	default:
		t.Fatal("phone 3 did not receive busy signal")
	}

	// Phone 1 tries to call Phone 3 while already on a call -- should also get busy
	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140003"})

	select {
	case data := <-conn1.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if msg.Type != TypeBusy {
			t.Fatalf("expected busy for caller already in call, got: %+v", msg)
		}
	default:
		t.Fatal("phone 1 did not receive busy signal when already in a call")
	}

	// Phone 3 should not have received anything from the second call attempt
	select {
	case data := <-conn3.Send:
		msg, _ := ParseMessage(data)
		t.Fatalf("phone 3 should not have received anything, got: %+v", msg)
	default:
		// correct
	}
}

func TestRelayHangupWithoutToResolvesPeer(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn1)
	hub.Register("3140002", conn2)

	// Phone 1 calls Phone 2
	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

	if !tracker.Busy("3140001") {
		t.Fatal("expected 3140001 to be busy after call initiated")
	}

	// Phone 1 hangs up WITHOUT specifying To (reproduces the client bug)
	relay.HandleMessage("3140001", &Message{Type: TypeHangup})

	// Tracker should have resolved the peer and ended the call
	if tracker.Busy("3140001") {
		t.Fatal("expected 3140001 to no longer be busy after hangup")
	}
	if tracker.Busy("3140002") {
		t.Fatal("expected 3140002 to no longer be busy after hangup")
	}

	// Phone 2 should have received the hangup (forwarded with resolved To)
	select {
	case data := <-conn2.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if msg.Type != TypeHangup {
			t.Fatalf("expected hangup, got: %s", msg.Type)
		}
	default:
		t.Fatal("phone 2 did not receive hangup")
	}
}

func TestRelayICERestartForwarded(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn1)
	hub.Register("3140002", conn2)

	// Establish an active call first
	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

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

func TestRelayICERestartRejectedWithoutCall(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn1)
	hub.Register("3140002", conn2)

	// Phone 1 sends ICE restart without an active call
	relay.HandleMessage("3140001", &Message{
		Type: TypeICERestart,
		To:   "3140002",
		SDP:  "v=0\r\nrestart-offer\r\n",
	})

	// Sender should get an error
	select {
	case data := <-conn1.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeError {
			t.Fatalf("expected error, got %s", msg.Type)
		}
		if msg.Error != "no active call" {
			t.Fatalf("expected 'no active call', got %q", msg.Error)
		}
	default:
		t.Fatal("sender did not receive error for ice_restart without active call")
	}

	// Target should not have received anything
	select {
	case data := <-conn2.Send:
		msg, _ := ParseMessage(data)
		t.Fatalf("target should not receive anything, got: %+v", msg)
	default:
		// correct
	}
}

func TestRelayOnDisconnectClearsActiveCalls(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	conn3 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn1)
	hub.Register("3140002", conn2)
	hub.Register("3140003", conn3)

	// Establish a call: Phone 1 → Phone 2
	relay.HandleMessage("3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

	// Verify Phone 2 is busy
	if !tracker.Busy("3140002") {
		t.Fatal("expected phone 2 to be busy after call initiated")
	}

	// Simulate Phone 2 disconnecting (WebSocket drops)
	relay.OnDisconnect("3140002")

	// Phone 2 should no longer be busy
	if tracker.Busy("3140002") {
		t.Fatal("expected phone 2 to not be busy after disconnect cleanup")
	}
	// Phone 1 should also be freed (its call was with Phone 2)
	if tracker.Busy("3140001") {
		t.Fatal("expected phone 1 to not be busy after peer disconnected")
	}

	// Phone 3 can now call Phone 2 (once reconnected)
	newConn2 := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140002", newConn2)
	relay.HandleMessage("3140003", &Message{Type: TypeCall, To: "3140002"})

	select {
	case data := <-newConn2.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeRing {
			t.Fatalf("expected ring on reconnected phone 2, got: %+v", msg)
		}
	default:
		t.Fatal("reconnected phone 2 did not receive ring")
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

func TestRelayRestartMessageNotPanics(t *testing.T) {
	hub := NewHub()
	relay := NewRelay(hub, nil, nil, nil)

	conn := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn)

	// Restart messages are server->device, not relayed through HandleMessage.
	// But verify it doesn't crash if one passes through.
	relay.HandleMessage("3140001", &Message{Type: TypeRestart, RestartMode: "service"})
}
