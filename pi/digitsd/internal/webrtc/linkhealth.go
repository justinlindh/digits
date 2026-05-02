package owebrtc

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	pionwebrtc "github.com/pion/webrtc/v4"
)

// Sample is the phone-local representation of one telemetry point. The
// server-side calls.Sample mirrors these fields; conversion to the
// signaling wire format happens in the send callback provided by the
// caller.
//
// ConnType reflects the ICE candidate type of the local end of the
// nominated pair: one of "host", "srflx", "prflx", "relay", or empty if
// no pair is nominated.
type Sample struct {
	TS       time.Time
	LossPct  *float32
	JitterMs *float32
	RttMs    *float32
	ConnType string
	BytesIn  *int64
	BytesOut *int64
}

// StatsGetter is the minimal Pion surface the Reporter depends on.
// *pionwebrtc.PeerConnection satisfies this.
type StatsGetter interface {
	GetStats() pionwebrtc.StatsReport
}

// Reporter samples WebRTC stats on a ticker and invokes send on each
// non-empty sample. Zero-valued samples (no InboundRTP data yet AND no ICE
// pair) are skipped entirely to avoid wire noise. Fields owned by Run()
// goroutine after start; sample() is not safe for concurrent use.
type Reporter struct {
	getter   StatsGetter
	send     func(Sample) error
	interval time.Duration

	prevPacketsReceived uint32
	prevPacketsLost     int32
	havePrev            bool
}

const defaultInterval = 2 * time.Second

// NewReporter constructs a Reporter. interval <= 0 selects defaultInterval.
func NewReporter(getter StatsGetter, send func(Sample) error, interval time.Duration) *Reporter {
	if interval <= 0 {
		interval = defaultInterval
	}
	return &Reporter{getter: getter, send: send, interval: interval}
}

// Run blocks until ctx is canceled. Each tick samples the peer connection
// and invokes the send callback. A top-level panic recovery ensures a
// misbehaving send callback or unexpected Pion state cannot take down the
// call -- we log and exit the goroutine instead.
func (r *Reporter) Run(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Warn("link-health reporter panic; exiting", "panic", rec, "stack", string(debug.Stack()))
		}
	}()
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sample, ok := r.sample()
			if !ok {
				continue
			}
			if r.send != nil {
				if err := r.send(sample); err != nil {
					slog.Debug("link-health send failed; will retry next tick", "err", err)
				}
			}
		}
	}
}

// sample walks a GetStats() report and extracts a Sample. Returns ok=false
// if the report has no useful data to report (no InboundRTP data yet AND no nominated
// ICE pair).
func (r *Reporter) sample() (Sample, bool) {
	report := r.getter.GetStats()
	out := Sample{TS: time.Now()}

	// InboundRTPStreamStats: pion's local view of the inbound stream. Includes
	// jitter directly (in seconds; convert to ms) and lifetime PacketsReceived /
	// PacketsLost counters that we delta against the previous sample to get a
	// per-window loss percentage.
	//
	// pion v4.2.11 does NOT emit RemoteInboundRTPStreamStats in this codec
	// configuration, so we cannot use the remote's RR. Confirmed empirically.
	for _, st := range report {
		if in, ok := st.(pionwebrtc.InboundRTPStreamStats); ok {
			jitter := float32(in.Jitter * 1000.0)
			out.JitterMs = &jitter
			if r.havePrev {
				recvDelta := int64(in.PacketsReceived) - int64(r.prevPacketsReceived)
				lostDelta := int64(in.PacketsLost) - int64(r.prevPacketsLost)
				total := recvDelta + lostDelta
				if total > 0 && lostDelta >= 0 && recvDelta >= 0 {
					loss := float32(lostDelta) * 100.0 / float32(total)
					out.LossPct = &loss
				}
			}
			r.prevPacketsReceived = in.PacketsReceived
			r.prevPacketsLost = in.PacketsLost
			r.havePrev = true
			break // typical voice call: one audio track
		}
	}

	// Nominated ICE candidate pair: the pair carrying media right now.
	var nominated *pionwebrtc.ICECandidatePairStats
	for _, st := range report {
		if p, ok := st.(pionwebrtc.ICECandidatePairStats); ok {
			if p.Nominated && p.State == pionwebrtc.StatsICECandidatePairStateSucceeded {
				cp := p
				nominated = &cp
				break
			}
		}
	}
	if nominated != nil {
		rtt := float32(nominated.CurrentRoundTripTime * 1000.0)
		out.RttMs = &rtt
		bin := int64(nominated.BytesReceived)
		bout := int64(nominated.BytesSent)
		out.BytesIn = &bin
		out.BytesOut = &bout
		if local, ok := report[nominated.LocalCandidateID]; ok {
			if lc, ok := local.(pionwebrtc.ICECandidateStats); ok {
				out.ConnType = lc.CandidateType.String()
			}
		}
	}

	// Drop if neither InboundRTP nor ICE pair produced anything useful.
	if out.LossPct == nil && out.JitterMs == nil && out.RttMs == nil &&
		out.BytesIn == nil && out.BytesOut == nil && out.ConnType == "" {
		return Sample{}, false
	}
	return out, true
}
