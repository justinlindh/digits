package signaling

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/justinlindh/digits/server/internal/calls"
)

type mockTracker struct {
	initiated         []string
	answered          []string
	ended             []string
	mu                sync.Mutex
	cleared           []string
	calls             map[string]bool  // "a→b" keys for active calls
	callIDs           map[string]int64 // "a→b" keys for active call IDs
	peers             map[string]string
	conferences       *calls.ConferenceTracker
	lastInboundCaller string
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

func (m *mockTracker) OnCallInitiated(ctx context.Context, from, to string) (int64, error) {
	m.initiated = append(m.initiated, from+"→"+to)
	m.calls[from+"→"+to] = true
	m.callIDs[from+"→"+to] = 1
	return 1, nil
}
func (m *mockTracker) OnCallAnswered(ctx context.Context, caller, callee string) error {
	m.answered = append(m.answered, caller+"→"+callee)
	return nil
}
func (m *mockTracker) OnCallEnded(ctx context.Context, caller, callee string) error {
	m.ended = append(m.ended, caller+"→"+callee)
	delete(m.calls, caller+"→"+callee)
	delete(m.calls, callee+"→"+caller)
	delete(m.callIDs, caller+"→"+callee)
	delete(m.callIDs, callee+"→"+caller)
	return nil
}
func (m *mockTracker) ClearByNumber(ctx context.Context, number string) {
	m.mu.Lock()
	m.cleared = append(m.cleared, number)
	m.mu.Unlock()
	for k := range m.calls {
		a, b, _ := strings.Cut(k, "→")
		if a == number || b == number {
			delete(m.calls, k)
			delete(m.callIDs, k)
		}
	}
}
func (m *mockTracker) clearedNumbers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.cleared...)
}
func (m *mockTracker) Busy(_ context.Context, number string) bool {
	for k := range m.calls {
		a, b, _ := strings.Cut(k, "→")
		if a == number || b == number {
			return true
		}
	}
	return false
}

func (m *mockTracker) CanAddAsHost(_ context.Context, number string) bool {
	callerCount := 0
	for k := range m.calls {
		a, b, _ := strings.Cut(k, "→")
		if b == number {
			return false
		}
		if a == number {
			callerCount++
		}
	}
	return callerCount == 1
}

func (m *mockTracker) InCall(_ context.Context, a, b string) bool {
	return m.calls[a+"→"+b] || m.calls[b+"→"+a]
}

func (m *mockTracker) PeerOf(_ context.Context, number string) string {
	// Explicit peers map takes precedence; used by OnDisconnect grace-window tests.
	if peer, ok := m.peers[number]; ok {
		return peer
	}
	// Fall back to the calls map so extension and call-flow tests continue to
	// work without needing to pre-populate peers.
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

func (m *mockTracker) AllPeersOf(_ context.Context, number string) []string {
	// Explicit peers map takes precedence, mirroring PeerOf behavior so
	// grace-window tests that stage via peers instead of calls still work.
	if peer, ok := m.peers[number]; ok && peer != "" {
		return []string{peer}
	}
	var peers []string
	for k := range m.calls {
		a, b, _ := strings.Cut(k, "→")
		if a == number {
			peers = append(peers, b)
		} else if b == number {
			peers = append(peers, a)
		}
	}
	return peers
}

func (m *mockTracker) Conferences() *calls.ConferenceTracker {
	return m.conferences
}

func (m *mockTracker) CreateConferencePersistent(ctx context.Context, host string, originatingCallID int64, addedMembers []string) (*calls.Conference, error) {
	return m.conferences.CreateConference(ctx, host, originatingCallID, addedMembers)
}

func (m *mockTracker) CallIDForPair(_ context.Context, a, b string) int64 {
	if id, ok := m.callIDs[a+"→"+b]; ok {
		return id
	}
	if id, ok := m.callIDs[b+"→"+a]; ok {
		return id
	}
	return 0
}

func (m *mockTracker) CallIDFor(_ context.Context, number string) (int64, bool) {
	for k, id := range m.callIDs {
		a, b, _ := strings.Cut(k, "→")
		if a == number || b == number {
			return id, true
		}
	}
	return 0, false
}

func (m *mockTracker) EndConferencePersistent(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := m.conferences.EndConference(ctx, id, reason)
	return err
}

func (m *mockTracker) DropMemberPersistent(ctx context.Context, id uuid.UUID, phone, reason string) ([]string, bool, error) {
	return m.conferences.DropMember(ctx, id, phone, reason)
}

func (m *mockTracker) LastInboundCaller(ctx context.Context, number string) (string, error) {
	return m.lastInboundCaller, nil
}

// fakeHealthRecorder records calls for assertion in tests.
type fakeHealthRecorder struct {
	records []struct {
		callID   int64
		endpoint string
		sample   calls.Sample
	}
	edges []recordedEdge
}

type recordedEdge struct {
	ConfID uuid.UUID
	From   string
	Peer   string
	Sample calls.Sample
}

func (f *fakeHealthRecorder) Record(id int64, ep string, s calls.Sample) {
	f.records = append(f.records, struct {
		callID   int64
		endpoint string
		sample   calls.Sample
	}{id, ep, s})
}

func (f *fakeHealthRecorder) RecordEdge(confID uuid.UUID, from, peer string, s calls.Sample) {
	f.edges = append(f.edges, recordedEdge{ConfID: confID, From: from, Peer: peer, Sample: s})
}

type mockCallAuthorizer struct {
	allowed map[[2]string]bool
	denyAll bool
}

func (m *mockCallAuthorizer) CanCall(ctx context.Context, fromNumber, toNumber string) (bool, error) {
	if m.denyAll {
		return false, nil
	}
	return m.allowed[[2]string{fromNumber, toNumber}], nil
}

type errorCallAuthorizer struct {
	err error
}

func (m *errorCallAuthorizer) CanCall(ctx context.Context, fromNumber, toNumber string) (bool, error) {
	return false, m.err
}

func TestRelayCallFlow(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	// Register two mock connections
	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	// Phone 1 calls Phone 2
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})

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
	_ = hub.Register("3140001", conn1)

	// Call offline phone
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140099"})

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
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	// Test 1: Phone 1 calls Phone 2 → ring delivered (authorized)
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})

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
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140003"})

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
	_ = hub.Register("3140003", conn3)

	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140003"})

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
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})

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
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)
	_ = hub.Register("3140003", conn3)

	// Phone 1 calls Phone 2 (establishes active call)
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

	// Phone 3 calls Phone 2 (busy) -- should get busy signal
	relay.HandleMessage(context.Background(), "3140003", &Message{Type: TypeCall, To: "3140002"})

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

	// Phone 1 (host of the active call with Phone 2) tries to call Phone 3.
	// This is the party-line add-third-party flow: the host may initiate a
	// second call while busy as caller, so Phone 3 should ring rather than
	// Phone 1 receiving busy.
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140003"})

	select {
	case data := <-conn3.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if msg.Type != TypeRing {
			t.Fatalf("expected ring for add-dial target, got: %+v", msg)
		}
	default:
		t.Fatal("phone 3 did not receive ring from add-dial")
	}

	// Phone 1 should NOT have received busy for the add-dial.
	select {
	case data := <-conn1.Send:
		msg, _ := ParseMessage(data)
		if msg.Type == TypeBusy {
			t.Fatalf("phone 1 got busy during legitimate add-dial")
		}
	default:
		// correct -- no spurious busy
	}
}

