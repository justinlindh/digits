package signaling

import (
	"bytes"
	"encoding/json"
	"testing"
)

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
