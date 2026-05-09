package signaling

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/justinlindh/digits/server/internal/calls"
)

type mockTracker struct {
	initiated          []string
	answered           []string
	ended              []string
	calls              map[string]bool  // "a→b" keys for active calls
	callIDs            map[string]int64 // "a→b" keys for active call IDs
	conferences        *calls.ConferenceTracker
	lastInboundCaller  string
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
	for k := range m.calls {
		a, b, _ := strings.Cut(k, "→")
		if a == number || b == number {
			delete(m.calls, k)
			delete(m.callIDs, k)
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

func (m *mockTracker) CanAddAsHost(number string) bool {
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

func (m *mockTracker) AllPeersOf(number string) []string {
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
	return m.conferences.CreateConference(host, originatingCallID, addedMembers)
}

func (m *mockTracker) CallIDForPair(a, b string) int64 {
	if id, ok := m.callIDs[a+"→"+b]; ok {
		return id
	}
	if id, ok := m.callIDs[b+"→"+a]; ok {
		return id
	}
	return 0
}

func (m *mockTracker) CallIDFor(number string) (int64, bool) {
	for k, id := range m.callIDs {
		a, b, _ := strings.Cut(k, "→")
		if a == number || b == number {
			return id, true
		}
	}
	return 0, false
}

func (m *mockTracker) EndConferencePersistent(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := m.conferences.EndConference(id, reason)
	return err
}

func (m *mockTracker) DropMemberPersistent(ctx context.Context, id uuid.UUID, phone, reason string) ([]string, bool, error) {
	return m.conferences.DropMember(id, phone, reason)
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

	if !tracker.Busy("3140001") {
		t.Fatal("expected 3140001 to be busy after call initiated")
	}

	// Phone 1 hangs up WITHOUT specifying To (reproduces the client bug)
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeHangup})

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

	if tracker.Busy("5550001") {
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

func TestRelayOnDisconnectClearsActiveCalls(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, nil)

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
	if !tracker.Busy("3140002") {
		t.Fatal("expected phone 2 to be busy after call initiated")
	}

	// Simulate Phone 2 disconnecting (WebSocket drops)
	relay.OnDisconnect(context.Background(), "3140002", "")

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
	_ = hub.Register("3140002", newConn2)
	relay.HandleMessage(context.Background(), "3140003", &Message{Type: TypeCall, To: "3140002"})

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
	if tracker.Busy("5550001") {
		t.Error("A should no longer be busy after hangup")
	}
	if tracker.Busy("5550002") {
		t.Error("B should no longer be busy after hangup")
	}
	if tracker.Busy("5550003") {
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
	conf, err := tracker.conferences.CreateConference("A", 1, []string{"B", "C"})
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
	if _, err := tracker.conferences.CreateConference("A", 1, []string{"B", "C"}); err != nil {
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

// fakeErrorObserver records every category passed to ObserveSignalingError-
// Category. Tests assert against the slice rather than a counter so the
// order of observations is also verifiable; ordering matters when we want
// to confirm the relay's first error wins instead of doubling up.
type fakeErrorObserver struct {
	seen []string
}

func (f *fakeErrorObserver) ObserveSignalingErrorCategory(category string) {
	f.seen = append(f.seen, category)
}

func TestRelayObservesPeerUnreachableOnOfflineCall(t *testing.T) {
	hub := NewHub()
	obs := &fakeErrorObserver{}
	relay := NewRelay(hub, nil, nil, nil)
	relay.Errors = obs

	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140099"})

	if len(obs.seen) != 1 || obs.seen[0] != "peer_unreachable" {
		t.Fatalf("expected peer_unreachable, got %v", obs.seen)
	}
}

func TestRelayObservesAuthFailedWhenAuthorizerDenies(t *testing.T) {
	hub := NewHub()
	obs := &fakeErrorObserver{}
	authorizer := &mockCallAuthorizer{allowed: map[[2]string]bool{}}
	relay := NewRelay(hub, newMockTracker(), authorizer, nil)
	relay.Errors = obs

	conn1 := &Conn{Send: make(chan []byte, 10)}
	conn2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	_ = hub.Register("3140002", conn2)

	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeCall, To: "3140002"})

	if len(obs.seen) != 1 || obs.seen[0] != "auth_failed" {
		t.Fatalf("expected auth_failed, got %v", obs.seen)
	}
}

func TestRelayObservesInvalidMessageOnICERestartWithoutCall(t *testing.T) {
	hub := NewHub()
	obs := &fakeErrorObserver{}
	relay := NewRelay(hub, newMockTracker(), nil, nil)
	relay.Errors = obs

	conn1 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn1)
	relay.HandleMessage(context.Background(), "3140001", &Message{Type: TypeICERestart, To: "3140002"})

	if len(obs.seen) != 1 || obs.seen[0] != "invalid_message" {
		t.Fatalf("expected invalid_message, got %v", obs.seen)
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