// TestRelayAddDialRejectedForNonHost covers the contrapositive: a party that
// is busy as CALLEE (not the original caller) may not initiate an add-dial.
// This enforces the "host-initiated only" rule from 90s residential TWC.
func TestRelayAddDialRejectedForNonHost(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	conn3 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)
	_ = hub.Register("3140003", conn3)

	// Phone 1 calls Phone 2 -- Phone 2 is now the CALLEE.
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

	// Phone 2 (callee, not host) tries to add Phone 3 -- must be rejected.
	relay.HandleMessage(context.Background(), "3140002", &Message{Type: TypeCall, To: "3140003"})

	select {
	case data := <-conn2.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if msg.Type != TypeBusy {
			t.Fatalf("expected busy for non-host add attempt, got: %+v", msg)
		}
	default:
		t.Fatal("phone 2 did not receive busy when attempting add as non-host")
	}

	// Phone 3 must not have rung.
	select {
	case data := <-conn3.Send:
		msg, _ := ParseMessage(data)
		t.Fatalf("phone 3 should not have rung from non-host add, got: %+v", msg)
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
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	// Phone 1 calls Phone 2
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

	if !tracker.Busy(context.Background(), "3140001") {
		t.Fatal("expected 3140001 to be busy after call initiated")
	}

	// Phone 1 hangs up WITHOUT specifying To (reproduces the client bug)
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeHangup})

	// Tracker should have resolved the peer and ended the call
	if tracker.Busy(context.Background(), "3140001") {
		t.Fatal("expected 3140001 to no longer be busy after hangup")
	}
	if tracker.Busy(context.Background(), "3140002") {
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

// TestRegression_HangupBeforeAnswer_StopsRing reproduces the reported regression:
// D1 calls D3; D1 hangs up before D3 answers; D3 must receive the hangup so it
// stops ringing. Covers the pre-answer path specifically.
func TestRegression_HangupBeforeAnswer_StopsRing(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	d1 := &Conn{Send: make(chan []byte, 20)}
	d3 := &Conn{Send: make(chan []byte, 20)}
	_ = hub.Register("5550001", d1)
	_ = hub.Register("5550003", d3)

	// D1 calls D3
	relay.HandleMessage(context.Background(), "5550001", &Message{Type: TypeCall, To: "5550003"})
	got := <-d3.Send
	msg, _ := ParseMessage(got)
	if msg.Type != TypeRing || msg.From != "5550001" {
		t.Fatalf("expected ring to D3 from D1, got %+v", msg)
	}

	// D1 hangs up before D3 answers (no OnCallAnswered fired)
	relay.HandleMessage(context.Background(), "5550001", &Message{Type: TypeHangup, To: "5550003"})

	// D3 must receive the hangup or it will keep ringing
	select {
	case data := <-d3.Send:
		m, _ := ParseMessage(data)
		if m.Type != TypeHangup {
			t.Fatalf("expected hangup to D3, got %s", m.Type)
		}
	default:
		t.Fatal("REGRESSION: D3 did not receive hangup, will keep ringing")
	}

	if tracker.Busy(context.Background(), "5550001") {
		t.Fatal("D1 still busy after hangup")
	}
}

func TestRelayICERestartForwarded(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	// Establish an active call first
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

	// Phone 1 sends ICE restart to Phone 2
	relay.HandleMessage(context.Background(), "3140001", &Message{
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
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	// Phone 1 sends ICE restart without an active call
	relay.HandleMessage(context.Background(), "3140001", &Message{
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

func TestRelayDTMFForwardedDuringCall(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	// Establish an active call first.
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:  TypeDTMF,
		To:    "3140002",
		Digit: "5",
	})

	select {
	case data := <-conn2.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeDTMF {
			t.Fatalf("expected dtmf, got %s", msg.Type)
		}
		if msg.From != "3140001" {
			t.Fatalf("expected from 3140001, got %s", msg.From)
		}
		if msg.Digit != "5" {
			t.Fatalf("expected digit 5, got %q", msg.Digit)
		}
	default:
		t.Fatal("phone 2 did not receive dtmf")
	}
}

func TestRelayDTMFDroppedWithoutCall(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	// No active call: DTMF must not be relayed.
	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:  TypeDTMF,
		To:    "3140002",
		Digit: "5",
	})

	select {
	case data := <-conn2.Send:
		msg, _ := ParseMessage(data)
		t.Fatalf("target should not receive dtmf without an active call, got: %+v", msg)
	default:
		// correct: dropped
	}
}

func TestRelayOnDisconnectClearsActiveCalls(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)
	relay.GraceWindow = 20 * time.Millisecond

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	conn3 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)
	_ = hub.Register("3140003", conn3)

	// Establish a call: Phone 1 → Phone 2
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

	// Verify Phone 2 is busy
	if !tracker.Busy(context.Background(), "3140002") {
		t.Fatal("expected phone 2 to be busy after call initiated")
	}

	// Simulate Phone 2 disconnecting (WebSocket drops). Because a peer exists,
	// the call is held for GraceWindow before teardown.
	relay.OnDisconnect(context.Background(), "3140002", "")

	// Grace window is running: Phone 1 receives a hangup and state is cleared
	// after the window expires.
	select {
	case data := <-conn1.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeHangup {
			t.Fatalf("expected hangup for peer after grace expiry, got: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("peer (phone 1) did not receive hangup after grace window expired")
	}

	// After grace expiry the tracker has cleared phone 2's call state.
	if tracker.Busy(context.Background(), "3140002") {
		t.Fatal("expected phone 2 to not be busy after grace expiry")
	}

	// Phone 3 can now call Phone 2 (once reconnected)
	newConn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", newConn2)
	relay.HandleMessage(context.Background(), "3140003", &Message{Type: TypeCall, To: "3140002"})

	select {
	case data := <-newConn2.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeRing {
			t.Fatalf("expected ring on reconnected phone 2, got: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnected phone 2 did not receive ring")
	}
}

func TestHubOnlineNumbers(t *testing.T) {
	hub := NewHub()
	conn1 := &Conn{Send: make(chan []byte, 1)}
	conn2 := &Conn{Send: make(chan []byte, 1)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	nums := hub.OnlineNumbers()
	if len(nums) != 2 {
		t.Fatalf("expected 2 online, got %d", len(nums))
	}
}

// TestHandleHangup_EndsAllActivePeers verifies that when a host with two active
// 2-party calls (ADD_* flow) hangs up, both peers receive TypeHangup and both
// calls are removed from the tracker.
func TestHandleHangup_EndsAllActivePeers(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	aConn := &Conn{Send: make(chan []byte, 10)}
	bConn := &Conn{Send: make(chan []byte, 10)}
	cConn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("5550001", aConn)
	_ = hub.Register("5550002", bConn)
	_ = hub.Register("5550003", cConn)

	// Prime two active 2-party calls: A-B (held) and A-C (active).
	tracker.onCallInitiated("5550001", "5550002")
	tracker.onCallInitiated("5550001", "5550003")

	// A hangs up (targets C, the active add peer, but B is also an active peer).
	relay.HandleMessage(context.Background(), "5550001", &Message{Type: TypeHangup, To: "5550003"})

	// Both B and C must receive TypeHangup.
	bMsgs := drainConnUnit(t, bConn)
	cMsgs := drainConnUnit(t, cConn)
	if countTypeUnit(bMsgs, TypeHangup) != 1 {
		t.Fatalf("B: expected 1 TypeHangup, got %d", countTypeUnit(bMsgs, TypeHangup))
	}
	if countTypeUnit(cMsgs, TypeHangup) != 1 {
		t.Fatalf("C: expected 1 TypeHangup, got %d", countTypeUnit(cMsgs, TypeHangup))
	}

	// Both calls must be removed from the tracker.
	if tracker.Busy(context.Background(), "5550001") {
		t.Error("A should no longer be busy after hangup")
	}
	if tracker.Busy(context.Background(), "5550002") {
		t.Error("B should no longer be busy after hangup")
	}
	if tracker.Busy(context.Background(), "5550003") {
		t.Error("C should no longer be busy after hangup")
	}

	// Verify OnCallEnded was recorded for both peers.
	endedAB := false
	endedAC := false
	for _, e := range tracker.ended {
		if e == "5550001→5550002" || e == "5550002→5550001" {
			endedAB = true
		}
		if e == "5550001→5550003" || e == "5550003→5550001" {
			endedAC = true
		}
	}
	if !endedAB {
		t.Errorf("expected OnCallEnded for A-B, ended=%v", tracker.ended)
	}
	if !endedAC {
		t.Errorf("expected OnCallEnded for A-C, ended=%v", tracker.ended)
	}
}

// drainConnUnit reads all buffered messages from a Conn without the integration
// test helpers (no ParseMessage from signaling_test package).
func drainConnUnit(t *testing.T, conn *Conn) []*Message {
	t.Helper()
	var out []*Message
	for {
		select {
		case data := <-conn.Send:
			msg, err := ParseMessage(data)
			if err != nil {
				t.Fatalf("parse message: %v", err)
			}
			out = append(out, msg)
		default:
			return out
		}
	}
}

func countTypeUnit(msgs []*Message, typ string) int {
	n := 0
	for _, m := range msgs {
		if m.Type == typ {
			n++
		}
	}
	return n
}

func TestRelayRestartMessageNotPanics(t *testing.T) {
	hub := NewHub()
	relay := NewRelay(hub, nil, nil, nil)

	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)

	// Restart messages are server->device, not relayed through HandleMessage.
	// But verify it doesn't crash if one passes through.
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeRestart, RestartMode: "service"})
}

func TestLinkHealthDispatchRecordsSample(t *testing.T) {
	tr := newMockTracker()
	// Seed an active call so CallIDFor("555-1111") returns (42, true).
	tr.calls["555-1111→555-2222"] = true
	tr.callIDs["555-1111→555-2222"] = 42

	store := &fakeHealthRecorder{}
	r := &Relay{Tracker: tr, HealthStore: store}

	loss := float32(0.5)
	msg := &Message{
		Type: TypeLinkHealth,
		From: "555-OTHER", // forged -- MUST be ignored
		LinkHealth: &LinkHealthPayload{
			TS:      1714000000000,
			LossPct: &loss,
		},
	}
	r.handleLinkHealth(context.Background(), "555-1111", msg)

	if len(store.records) != 1 {
		t.Fatalf("record count: got %d want 1", len(store.records))
	}
	got := store.records[0]
	if got.callID != 42 {
		t.Fatalf("callID: got %d want 42", got.callID)
	}
	if got.endpoint != "555-1111" {
		t.Fatalf("endpoint: got %q want %q (forged From must be ignored)", got.endpoint, "555-1111")
	}
	if got.sample.LossPct == nil || *got.sample.LossPct != 0.5 {
		t.Fatalf("loss: %+v", got.sample)
	}
}

func TestLinkHealthDispatchDropsForUnknownCall(t *testing.T) {
	tr := newMockTracker()
	// No active calls -- CallIDFor returns (0, false).
	store := &fakeHealthRecorder{}
	r := &Relay{Tracker: tr, HealthStore: store}

	loss := float32(0.5)
	r.handleLinkHealth(context.Background(), "555-1111", &Message{
		Type:       TypeLinkHealth,
		LinkHealth: &LinkHealthPayload{TS: 1, LossPct: &loss},
	})
	if len(store.records) != 0 {
		t.Fatalf("expected drop; got %d records", len(store.records))
	}
}

func TestLinkHealthDispatchIgnoresNilPayload(t *testing.T) {
	tr := newMockTracker()
	tr.calls["555-1111→555-2222"] = true
	tr.callIDs["555-1111→555-2222"] = 42

	store := &fakeHealthRecorder{}
	r := &Relay{Tracker: tr, HealthStore: store}

	r.handleLinkHealth(context.Background(), "555-1111", &Message{Type: TypeLinkHealth, LinkHealth: nil})
	if len(store.records) != 0 {
		t.Fatalf("expected drop on nil LinkHealth; got %d", len(store.records))
	}
}

func TestRelayForceHangupSendsToBothPeers(t *testing.T) {
	hub := NewHub()
	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("555-1111", conn1)
	_ = hub.Register("555-2222", conn2)

	r := &Relay{Hub: hub}
	r.ForceHangup(context.Background(), "555-1111", "555-2222")

	for _, tc := range []struct {
		name string
		ch   chan []byte
	}{
		{"555-1111", conn1.Send},
		{"555-2222", conn2.Send},
	} {
		select {
		case data := <-tc.ch:
			msg, err := ParseMessage(data)
			if err != nil {
				t.Fatalf("%s: parse error: %v", tc.name, err)
			}
			if msg.Type != TypeHangup {
				t.Fatalf("%s: expected TypeHangup, got %s", tc.name, msg.Type)
			}
		default:
			t.Fatalf("%s: did not receive hangup", tc.name)
		}
	}
}

func TestRelayForceHangupTolerantOfOnePeerOffline(t *testing.T) {
	hub := NewHub()
	// 555-1111 is NOT registered (offline); only 555-2222 is online.
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("555-2222", conn2)

	r := &Relay{Hub: hub}

	// Must not panic, must not block, must still deliver to the online peer.
	r.ForceHangup(context.Background(), "555-1111", "555-2222")

	select {
	case data := <-conn2.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if msg.Type != TypeHangup {
			t.Fatalf("expected TypeHangup, got %s", msg.Type)
		}
	default:
		t.Fatal("555-2222 did not receive hangup despite being online")
	}
}

func TestHandleLinkHealth_3WayPath_RecordsEdge(t *testing.T) {
	tracker := newMockTracker()
	conf, err := tracker.conferences.CreateConference(context.Background(), "A", 1, []string{"B", "C"})
	if err != nil {
		t.Fatalf("CreateConference: %v", err)
	}
	store := &fakeHealthRecorder{}
	r := &Relay{Tracker: tracker, HealthStore: store}

	loss := float32(4.2)
	m := &Message{
		Type:       TypeLinkHealth,
		LinkHealth: &LinkHealthPayload{TS: 1, LossPct: &loss, Peer: "B"},
	}
	r.handleLinkHealth(context.Background(), "A", m)

	if len(store.edges) != 1 {
		t.Fatalf("expected 1 RecordEdge; got %d", len(store.edges))
	}
	e := store.edges[0]
	if e.ConfID != conf.ID {
		t.Fatalf("ConfID: got %s want %s", e.ConfID, conf.ID)
	}
	if e.From != "A" || e.Peer != "B" {
		t.Fatalf("From/Peer: got %s/%s want A/B", e.From, e.Peer)
	}
	if e.Sample.LossPct == nil || *e.Sample.LossPct != 4.2 {
		t.Fatalf("sample LossPct not preserved")
	}
}

func TestHandleLinkHealth_3WayPath_BogusPeerDropped(t *testing.T) {
	tracker := newMockTracker()
	if _, err := tracker.conferences.CreateConference(context.Background(), "A", 1, []string{"B", "C"}); err != nil {
		t.Fatalf("CreateConference: %v", err)
	}
	store := &fakeHealthRecorder{}
	r := &Relay{Tracker: tracker, HealthStore: store}

	m := &Message{
		Type:       TypeLinkHealth,
		LinkHealth: &LinkHealthPayload{TS: 1, Peer: "Z"}, // Z not in conference
	}
	r.handleLinkHealth(context.Background(), "A", m)

	if len(store.edges) != 0 {
		t.Fatalf("bogus peer must be dropped; got %d edges", len(store.edges))
	}
}

func TestHandleLinkHealth_3WayPath_FromNotInConferenceDropped(t *testing.T) {
	tracker := newMockTracker() // no conference created
	store := &fakeHealthRecorder{}
	r := &Relay{Tracker: tracker, HealthStore: store}

	m := &Message{
		Type:       TypeLinkHealth,
		LinkHealth: &LinkHealthPayload{TS: 1, Peer: "B"},
	}
	r.handleLinkHealth(context.Background(), "A", m)

	if len(store.edges) != 0 {
		t.Fatalf("from-not-in-conference must drop; got %d edges", len(store.edges))
	}
}

func TestHandleLinkHealth_2WayPath_UnchangedBehavior(t *testing.T) {
	tracker := newMockTracker()
	tracker.onCallInitiated("A", "B")
	tracker.setCallID("A", "B", 42)
	store := &fakeHealthRecorder{}
	r := &Relay{Tracker: tracker, HealthStore: store}

	loss := float32(0.5)
	m := &Message{
		Type:       TypeLinkHealth,
		LinkHealth: &LinkHealthPayload{TS: 1, LossPct: &loss},
	}
	r.handleLinkHealth(context.Background(), "A", m)

	if len(store.edges) != 0 {
		t.Fatalf("2-party path must not touch RecordEdge; got %d edges", len(store.edges))
	}
	if len(store.records) != 1 {
		t.Fatalf("expected 1 Record for 2-party path; got %d", len(store.records))
	}
	rec := store.records[0]
	if rec.callID != 42 {
		t.Fatalf("callID: got %d want 42", rec.callID)
	}
	if rec.endpoint != "A" {
		t.Fatalf("endpoint: got %q want A", rec.endpoint)
	}
	if rec.sample.LossPct == nil || *rec.sample.LossPct != 0.5 {
		t.Fatalf("sample LossPct not preserved: %+v", rec.sample)
	}
}

// fakeMetricsObserver records every event passed to the MetricsObserver.
// Error categories are kept as a slice rather than a counter so the order of
// observations is also verifiable; ordering matters when we want to confirm
// the relay's first error wins instead of doubling up. Candidate and
// ICE-server events are recorded so tests can assert the relay reports
// media-negotiation telemetry derived from the parsed candidate, never from
// raw user input.
type fakeMetricsObserver struct {
	seen       []string    // signaling-error categories
	candidates [][2]string // (cand_type, transport) pairs
	turnIssued []bool
}

func (f *fakeMetricsObserver) ObserveSignalingError(category string) {
	f.seen = append(f.seen, category)
}

func (f *fakeMetricsObserver) ObserveICECandidate(candType, transport string) {
	f.candidates = append(f.candidates, [2]string{candType, transport})
}

func (f *fakeMetricsObserver) ObserveICEServersIssued(turn bool) {
	f.turnIssued = append(f.turnIssued, turn)
}

func TestRelayDoesNotErrorOnOfflineCall(t *testing.T) {
	hub := NewHub()
	obs := &fakeMetricsObserver{}
	relay := NewRelay(hub, nil, nil, nil)
	relay.Metrics = obs

	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140099"})

	// Dialing an offline or unregistered number is normal user behavior. The
	// caller is told it didn't connect, but nothing is counted as an error.
	select {
	case data := <-conn1.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeError || msg.Error != "phone not connected" {
			t.Fatalf("expected 'phone not connected' error, got %s/%q", msg.Type, msg.Error)
		}
	default:
		t.Fatal("caller did not receive a not-connected reply")
	}
	if len(obs.seen) != 0 {
		t.Fatalf("dialing an unreachable number must not be recorded as an error, got %v", obs.seen)
	}
}

func TestRelayObservesAuthFailedWhenAuthorizerDenies(t *testing.T) {
	hub := NewHub()
	obs := &fakeMetricsObserver{}
	authorizer := &mockCallAuthorizer{allowed: map[[2]string]bool{}}
	relay := NewRelay(hub, newMockTracker(), authorizer, nil)
	relay.Metrics = obs

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})

	if len(obs.seen) != 1 || obs.seen[0] != "auth_failed" {
		t.Fatalf("expected auth_failed, got %v", obs.seen)
	}
}

