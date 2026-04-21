//go:build integration

package signaling_test

import (
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/signaling"
)

// openSignalingTestDB opens the test database via TEST_DATABASE_URL and
// cleans up all conference-related tables after the test.
func openSignalingTestDB(t *testing.T) *db.Database {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Clean up any leftovers from prior runs before the test starts.
	// Order matters: break the circular FK by nulling continuation calls first.
	cleanDB := func() {
		_, _ = d.DB.Exec("UPDATE calls SET originating_conference_id = NULL WHERE originating_conference_id IS NOT NULL")
		_, _ = d.DB.Exec("DELETE FROM conference_members")
		_, _ = d.DB.Exec("DELETE FROM conferences")
		_, _ = d.DB.Exec("DELETE FROM calls")
	}
	cleanDB()
	t.Cleanup(func() {
		cleanDB()
		_ = d.Close()
	})
	return d
}

// alwaysAllow implements signaling.CallAuthorizer by permitting every call.
type alwaysAllow struct{}

func (alwaysAllow) CanCall(_, _ string) (bool, error) { return true, nil }

// drainConn reads all buffered messages from a single Conn, returning them decoded.
func drainConn(t *testing.T, conn *signaling.Conn) []*signaling.Message {
	t.Helper()
	var out []*signaling.Message
	for {
		select {
		case data := <-conn.Send:
			msg, err := signaling.ParseMessage(data)
			if err != nil {
				t.Fatalf("parse message: %v", err)
			}
			out = append(out, msg)
		default:
			return out
		}
	}
}

func countType(msgs []*signaling.Message, typ string) int {
	n := 0
	for _, m := range msgs {
		if m.Type == typ {
			n++
		}
	}
	return n
}

