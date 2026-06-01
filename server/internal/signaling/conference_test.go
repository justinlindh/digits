package signaling

import (
	"context"
	"testing"
)

// drainMessages reads all buffered messages from a set of Conn channels,
// returning slices tagged with the recipient phone number.
type sentMessage struct {
	to  string
	msg *Message
}

func drainAll(phones map[string]*Conn) []sentMessage {
	var out []sentMessage
	for phone, conn := range phones {
		for {
			select {
			case data := <-conn.Send:
				msg, err := ParseMessage(data)
				if err == nil {
					out = append(out, sentMessage{to: phone, msg: msg})
				}
			default:
				// no more messages for this conn
				goto nextPhone
			}
		}
	nextPhone:
	}
	return out
}

func TestHandleConferenceMerge_Success(t *testing.T) {
	hub := NewHub()
	tr := newMockTracker()
	r := NewRelay(hub, tr, &mockCallAuthorizer{}, nil)

	// Register connections for all three phones.
	connA := &Conn{Send: make(chan []byte, 20)}
	connB := &Conn{Send: make(chan []byte, 20)}
	connC := &Conn{Send: make(chan []byte, 20)}
	_ = hub.Register("5550001", connA)
	_ = hub.Register("5550002", connB)
	_ = hub.Register("5550003", connC)

	// Pre-condition: A has active calls to both B and C.
	tr.onCallInitiated("5550001", "5550002")
	tr.onCallInitiated("5550001", "5550003")
	tr.setCallID("5550001", "5550002", 42)

	msg := &Message{
		Type:       TypeConferenceMerge,
		HeldPeer:   "5550002",
		ActivePeer: "5550003",
	}
	r.HandleMessage(context.Background(), "5550001", msg)

	phones := map[string]*Conn{
		"5550001": connA,
		"5550002": connB,
		"5550003": connC,
	}
	got := drainAll(phones)

	hasMember := 0
	hasConnect := 0
	for _, m := range got {
		if m.msg.Type == TypeConferenceMember {
			hasMember++
		}
		if m.msg.Type == TypeConferenceConnect {
			hasConnect++
		}
	}
	if hasMember != 3 {
		t.Fatalf("expected 3 ConferenceMember messages, got %d", hasMember)
	}
	if hasConnect != 2 {
		t.Fatalf("expected 2 ConferenceConnect messages (B, C), got %d", hasConnect)
	}

	// Exactly one of B and C should have initiator=true (the lower phone number).
	var bInit, cInit bool
	for _, m := range got {
		if m.msg.Type == TypeConferenceConnect {
			if m.to == "5550002" {
				bInit = m.msg.Initiator
			}
			if m.to == "5550003" {
				cInit = m.msg.Initiator
			}
		}
	}
	if !bInit || cInit {
		t.Fatalf("expected B to be initiator (smaller phone number), B=%v C=%v", bInit, cInit)
	}
}

