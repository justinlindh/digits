package main

import (
	"testing"
	"time"

	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
)

type recordingSig struct{ last *sigclient.Message }

func (r *recordingSig) Send(m *sigclient.Message) error {
	r.last = m
	return nil
}

func TestBuildMeshLinkHealthSend_StampsPeer(t *testing.T) {
	rs := &recordingSig{}
	send := buildMeshLinkHealthSend(rs, "+15555550123")

	loss := float32(1.25)
	s := owebrtc.Sample{
		TS:       time.UnixMilli(1714000000000),
		LossPct:  &loss,
		ConnType: "srflx",
	}
	if err := send(s); err != nil {
		t.Fatalf("send: %v", err)
	}
	if rs.last == nil {
		t.Fatal("no message sent")
	}
	if rs.last.Type != sigclient.TypeLinkHealth {
		t.Fatalf("Type: got %q", rs.last.Type)
	}
	if rs.last.LinkHealth == nil {
		t.Fatal("LinkHealth nil")
	}
	if rs.last.LinkHealth.Peer != "+15555550123" {
		t.Fatalf("Peer: got %q want %q", rs.last.LinkHealth.Peer, "+15555550123")
	}
	if rs.last.LinkHealth.ConnType != "srflx" {
		t.Fatalf("ConnType not preserved: got %q", rs.last.LinkHealth.ConnType)
	}
	if rs.last.LinkHealth.LossPct == nil || *rs.last.LinkHealth.LossPct != 1.25 {
		t.Fatalf("LossPct not preserved")
	}
}