func TestConferenceLifecycle_Integration(t *testing.T) {
	d := openSignalingTestDB(t)
	tr := calls.New(d)
	hub := signaling.NewHub()
	r := signaling.NewRelay(hub, tr, alwaysAllow{}, nil)

	aConn := &signaling.Conn{Send: make(chan []byte, 50)}
	bConn := &signaling.Conn{Send: make(chan []byte, 50)}
	cConn := &signaling.Conn{Send: make(chan []byte, 50)}
	hub.Register("5550001", aConn)
	hub.Register("5550002", bConn)
	hub.Register("5550003", cConn)

	// Step 1: Directly prime both active calls via the tracker (bypassing the
	// relay's busy-check, which would block A's second call since A is already
	// in a call with B). This mirrors what the unit tests do with mockTracker.
	//
	// A-B call: initiated + answered (stored in DB, added to active map).
	if _, err := tr.OnCallInitiated("5550001", "5550002"); err != nil {
		t.Fatalf("OnCallInitiated A->B: %v", err)
	}
	if err := tr.OnCallAnswered("5550001", "5550002"); err != nil {
		t.Fatalf("OnCallAnswered A->B: %v", err)
	}

	// A-C call: initiated + answered.
	if _, err := tr.OnCallInitiated("5550001", "5550003"); err != nil {
		t.Fatalf("OnCallInitiated A->C: %v", err)
	}
	if err := tr.OnCallAnswered("5550001", "5550003"); err != nil {
		t.Fatalf("OnCallAnswered A->C: %v", err)
	}

	// Step 3: A sends ConferenceMerge.
	r.HandleMessage("5550001", &signaling.Message{
		Type:       signaling.TypeConferenceMerge,
		HeldPeer:   "5550002",
		ActivePeer: "5550003",
	})

	// --- DB assertions after merge ---

	var dbHost, dbState string
	err := d.DB.QueryRow(`SELECT host_phone, state FROM conferences LIMIT 1`).Scan(&dbHost, &dbState)
	if err != nil {
		t.Fatalf("select conference: %v", err)
	}
	if dbHost != "5550001" {
		t.Fatalf("expected host_phone=5550001, got %q", dbHost)
	}
	if dbState != "active" {
		t.Fatalf("expected state=active, got %q", dbState)
	}

	var dbMembers int
	err = d.DB.QueryRow(`SELECT COUNT(*) FROM conference_members`).Scan(&dbMembers)
	if err != nil {
		t.Fatalf("count conference_members: %v", err)
	}
	if dbMembers != 3 {
		t.Fatalf("expected 3 conference_members rows, got %d", dbMembers)
	}

	// --- Message assertions after merge ---

	aMsgs := drainConn(t, aConn)
	bMsgs := drainConn(t, bConn)
	cMsgs := drainConn(t, cConn)

	if countType(aMsgs, signaling.TypeConferenceMember) != 1 {
		t.Fatalf("A: expected 1 ConferenceMember, got %d", countType(aMsgs, signaling.TypeConferenceMember))
	}
	if countType(bMsgs, signaling.TypeConferenceMember) != 1 {
		t.Fatalf("B: expected 1 ConferenceMember, got %d", countType(bMsgs, signaling.TypeConferenceMember))
	}
	if countType(cMsgs, signaling.TypeConferenceMember) != 1 {
		t.Fatalf("C: expected 1 ConferenceMember, got %d", countType(cMsgs, signaling.TypeConferenceMember))
	}
	if countType(bMsgs, signaling.TypeConferenceConnect) != 1 {
		t.Fatalf("B: expected 1 ConferenceConnect, got %d", countType(bMsgs, signaling.TypeConferenceConnect))
	}
	if countType(cMsgs, signaling.TypeConferenceConnect) != 1 {
		t.Fatalf("C: expected 1 ConferenceConnect, got %d", countType(cMsgs, signaling.TypeConferenceConnect))
	}

	// Verify initiator tiebreak: B (5550002 < 5550003) is initiator, C is not.
	var bInit, cInit bool
	for _, m := range bMsgs {
		if m.Type == signaling.TypeConferenceConnect {
			bInit = m.Initiator
		}
	}
	for _, m := range cMsgs {
		if m.Type == signaling.TypeConferenceConnect {
			cInit = m.Initiator
		}
	}
	if !bInit || cInit {
		t.Fatalf("expected B initiator=true, C initiator=false; got B=%v C=%v", bInit, cInit)
	}

	// Step 4: B hangs up.
	r.HandleMessage("5550002", &signaling.Message{Type: signaling.TypeHangup, To: "5550001"})

	// --- DB assertions after hangup ---

	var endedState, endReason string
	err = d.DB.QueryRow(`SELECT state, COALESCE(end_reason, '') FROM conferences LIMIT 1`).Scan(&endedState, &endReason)
	if err != nil {
		t.Fatalf("select ended conference: %v", err)
	}
	if endedState != "ended" {
		t.Fatalf("expected state=ended, got %q", endedState)
	}
	if endReason != "member_left" {
		t.Fatalf("expected end_reason='member_left', got %q", endReason)
	}

	var continuationCount int
	err = d.DB.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE originating_conference_id IS NOT NULL AND status = 'connected'`,
	).Scan(&continuationCount)
	if err != nil {
		t.Fatalf("count continuation calls: %v", err)
	}
	if continuationCount != 1 {
		t.Fatalf("expected 1 continuation calls row, got %d", continuationCount)
	}

	// --- Message assertions after hangup ---

	aLeaveMsgs := drainConn(t, aConn)
	cLeaveMsgs := drainConn(t, cConn)

	if countType(aLeaveMsgs, signaling.TypeConferenceLeave) != 1 {
		t.Fatalf("A: expected 1 ConferenceLeave, got %d", countType(aLeaveMsgs, signaling.TypeConferenceLeave))
	}
	if countType(cLeaveMsgs, signaling.TypeConferenceLeave) != 1 {
		t.Fatalf("C: expected 1 ConferenceLeave, got %d", countType(cLeaveMsgs, signaling.TypeConferenceLeave))
	}
	if countType(aLeaveMsgs, signaling.TypeConferenceEnd) != 1 {
		t.Fatalf("A: expected 1 ConferenceEnd, got %d", countType(aLeaveMsgs, signaling.TypeConferenceEnd))
	}
	if countType(cLeaveMsgs, signaling.TypeConferenceEnd) != 1 {
		t.Fatalf("C: expected 1 ConferenceEnd, got %d", countType(cLeaveMsgs, signaling.TypeConferenceEnd))
	}
}

// TestHangupDuringADDCalling_Integration verifies that when a host hangs up
// while in ADD_* flow (two active 2-party calls: A-B and A-C), a single
// hook-on causes the server to end both calls and notify both peers.
func TestHangupDuringADDCalling_Integration(t *testing.T) {
	d := openSignalingTestDB(t)
	tr := calls.New(d)
	hub := signaling.NewHub()
	r := signaling.NewRelay(hub, tr, alwaysAllow{}, nil)

	aConn := &signaling.Conn{Send: make(chan []byte, 50)}
	bConn := &signaling.Conn{Send: make(chan []byte, 50)}
	cConn := &signaling.Conn{Send: make(chan []byte, 50)}
	hub.Register("5550001", aConn)
	hub.Register("5550002", bConn)
	hub.Register("5550003", cConn)

	// Prime both active 2-party calls: A-B (held) and A-C (active, adding).
	if _, err := tr.OnCallInitiated("5550001", "5550002"); err != nil {
		t.Fatalf("OnCallInitiated A->B: %v", err)
	}
	if err := tr.OnCallAnswered("5550001", "5550002"); err != nil {
		t.Fatalf("OnCallAnswered A->B: %v", err)
	}
	if _, err := tr.OnCallInitiated("5550001", "5550003"); err != nil {
		t.Fatalf("OnCallInitiated A->C: %v", err)
	}
	if err := tr.OnCallAnswered("5550001", "5550003"); err != nil {
		t.Fatalf("OnCallAnswered A->C: %v", err)
	}

	// Verify both calls are tracked before hangup.
	if !tr.InCall("5550001", "5550002") {
		t.Fatal("expected A-B to be an active call before hangup")
	}
	if !tr.InCall("5550001", "5550003") {
		t.Fatal("expected A-C to be an active call before hangup")
	}

	// A sends Hangup (hook-on gesture). The To field names C (the active add
	// peer) as the client would set it; the server must also end A-B.
	r.HandleMessage("5550001", &signaling.Message{
		Type: signaling.TypeHangup,
		To:   "5550003",
	})

	// Both calls must be gone from the in-memory tracker.
	if tr.InCall("5550001", "5550002") {
		t.Error("A-B call still active in tracker after hangup")
	}
	if tr.InCall("5550001", "5550003") {
		t.Error("A-C call still active in tracker after hangup")
	}

	// Both B and C must receive TypeHangup.
	bMsgs := drainConn(t, bConn)
	cMsgs := drainConn(t, cConn)
	if countType(bMsgs, signaling.TypeHangup) != 1 {
		t.Fatalf("B: expected 1 TypeHangup, got %d", countType(bMsgs, signaling.TypeHangup))
	}
	if countType(cMsgs, signaling.TypeHangup) != 1 {
		t.Fatalf("C: expected 1 TypeHangup, got %d", countType(cMsgs, signaling.TypeHangup))
	}

	// Both call rows must be ended in the DB.
	var openCalls int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE (caller = '5550001' OR callee = '5550001')
		 AND status IN ('initiated', 'ringing', 'connected')`,
	).Scan(&openCalls); err != nil {
		t.Fatalf("count open calls: %v", err)
	}
	if openCalls != 0 {
		t.Fatalf("expected 0 open calls in DB after hangup, got %d", openCalls)
	}
}

