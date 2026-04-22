package signal

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestParseMessage_Register(t *testing.T) {
	data := []byte(`{"type":"register","number":"3140001"}`)
	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != TypeRegister {
		t.Errorf("expected type %q, got %q", TypeRegister, msg.Type)
	}
	if msg.Number != "3140001" {
		t.Errorf("expected number %q, got %q", "3140001", msg.Number)
	}
}

func TestParseMessage_ICEServers(t *testing.T) {
	data := []byte(`{
		"type": "ice-servers",
		"servers": [
			{"urls": ["stun:stun.example.com:3478"]},
			{"urls": ["turn:turn.example.com:3478"], "username": "user1", "credential": "pass1"}
		]
	}`)
	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != TypeICEServers {
		t.Errorf("expected type %q, got %q", TypeICEServers, msg.Type)
	}
	if len(msg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(msg.Servers))
	}
	// STUN server
	stun := msg.Servers[0]
	if len(stun.URLs) != 1 || stun.URLs[0] != "stun:stun.example.com:3478" {
		t.Errorf("unexpected STUN URLs: %v", stun.URLs)
	}
	if stun.Username != "" || stun.Credential != "" {
		t.Errorf("expected empty username/credential for STUN, got %q/%q", stun.Username, stun.Credential)
	}
	// TURN server
	turn := msg.Servers[1]
	if len(turn.URLs) != 1 || turn.URLs[0] != "turn:turn.example.com:3478" {
		t.Errorf("unexpected TURN URLs: %v", turn.URLs)
	}
	if turn.Username != "user1" {
		t.Errorf("expected username %q, got %q", "user1", turn.Username)
	}
	if turn.Credential != "pass1" {
		t.Errorf("expected credential %q, got %q", "pass1", turn.Credential)
	}
}

