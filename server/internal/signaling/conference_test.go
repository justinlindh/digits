package signaling

import (
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
	hub.Register("5550001", connA)
	hub.Register("5550002", connB)
	hub.Register("5550003", connC)

	// Pre-condition: A has active calls to both B and C.
	tr.onCallInitiated("5550001", "5550002")
	tr.onCallInitiated("5550001", "5550003")
	tr.setCallID("5550001", "5550002", 42)

	msg := &Message{
		Type:       TypeConferenceMerge,
		HeldPeer:   "5550002",
		ActivePeer: "5550003",
	}
	r.HandleMessage("5550001", msg)

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
	hub.Register("5550001", connA)
	hub.Register("5550002", connB)

	// A has only A-B, not A-C.
	tr.onCallInitiated("5550001", "5550002")

	msg := &Message{
		Type:       TypeConferenceMerge,
		HeldPeer:   "5550002",
		ActivePeer: "5550003",
	}
	r.HandleMessage("5550001", msg)

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

func TestHandleConferenceMerge_RejectsIfMemberAlreadyInConference(t *testing.T) {
	hub := NewHub()
	tr := newMockTracker()
	r := NewRelay(hub, tr, &mockCallAuthorizer{}, nil)

	connA1 := &Conn{Send: make(chan []byte, 20)}
	connA2 := &Conn{Send: make(chan []byte, 20)}
	connB := &Conn{Send: make(chan []byte, 20)}
	connC := &Conn{Send: make(chan []byte, 20)}
	connD := &Conn{Send: make(chan []byte, 20)}
	hub.Register("5550010", connA1)
	hub.Register("5550001", connA2)
	hub.Register("5550002", connB)
	hub.Register("5550003", connC)
	hub.Register("5550004", connD)

	// A1 has merged with B and C previously (conference exists).
	tr.onCallInitiated("5550010", "5550002")
	tr.onCallInitiated("5550010", "5550003")
	tr.setCallID("5550010", "5550002", 100)
	_, err := tr.CreateConferencePersistent("5550010", 100, []string{"5550002", "5550003"})
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
	r.HandleMessage("5550001", msg)

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