func TestRelayDoesNotErrorOnSignalingWithoutCall(t *testing.T) {
	// Stray control/media for a call that isn't active (raced past teardown or
	// trailing a dial to an unreachable number) is normal and must not be
	// recorded as a signaling error. Where a client-facing reply is part of the
	// contract (ice_restart), it must still be delivered.
	for _, tc := range []struct {
		msg          *Message
		wantReplyErr string // "" means no reply expected
	}{
		{&Message{Type: TypeICERestart, To: "3140002"}, "no active call"},
		{&Message{Type: TypeDTMF, To: "3140002", Digit: "5"}, ""},
		{&Message{Type: TypeAnswer, To: "3140002"}, ""},
		{&Message{Type: TypeSDP, To: "3140002", SDP: "v=0\r\n"}, ""},
		{&Message{Type: TypeICE, To: "3140002", Candidate: "candidate:1 1 udp 1 1.2.3.4 5 typ host"}, ""},
	} {
		hub := NewHub()
		obs := &fakeMetricsObserver{}
		relay := NewRelay(hub, newMockTracker(), nil, nil)
		relay.Metrics = obs

		conn1 := &Conn{Send: make(chan []byte, 10)}
		_ = hub.Register("3140001", conn1)
		relay.HandleMessage(context.Background(), "3140001", tc.msg)

		if len(obs.seen) != 0 {
			t.Fatalf("%s without active call must not be an error, got %v", tc.msg.Type, obs.seen)
		}

		select {
		case data := <-conn1.Send:
			reply, err := ParseMessage(data)
			if err != nil {
				t.Fatalf("%s: parse reply: %v", tc.msg.Type, err)
			}
			if tc.wantReplyErr == "" {
				t.Fatalf("%s without active call should send no reply, got %+v", tc.msg.Type, reply)
			}
			if reply.Type != TypeError || reply.Error != tc.wantReplyErr {
				t.Fatalf("%s: expected error reply %q, got %s/%q", tc.msg.Type, tc.wantReplyErr, reply.Type, reply.Error)
			}
		default:
			if tc.wantReplyErr != "" {
				t.Fatalf("%s without active call should reply %q, got nothing", tc.msg.Type, tc.wantReplyErr)
			}
		}
	}
}

