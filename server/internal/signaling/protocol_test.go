package signaling

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageConferenceFieldsRoundTrip(t *testing.T) {
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
	if out.Members[1].Phone != "5550002" || out.Members[1].Role != "added" {
		t.Fatalf("member 1 mismatch: %+v", out.Members[1])
	}
}

func TestMessageConferenceFieldsOmitempty(t *testing.T) {
	// A Message with no conference fields should not include any conf_* keys in its JSON output.
	in := Message{Type: TypeCall, From: "5550001", To: "5550002"}
	b, err := json.Marshal(&in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, substr := range []string{"conf_id", "held_peer", "active_peer", "members", "peer\":", "initiator"} {
		if contains(s, substr) {
			t.Fatalf("expected %q absent from JSON but got: %s", substr, s)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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
	got, err := ParseMessage(data)
	if err != nil {
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
	// Wire representation when phone has TS and ConnType but no RTCP stats yet.
	data := []byte(`{"type":"link_health","link_health":{"ts":1714000000000,"conn_type":"host"}}`)
	m, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.LinkHealth == nil {
		t.Fatal("LinkHealth nil")
	}
	if m.LinkHealth.LossPct != nil {
		t.Fatalf("LossPct: got %v, want nil", *m.LinkHealth.LossPct)
	}
	if m.LinkHealth.JitterMs != nil {
		t.Fatalf("JitterMs: got %v, want nil", *m.LinkHealth.JitterMs)
	}
	if m.LinkHealth.RttMs != nil {
		t.Fatalf("RttMs: got %v, want nil", *m.LinkHealth.RttMs)
	}
	if m.LinkHealth.BytesIn != nil {
		t.Fatalf("BytesIn: got %v, want nil", *m.LinkHealth.BytesIn)
	}
	if m.LinkHealth.BytesOut != nil {
		t.Fatalf("BytesOut: got %v, want nil", *m.LinkHealth.BytesOut)
	}
	if m.LinkHealth.TS != 1714000000000 {
		t.Fatalf("TS: got %v, want 1714000000000", m.LinkHealth.TS)
	}
	if m.LinkHealth.ConnType != "host" {
		t.Fatalf("ConnType: got %q", m.LinkHealth.ConnType)
	}
}

func TestLinkHealthPeerRoundTrip(t *testing.T) {
	loss := float32(1.5)
	m := &Message{
		Type: TypeLinkHealth,
		LinkHealth: &LinkHealthPayload{
			TS:       1714000000000,
			LossPct:  &loss,
			ConnType: "relay",
			Peer:     "+15555550123",
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"peer":"+15555550123"`) {
		t.Fatalf("expected peer field in JSON: %s", b)
	}
	var got Message
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.LinkHealth == nil {
		t.Fatal("LinkHealth nil after round trip")
	}
	if got.LinkHealth.Peer != "+15555550123" {
		t.Fatalf("Peer: got %q want %q", got.LinkHealth.Peer, "+15555550123")
	}
}

func TestLinkHealthPeerOmitEmpty(t *testing.T) {
	m := &Message{Type: TypeLinkHealth, LinkHealth: &LinkHealthPayload{TS: 1, ConnType: "host"}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var env struct {
		LinkHealth map[string]json.RawMessage `json:"link_health"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if _, ok := env.LinkHealth["peer"]; ok {
		t.Fatalf("empty Peer should be omitted from link_health object: %s", b)
	}
}
