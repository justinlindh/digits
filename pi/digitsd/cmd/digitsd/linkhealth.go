package main

import (
	"context"

	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
	"github.com/pion/webrtc/v4"
)

// sigSender is the minimal send surface buildMeshLinkHealthSend needs.
// *sigclient.Client satisfies it.
type sigSender interface {
	Send(*sigclient.Message) error
}

// meshReporterOnConnected returns a PeerConnectionStateChange callback that
// spawns a link-health reporter for peerPhone on Connected and stores its
// cancel in d.meshReporterCancels. Idempotent on ICE restart (cancels any
// prior reporter for this peer before spawning a fresh one).
func (d *daemonCallbacks) meshReporterOnConnected(pm *owebrtc.PeerManager, peerPhone string) func(webrtc.PeerConnectionState) {
	return func(state webrtc.PeerConnectionState) {
		if state != webrtc.PeerConnectionStateConnected {
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.linkHealthDisabled {
			return
		}
		if cancel, ok := d.meshReporterCancels[peerPhone]; ok {
			cancel()
		}
		rctx, cancel := context.WithCancel(context.Background())
		d.meshReporterCancels[peerPhone] = cancel
		reporter := owebrtc.NewReporter(pm, buildMeshLinkHealthSend(d.sig, peerPhone), d.linkHealthInterval)
		go reporter.Run(rctx)
	}
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
