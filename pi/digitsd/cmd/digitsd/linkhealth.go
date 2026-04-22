package main

import (
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
)

// sigSender is the minimal send surface buildMeshLinkHealthSend needs.
// *sigclient.Client satisfies it.
type sigSender interface {
	Send(*sigclient.Message) error
}

// buildMeshLinkHealthSend returns an owebrtc.Reporter send callback that
// emits a link_health signaling message stamped with the given remote
// peer phone. Every other field is copied through from the sample.
func buildMeshLinkHealthSend(sig sigSender, peer string) func(owebrtc.Sample) error {
	return func(s owebrtc.Sample) error {
		payload := &sigclient.LinkHealthPayload{
			TS:       s.TS.UnixMilli(),
			LossPct:  s.LossPct,
			JitterMs: s.JitterMs,
			RttMs:    s.RttMs,
			ConnType: s.ConnType,
			BytesIn:  s.BytesIn,
			BytesOut: s.BytesOut,
			Peer:     peer,
		}
		return sig.Send(&sigclient.Message{Type: sigclient.TypeLinkHealth, LinkHealth: payload})
	}
}