func TestRelayObservesInvalidMessageOnEmptyDestination(t *testing.T) {
	// A relay message with no destination is malformed, a genuine fault. It is
	// counted as invalid_message and dropped before the active-call guard, so it
	// is not shadowed into that guard's benign drop.
	for _, msg := range []*Message{
		{Type: TypeICERestart},
		{Type: TypeDTMF, Digit: "5"},
		{Type: TypeAnswer},
		{Type: TypeSDP, SDP: "v=0\r\n"},
		{Type: TypeICE, Candidate: "candidate:1 1 udp 1 1.2.3.4 5 typ host"},
	} {
		hub := NewHub()
		obs := &fakeMetricsObserver{}
		relay := NewRelay(hub, newMockTracker(), nil, nil)
		relay.Metrics = obs

		conn1 := &Conn{Send: make(chan []byte, 10)}
		_ = hub.Register("3140001", conn1)
		relay.HandleMessage(context.Background(), "3140001", msg)

		if len(obs.seen) != 1 || obs.seen[0] != "invalid_message" {
			t.Fatalf("%s with empty destination: expected invalid_message, got %v", msg.Type, obs.seen)
		}
	}
}

func TestRelayNilObserverIsSafe(t *testing.T) {
	hub := NewHub()
	relay := NewRelay(hub, nil, nil, nil)
	// Errors is nil; the relay must not panic when it tries to observe.
	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140099"})
	// No assertion: the test passes if it didn't panic.
}