func TestHandleConferenceMerge_RejectsMissingCalls(t *testing.T) {
	hub := NewHub()
	tr := newMockTracker()
	r := NewRelay(hub, tr, &mockCallAuthorizer{}, nil)

	connA := &Conn{Send: make(chan []byte, 20)}
	connB := &Conn{Send: make(chan []byte, 20)}
	_ = hub.Register("5550001", connA)
	_ = hub.Register("5550002", connB)

	// A has only A-B, not A-C.
	tr.onCallInitiated("5550001", "5550002")

	msg := &Message{
		Type:       TypeConferenceMerge,
		HeldPeer:   "5550002",
		ActivePeer: "5550003",
	}
	r.HandleMessage(context.Background(), "5550001", msg)

	phones := map[string]*Conn{"5550001": connA}
	got := drainAll(phones)

	rejected := false
	for _, m := range got {
		if m.to == "5550001" && m.msg.Type == TypeConferenceRejected {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("expected ConferenceRejected sent to host")
	}
}

func TestHandleSDP_AllowsConferencePeer(t *testing.T) {
	tr := newMockTracker()
	hub := NewHub()

	bConn := &Conn{Send: make(chan []byte, 20)}
	cConn := &Conn{Send: make(chan []byte, 20)}
	_ = hub.Register("5550002", bConn)
	_ = hub.Register("5550003", cConn)

	r := &Relay{Tracker: tr, Hub: hub, CallAuthorizer: &mockCallAuthorizer{denyAll: true}}

	// Pre-condition: B and C are in the same conference. CanCall denies them directly.
	tr.onCallInitiated("5550001", "5550002")
	tr.onCallInitiated("5550001", "5550003")
	tr.setCallID("5550001", "5550002", 42)
	conf, err := tr.Conferences().CreateConference(context.Background(), "5550001", 42, []string{"5550002", "5550003"})
	if err != nil {
		t.Fatalf("CreateConference: %v", err)
	}

	msg := &Message{Type: TypeSDP, To: "5550003", ConfID: conf.ID.String(), SDP: "v=0..."}
	r.HandleMessage(context.Background(), "5550002", msg)

	// C should receive the SDP despite denyAll authorizer.
	received := drainAll(map[string]*Conn{"5550003": cConn})
	found := false
	for _, m := range received {
		if m.to == "5550003" && m.msg.Type == TypeSDP && m.msg.SDP == "v=0..." {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected SDP relayed from B to C via conf_id")
	}
}

func TestHandleHangup_InConferenceEndsViaLeave(t *testing.T) {
	tr := newMockTracker()
	hub := NewHub()

	aConn := &Conn{Send: make(chan []byte, 20)}
	bConn := &Conn{Send: make(chan []byte, 20)}
	cConn := &Conn{Send: make(chan []byte, 20)}
	_ = hub.Register("5550001", aConn)
	_ = hub.Register("5550002", bConn)
	_ = hub.Register("5550003", cConn)

	r := &Relay{Tracker: tr, Hub: hub, CallAuthorizer: &mockCallAuthorizer{}}

	tr.onCallInitiated("5550001", "5550002")
	tr.onCallInitiated("5550001", "5550003")
	tr.setCallID("5550001", "5550002", 42)
	if _, err := tr.CreateConferencePersistent(context.Background(), "5550001", 42, []string{"5550002", "5550003"}); err != nil {
		t.Fatalf("preload conference: %v", err)
	}

	// B hangs up.
	r.HandleMessage(context.Background(), "5550002", &Message{Type: TypeHangup, To: "5550001"})

	received := drainAll(map[string]*Conn{"5550001": aConn, "5550003": cConn})
	leaves := 0
	ends := 0
	for _, m := range received {
		if m.msg.Type == TypeConferenceLeave {
			leaves++
		}
		if m.msg.Type == TypeConferenceEnd {
			ends++
		}
	}
	if leaves != 2 {
		t.Fatalf("expected 2 ConferenceLeave (to A and C), got %d", leaves)
	}
	if ends != 2 {
		t.Fatalf("expected 2 ConferenceEnd (v1: any drop ends conference), got %d", ends)
	}
}

func TestHandleHangup_HostEndsConference(t *testing.T) {
	tr := newMockTracker()
	hub := NewHub()

	aConn := &Conn{Send: make(chan []byte, 20)}
	bConn := &Conn{Send: make(chan []byte, 20)}
	cConn := &Conn{Send: make(chan []byte, 20)}
	_ = hub.Register("5550001", aConn)
	_ = hub.Register("5550002", bConn)
	_ = hub.Register("5550003", cConn)

	r := &Relay{Tracker: tr, Hub: hub, CallAuthorizer: &mockCallAuthorizer{}}

	tr.onCallInitiated("5550001", "5550002")
	tr.onCallInitiated("5550001", "5550003")
	tr.setCallID("5550001", "5550002", 42)
	if _, err := tr.CreateConferencePersistent(context.Background(), "5550001", 42, []string{"5550002", "5550003"}); err != nil {
		t.Fatalf("preload conference: %v", err)
	}

	// Host hangs up.
	r.HandleMessage(context.Background(), "5550001", &Message{Type: TypeHangup, To: "5550002"})

	received := drainAll(map[string]*Conn{"5550002": bConn, "5550003": cConn})
	ends := 0
	for _, m := range received {
		if m.msg.Type == TypeConferenceEnd {
			ends++
		}
	}
	if ends < 2 {
		t.Fatalf("expected at least 2 ConferenceEnd (to B and C), got %d", ends)
	}
}

func TestHandleConferenceMerge_RejectsIfMemberAlreadyInConference(t *testing.T) {
	hub := NewHub()
	tr := newMockTracker()
	r := NewRelay(hub, tr, &mockCallAuthorizer{}, nil)

	connA1 := &Conn{Send: make(chan []byte, 20)}
	connA2 := &Conn{Send: make(chan []byte, 20)}
	connB := &Conn{Send: make(chan []byte, 20)}
	connC := &Conn{Send: make(chan []byte, 20)}
	connD := &Conn{Send: make(chan []byte, 20)}
	_ = hub.Register("5550010", connA1)
	_ = hub.Register("5550001", connA2)
	_ = hub.Register("5550002", connB)
	_ = hub.Register("5550003", connC)
	_ = hub.Register("5550004", connD)

	// A1 has merged with B and C previously (conference exists).
	tr.onCallInitiated("5550010", "5550002")
	tr.onCallInitiated("5550010", "5550003")
	tr.setCallID("5550010", "5550002", 100)
	_, err := tr.CreateConferencePersistent(context.Background(), "5550010", 100, []string{"5550002", "5550003"})
	if err != nil {
		t.Fatalf("preload conference: %v", err)
	}

	// Now a different host A2 tries to merge 5550002 into a new conference.
	tr.onCallInitiated("5550001", "5550002")
	tr.onCallInitiated("5550001", "5550004")
	tr.setCallID("5550001", "5550002", 200)

	msg := &Message{
		Type:       TypeConferenceMerge,
		HeldPeer:   "5550002", // already in another conference
		ActivePeer: "5550004",
	}
	r.HandleMessage(context.Background(), "5550001", msg)

	phones := map[string]*Conn{"5550001": connA2}
	got := drainAll(phones)

	rejected := false
	for _, m := range got {
		if m.to == "5550001" && m.msg.Type == TypeConferenceRejected {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("expected ConferenceRejected because 5550002 is already in a conference")
	}
}