// TestDisconnectNonHostConferenceParticipant_Integration verifies that when a
// non-host conference member disconnects, the entire conference is ended (not
// just a member drop), remaining members receive TypeConferenceEnd, and no
// continuation calls row is created (unlike the hangup/dropMember path).
func TestDisconnectNonHostConferenceParticipant_Integration(t *testing.T) {
	d := openSignalingTestDB(t)
	tr := calls.New(d)
	hub := signaling.NewHub()
	r := signaling.NewRelay(hub, tr, alwaysAllow{}, nil)

	aConn := &signaling.Conn{Send: make(chan []byte, 50)}
	bConn := &signaling.Conn{Send: make(chan []byte, 50)}
	cConn := &signaling.Conn{Send: make(chan []byte, 50)}
	hub.Register("5550001", aConn)
	hub.Register("5550002", bConn)
	hub.Register("5550003", cConn)

	// Set up an active conference: A hosts, B and C are members.
	if _, err := tr.OnCallInitiated("5550001", "5550002"); err != nil {
		t.Fatalf("OnCallInitiated A->B: %v", err)
	}
	if err := tr.OnCallAnswered("5550001", "5550002"); err != nil {
		t.Fatalf("OnCallAnswered A->B: %v", err)
	}
	callID := tr.CallIDFor("5550001", "5550002")
	if _, err := tr.OnCallInitiated("5550001", "5550003"); err != nil {
		t.Fatalf("OnCallInitiated A->C: %v", err)
	}
	if err := tr.OnCallAnswered("5550001", "5550003"); err != nil {
		t.Fatalf("OnCallAnswered A->C: %v", err)
	}
	if _, err := tr.CreateConferencePersistent("5550001", callID, []string{"5550002", "5550003"}); err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	// Simulate B (non-host) disconnecting by calling OnDisconnect directly.
	r.OnDisconnect("5550002")

	// Conference DB row must be ended with reason 'disconnect'.
	var dbState, dbReason string
	if err := d.DB.QueryRow(
		`SELECT state, COALESCE(end_reason, '') FROM conferences LIMIT 1`,
	).Scan(&dbState, &dbReason); err != nil {
		t.Fatalf("select conference: %v", err)
	}
	if dbState != "ended" {
		t.Errorf("expected conference state=ended, got %q", dbState)
	}
	if dbReason != "disconnect" {
		t.Errorf("expected end_reason=disconnect, got %q", dbReason)
	}

	// A and C must receive TypeConferenceEnd (not TypeConferenceLeave).
	// OnDisconnect calls endConference (not dropMemberFromConference), so all
	// members get ConferenceEnd with reason 'disconnect'.
	aMsgs := drainConn(t, aConn)
	cMsgs := drainConn(t, cConn)
	if countType(aMsgs, signaling.TypeConferenceEnd) != 1 {
		t.Fatalf("A: expected 1 ConferenceEnd, got %d (msgs: %v)", countType(aMsgs, signaling.TypeConferenceEnd), aMsgs)
	}
	if countType(cMsgs, signaling.TypeConferenceEnd) != 1 {
		t.Fatalf("C: expected 1 ConferenceEnd, got %d (msgs: %v)", countType(cMsgs, signaling.TypeConferenceEnd), cMsgs)
	}
	// Disconnect path ends the conference for all — no ConferenceLeave expected.
	if countType(aMsgs, signaling.TypeConferenceLeave) != 0 {
		t.Errorf("A: expected 0 ConferenceLeave on disconnect (endConference path), got %d", countType(aMsgs, signaling.TypeConferenceLeave))
	}

	// No continuation call rows: endConference does NOT create continuations.
	var continuationCount int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE originating_conference_id IS NOT NULL AND status = 'connected'`,
	).Scan(&continuationCount); err != nil {
		t.Fatalf("count continuation calls: %v", err)
	}
	if continuationCount != 0 {
		t.Errorf("expected 0 continuation calls on disconnect path, got %d", continuationCount)
	}

	// IsBusy must return false for all three after cleanup.
	if tr.Busy("5550001") {
		t.Error("A should not be busy after non-host conference disconnect")
	}
	if tr.Busy("5550002") {
		t.Error("B should not be busy after conference disconnect")
	}
	if tr.Busy("5550003") {
		t.Error("C should not be busy after non-host conference disconnect")
	}
}

// TestDisconnectConferenceParticipant_Integration verifies that when a
// conference member disconnects, the conference is ended in the DB, remaining
// members receive ConferenceEnd, and IsBusy returns false for them.
func TestDisconnectConferenceParticipant_Integration(t *testing.T) {
	d := openSignalingTestDB(t)
	tr := calls.New(d)
	hub := signaling.NewHub()
	r := signaling.NewRelay(hub, tr, alwaysAllow{}, nil)

	aConn := &signaling.Conn{Send: make(chan []byte, 50)}
	bConn := &signaling.Conn{Send: make(chan []byte, 50)}
	cConn := &signaling.Conn{Send: make(chan []byte, 50)}
	hub.Register("5550001", aConn)
	hub.Register("5550002", bConn)
	hub.Register("5550003", cConn)

	// Set up an active conference: A hosts, B and C are members.
	if _, err := tr.OnCallInitiated("5550001", "5550002"); err != nil {
		t.Fatalf("OnCallInitiated A->B: %v", err)
	}
	if err := tr.OnCallAnswered("5550001", "5550002"); err != nil {
		t.Fatalf("OnCallAnswered A->B: %v", err)
	}
	callID := tr.CallIDFor("5550001", "5550002")
	if _, err := tr.OnCallInitiated("5550001", "5550003"); err != nil {
		t.Fatalf("OnCallInitiated A->C: %v", err)
	}
	if err := tr.OnCallAnswered("5550001", "5550003"); err != nil {
		t.Fatalf("OnCallAnswered A->C: %v", err)
	}
	if _, err := tr.CreateConferencePersistent("5550001", callID, []string{"5550002", "5550003"}); err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	// Simulate A disconnecting by calling OnDisconnect directly.
	r.OnDisconnect("5550001")

	// Conference DB row must be ended with reason 'disconnect'.
	var dbState, dbReason string
	if err := d.DB.QueryRow(
		`SELECT state, COALESCE(end_reason, '') FROM conferences LIMIT 1`,
	).Scan(&dbState, &dbReason); err != nil {
		t.Fatalf("select conference: %v", err)
	}
	if dbState != "ended" {
		t.Errorf("expected conference state=ended, got %q", dbState)
	}
	if dbReason != "disconnect" {
		t.Errorf("expected end_reason=disconnect, got %q", dbReason)
	}

	// B and C must receive TypeConferenceEnd.
	bMsgs := drainConn(t, bConn)
	cMsgs := drainConn(t, cConn)
	if countType(bMsgs, signaling.TypeConferenceEnd) != 1 {
		t.Fatalf("B: expected 1 ConferenceEnd, got %d", countType(bMsgs, signaling.TypeConferenceEnd))
	}
	if countType(cMsgs, signaling.TypeConferenceEnd) != 1 {
		t.Fatalf("C: expected 1 ConferenceEnd, got %d", countType(cMsgs, signaling.TypeConferenceEnd))
	}

	// IsBusy must return false for B and C after cleanup.
	if tr.Busy("5550002") {
		t.Error("B should not be busy after conference disconnect")
	}
	if tr.Busy("5550003") {
		t.Error("C should not be busy after conference disconnect")
	}
}