func TestRelayCallReturn(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)

	tracker.lastInboundCaller = "3140002"

	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCallReturn})

	select {
	case data := <-conn1.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeCallReturnResult {
			t.Fatalf("expected call_return_result, got %q", msg.Type)
		}
		if msg.Number != "3140002" {
			t.Fatalf("expected number 3140002, got %q", msg.Number)
		}
	default:
		t.Fatal("phone did not receive call_return_result")
	}
}

func TestRelayCallReturnNoCalls(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)

	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCallReturn})

	select {
	case data := <-conn1.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeCallReturnResult {
			t.Fatalf("expected call_return_result, got %q", msg.Type)
		}
		if msg.Number != "" {
			t.Fatalf("expected empty number, got %q", msg.Number)
		}
	default:
		t.Fatal("phone did not receive call_return_result")
	}
}

func TestRelayCallReturnRetryAndTrigger(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", &Conn{Send: make(chan []byte, 10)})

	tracker.lastInboundCaller = "3140002"

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type: TypeCallReturnRetry, Number: "3140002",
	})

	select {
	case data := <-conn1.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeCallReturnRing {
			t.Fatalf("expected call_return_ring, got %q", msg.Type)
		}
		if msg.Number != "3140002" {
			t.Fatalf("expected number 3140002, got %q", msg.Number)
		}
	default:
		t.Fatal("phone did not receive call_return_ring")
	}
}

