package main

import (
	"context"
	"log/slog"

	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
	"github.com/pion/webrtc/v4"
)

// sigSender is the minimal Send-only surface that helpers in this package
// (link-health reporter, voicemail-state publisher) need from the signaling
// client. *sigclient.Client satisfies it; unit tests inject capturing fakes
// to exercise these flows without standing up a real WebSocket.
type sigSender interface {
	Send(*sigclient.Message) error
}

// meshReporterOnConnected returns a PeerConnectionStateChange callback that
// spawns a link-health reporter for peerPhone on Connected and stores its
// cancel in d.meshReporterCancels. Idempotent on ICE restart (cancels any
// prior reporter for this peer before spawning a fresh one).
func (d *daemonCallbacks) meshReporterOnConnected(pm *owebrtc.PeerManager, peerPhone string) func(webrtc.PeerConnectionState) {
	return func(state webrtc.PeerConnectionState) {
		slog.Info("conference: mesh PC state", "phone", peerPhone, "state", state.String())
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
		reporter := owebrtc.NewReporter(pm, buildLinkHealthSend(d.sig, peerPhone), d.linkHealthInterval)
		go reporter.Run(rctx)
	}
}

// buildLinkHealthSend returns an owebrtc.Reporter send callback that emits a
// link_health signaling message from a sample. peer names the remote endpoint
// for a mesh (conference) edge; pass "" for a 2-party call, where the server
// reads the empty Peer as the single-peer case. Every other field is copied
// through from the sample.
func buildLinkHealthSend(sig sigSender, peer string) func(owebrtc.Sample) error {
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