func TestMarshalRoundtrip(t *testing.T) {
	orig := &Message{
		Type: TypeSDP,
		From: "3140001",
		To:   "3140002",
		SDP:  "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\n",
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	parsed, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if parsed.Type != TypeSDP {
		t.Errorf("expected type %q, got %q", TypeSDP, parsed.Type)
	}
	if parsed.From != orig.From {
		t.Errorf("expected from %q, got %q", orig.From, parsed.From)
	}
	if parsed.To != orig.To {
		t.Errorf("expected to %q, got %q", orig.To, parsed.To)
	}
	if parsed.SDP != orig.SDP {
		t.Errorf("expected SDP %q, got %q", orig.SDP, parsed.SDP)
	}
}

func TestParseMessage_Call(t *testing.T) {
	data := []byte(`{"type":"call","to":"3140002"}`)
	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != TypeCall {
		t.Errorf("expected type %q, got %q", TypeCall, msg.Type)
	}
	if msg.To != "3140002" {
		t.Errorf("expected to %q, got %q", "3140002", msg.To)
	}
}

func TestParseMessage_Invalid(t *testing.T) {
	data := []byte(`{not valid json}`)
	_, err := ParseMessage(data)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestParseContactsMessage(t *testing.T) {
	raw := `{"type":"contacts","contacts":[{"number":"5551234","name":"Emma"},{"number":"5559876","name":"Liam"}]}`
	msg, err := ParseMessage([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Type != TypeContacts {
		t.Errorf("expected type %q, got %q", TypeContacts, msg.Type)
	}
	if len(msg.Contacts) != 2 {
		t.Fatalf("expected 2 contacts, got %d", len(msg.Contacts))
	}
	if msg.Contacts[0].Number != "5551234" || msg.Contacts[0].Name != "Emma" {
		t.Errorf("unexpected contact[0]: %+v", msg.Contacts[0])
	}
	if msg.Contacts[1].Number != "5559876" || msg.Contacts[1].Name != "Liam" {
		t.Errorf("unexpected contact[1]: %+v", msg.Contacts[1])
	}
}

func TestParseContactsUpdatedNudge(t *testing.T) {
	raw := `{"type":"contacts_updated"}`
	msg, err := ParseMessage([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Type != TypeContactsUpdated {
		t.Errorf("expected type %q, got %q", TypeContactsUpdated, msg.Type)
	}
}

func TestParseICERestartMessage(t *testing.T) {
	raw := `{"type":"ice_restart","from":"3140001","to":"3140002","sdp":"v=0\r\n"}`
	msg, err := ParseMessage([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Type != TypeICERestart {
		t.Errorf("expected type %q, got %q", TypeICERestart, msg.Type)
	}
	if msg.From != "3140001" {
		t.Errorf("expected from %q, got %q", "3140001", msg.From)
	}
	if msg.SDP != "v=0\r\n" {
		t.Errorf("expected SDP %q, got %q", "v=0\r\n", msg.SDP)
	}
}

func TestParseSyncRequest(t *testing.T) {
	msg := &Message{Type: TypeSync}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Type != TypeSync {
		t.Errorf("expected type %q, got %q", TypeSync, parsed.Type)
	}
}

func TestConferenceMessageFieldsRoundTrip(t *testing.T) {
	in := Message{
		Type:   TypeConferenceMember,
		ConfID: "abc-123",
		Members: []ConferenceMemberInfo{
			{Phone: "5550001", Role: "host"},
			{Phone: "5550002", Role: "added"},
		},
	}
	b, err := json.Marshal(&in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Type != in.Type || out.ConfID != in.ConfID || len(out.Members) != 2 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	if out.Members[0].Phone != "5550001" || out.Members[0].Role != "host" {
		t.Fatalf("member 0 mismatch: %+v", out.Members[0])
	}
}

func TestLinkHealthRoundTrip(t *testing.T) {
	loss := float32(1.2)
	jitter := float32(15.4)
	rtt := float32(72.0)
	in := int64(1000)
	out := int64(950)
	m := &Message{
		Type: TypeLinkHealth,
		LinkHealth: &LinkHealthPayload{
			TS:       1714000000000,
			LossPct:  &loss,
			JitterMs: &jitter,
			RttMs:    &rtt,
			ConnType: "srflx",
			BytesIn:  &in,
			BytesOut: &out,
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Type != TypeLinkHealth {
		t.Fatalf("type: got %q want %q", got.Type, TypeLinkHealth)
	}
	if got.LinkHealth == nil {
		t.Fatal("LinkHealth nil")
	}
	if got.LinkHealth.ConnType != "srflx" {
		t.Fatalf("ConnType: got %q", got.LinkHealth.ConnType)
	}
	if got.LinkHealth.LossPct == nil || *got.LinkHealth.LossPct != 1.2 {
		t.Fatalf("LossPct mismatch")
	}
}

func TestLinkHealthOmitEmpty(t *testing.T) {
	m := &Message{Type: TypeLinkHealth, LinkHealth: &LinkHealthPayload{TS: 1, ConnType: "host"}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(data, []byte(`"loss_pct"`)) {
		t.Fatalf("expected loss_pct to be omitted, got %s", data)
	}
}

func TestLinkHealthParseOmittedFieldsAreNil(t *testing.T) {
	data := []byte(`{"type":"link_health","link_health":{"ts":1714000000000,"conn_type":"host"}}`)
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.LinkHealth == nil {
		t.Fatal("LinkHealth nil")
	}
	if m.LinkHealth.LossPct != nil {
		t.Fatal("LossPct should be nil when omitted")
	}
	if m.LinkHealth.JitterMs != nil || m.LinkHealth.RttMs != nil ||
		m.LinkHealth.BytesIn != nil || m.LinkHealth.BytesOut != nil {
		t.Fatal("all optional pointer fields should be nil when omitted")
	}
	if m.LinkHealth.TS != 1714000000000 {
		t.Fatalf("TS: got %v", m.LinkHealth.TS)
	}
	if m.LinkHealth.ConnType != "host" {
		t.Fatalf("ConnType: got %q", m.LinkHealth.ConnType)
	}
}