func TestRelayCallReturnRetryBusyThenFree(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", &Conn{Send: make(chan []byte, 10)})

	tracker.onCallInitiated("3140002", "3140003")

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type: TypeCallReturnRetry, Number: "3140002",
	})

	select {
	case <-conn1.Send:
		t.Fatal("should not fire while target busy")
	default:
	}

	delete(tracker.calls, "3140002→3140003")
	relay.OnCallEndedNotify(context.Background(), "3140002", "3140003")

	select {
	case data := <-conn1.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeCallReturnRing {
			t.Fatalf("expected call_return_ring, got %q", msg.Type)
		}
	default:
		t.Fatal("phone did not receive call_return_ring after target freed")
	}
}

func TestRelayCallReturnCancel(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", &Conn{Send: make(chan []byte, 10)})

	tracker.onCallInitiated("3140002", "3140003")

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type: TypeCallReturnRetry, Number: "3140002",
	})

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type: TypeCallReturnCancel,
	})

	select {
	case data := <-conn1.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeCallReturnCancelled {
			t.Fatalf("expected call_return_cancelled, got %q", msg.Type)
		}
	default:
		t.Fatal("phone did not receive call_return_cancelled")
	}

	delete(tracker.calls, "3140002→3140003")
	relay.OnCallEndedNotify(context.Background(), "3140002", "3140003")

	select {
	case <-conn1.Send:
		t.Fatal("should not fire after cancel")
	default:
	}
}

func TestRelayCallReturnExpiry(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", &Conn{Send: make(chan []byte, 10)})

	tracker.onCallInitiated("3140002", "3140003")

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type: TypeCallReturnRetry, Number: "3140002",
	})

	relay.pendingReturnsMu.Lock()
	if p, ok := relay.pendingReturns["3140001"]; ok {
		p.ExpiresAt = time.Now().Add(-1 * time.Second)
	}
	relay.pendingReturnsMu.Unlock()

	delete(tracker.calls, "3140002→3140003")
	relay.OnCallEndedNotify(context.Background(), "3140002", "3140003")

	select {
	case <-conn1.Send:
		t.Fatal("should not fire after expiry")
	default:
	}
}

func TestRelayVoicemailStateUpdatesHub(t *testing.T) {
	hub := NewHub()
	relay := NewRelay(hub, nil, nil, nil)

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:                  TypeVoicemailState,
		HardwareID:            "hw-a",
		VoicemailUnheardCount: 4,
	})

	if got := hub.LineVoicemailUnheard("3140001"); got != 4 {
		t.Errorf("LineVoicemailUnheard: got %d, want 4", got)
	}
}

func TestRelayVoicemailStateMissingHardwareIDIsDropped(t *testing.T) {
	hub := NewHub()
	relay := NewRelay(hub, nil, nil, nil)

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:                  TypeVoicemailState,
		VoicemailUnheardCount: 9,
	})

	if got := hub.LineVoicemailUnheard("3140001"); got != 0 {
		t.Errorf("missing hardware_id should drop the message, got count %d", got)
	}
}

func TestRelayVoicemailStateZeroExplicitlyOverwrites(t *testing.T) {
	hub := NewHub()
	relay := NewRelay(hub, nil, nil, nil)

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type: TypeVoicemailState, HardwareID: "hw-a", VoicemailUnheardCount: 5,
	})
	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type: TypeVoicemailState, HardwareID: "hw-a", VoicemailUnheardCount: 0,
	})

	if got := hub.LineVoicemailUnheard("3140001"); got != 0 {
		t.Errorf("explicit zero should overwrite, got %d", got)
	}
}

// TestHandleCallGraceHeldLineReturnsBusy verifies Fix I1: when the callee is
// offline (grace window holding the call) but still Busy, a new caller
// receives TypeBusy instead of TypeError "phone not connected".
func TestHandleCallGraceHeldLineReturnsBusy(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	// 3140002 is in a call with 3140003 but its WebSocket is offline (grace window).
	tracker.onCallInitiated("3140002", "3140003")

	relay := NewRelay(hub, tracker, nil, nil)

	caller := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", caller)
	// 3140002 is intentionally NOT registered so IsOnline returns false.

	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})

	select {
	case data := <-caller.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeBusy {
			t.Fatalf("expected TypeBusy for grace-held line, got type=%q error=%q", msg.Type, msg.Error)
		}
	default:
		t.Fatal("caller did not receive any message")
	}
}

// TestHandleCallOfflineAndIdleReturnsNotConnected verifies that the busy-first
// check does not regress the existing "offline and not in a call" path: the
// caller should still receive TypeError "phone not connected".
func TestHandleCallOfflineAndIdleReturnsNotConnected(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	// 3140002 is offline and not in any call.

	relay := NewRelay(hub, tracker, nil, nil)

	caller := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", caller)

	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})

	select {
	case data := <-caller.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeError {
			t.Fatalf("expected TypeError for offline idle line, got type=%q", msg.Type)
		}
		if msg.Error != "phone not connected" {
			t.Fatalf("expected 'phone not connected', got %q", msg.Error)
		}
	default:
		t.Fatal("caller did not receive any message")
	}
}

func TestRelayObservesICECandidateOnForward(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	obs := &fakeMetricsObserver{}
	relay := NewRelay(hub, tracker, nil, nil)
	relay.Metrics = obs

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	// Establish an active call so the forward is permitted.
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send // drain ring

	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:      TypeICE,
		To:        "3140002",
		Candidate: "candidate:1 1 udp 1686052607 203.0.113.7 49152 typ srflx raddr 192.168.1.42 rport 54321",
	})

	if len(obs.candidates) != 1 {
		t.Fatalf("expected 1 candidate observation, got %v", obs.candidates)
	}
	if obs.candidates[0] != [2]string{"srflx", "udp"} {
		t.Fatalf("expected srflx/udp, got %v", obs.candidates[0])
	}
	// The candidate must still be relayed verbatim to the peer.
	select {
	case data := <-conn2.Send:
		msg, _ := ParseMessage(data)
		if msg.Type != TypeICE || msg.Candidate == "" {
			t.Fatalf("peer did not receive the ice candidate: %+v", msg)
		}
	default:
		t.Fatal("peer did not receive the ice candidate")
	}
}

func TestRelayDoesNotObserveUnparseableCandidate(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	obs := &fakeMetricsObserver{}
	relay := NewRelay(hub, tracker, nil, nil)
	relay.Metrics = obs

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send

	// An end-of-candidates marker (empty candidate) must not be counted.
	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:      TypeICE,
		To:        "3140002",
		Candidate: "",
	})

	if len(obs.candidates) != 0 {
		t.Fatalf("empty candidate should not be observed, got %v", obs.candidates)
	}
}

func TestRelayObservesMalformedCandidateAsOther(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	obs := &fakeMetricsObserver{}
	relay := NewRelay(hub, tracker, nil, nil)
	relay.Metrics = obs

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})
	<-conn2.Send

	// A non-empty but unparseable candidate is still counted so a
	// malformed-candidate spike is visible on a dashboard. The relay passes the
	// empty parsed type/transport through; the metrics registry's label
	// allowlist collapses those to other/other (see metrics_test.go).
	relay.HandleMessage(context.Background(), "3140001", &Message{
		Type:      TypeICE,
		To:        "3140002",
		Candidate: "totally not a candidate line",
	})

	if len(obs.candidates) != 1 {
		t.Fatalf("expected 1 candidate observation, got %v", obs.candidates)
	}
	if obs.candidates[0] != [2]string{"", ""} {
		t.Fatalf("expected empty type/transport for malformed candidate, got %v", obs.candidates[0])
	}
}

func TestRelayObservesICEServersIssued(t *testing.T) {
	hub := NewHub()
	obs := &fakeMetricsObserver{}
	relay := NewRelay(hub, newMockTracker(), nil, nil)
	relay.Metrics = obs

	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)

	// No TURN configured: should report turn=false.
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeRequestICE, HardwareID: "hw-1"})

	if len(obs.turnIssued) != 1 || obs.turnIssued[0] != false {
		t.Fatalf("expected one turn=false issuance, got %v", obs.turnIssued)
	}
	// Drain the ice-servers response.
	<-conn1.Send
}
