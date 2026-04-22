package owebrtc

import (
	"context"
	"testing"
	"time"

	pionwebrtc "github.com/pion/webrtc/v4"
)

// fakeStatsGetter satisfies the StatsGetter interface for tests.
type fakeStatsGetter struct {
	report pionwebrtc.StatsReport
}

func (f *fakeStatsGetter) GetStats() pionwebrtc.StatsReport { return f.report }

func TestReporterSampleNominalPath(t *testing.T) {
	report := buildFakeReport(t, fakeInput{
		FractionLost:  0.012, // 1.2%
		JitterSec:     0.0154,
		RttSec:        0.072,
		LocalCandType: pionwebrtc.ICECandidateTypeSrflx,
		BytesSent:     5000,
		BytesReceived: 4800,
	})
	r := NewReporter(&fakeStatsGetter{report: report}, nil, 0)

	s, ok := r.sample()
	if !ok {
		t.Fatal("expected sample, got drop")
	}
	if s.LossPct == nil || *s.LossPct < 1.15 || *s.LossPct > 1.25 {
		t.Fatalf("loss_pct: got %v want ≈1.2", s.LossPct)
	}
	if s.JitterMs == nil || *s.JitterMs < 15.0 || *s.JitterMs > 16.0 {
		t.Fatalf("jitter_ms: got %v want ≈15.4", s.JitterMs)
	}
	if s.RttMs == nil || *s.RttMs < 71.0 || *s.RttMs > 73.0 {
		t.Fatalf("rtt_ms: got %v want ≈72", s.RttMs)
	}
	if s.ConnType != "srflx" {
		t.Fatalf("conn_type: got %q", s.ConnType)
	}
	if s.BytesIn == nil || *s.BytesIn != 4800 {
		t.Fatalf("bytes_in: got %v", s.BytesIn)
	}
	if s.BytesOut == nil || *s.BytesOut != 5000 {
		t.Fatalf("bytes_out: got %v", s.BytesOut)
	}
}

func TestReporterSampleSkipsEmptyReport(t *testing.T) {
	r := NewReporter(&fakeStatsGetter{report: pionwebrtc.StatsReport{}}, nil, 0)
	if _, ok := r.sample(); ok {
		t.Fatal("expected drop on empty report")
	}
}

func TestReporterSampleOnlyRemoteInbound(t *testing.T) {
	// Only RR data (loss + jitter), no ICE pair. Should still emit a sample.
	report := pionwebrtc.StatsReport{
		"remote-inbound-0": pionwebrtc.RemoteInboundRTPStreamStats{
			FractionLost: 0.05,
			Jitter:       0.010,
		},
	}
	r := NewReporter(&fakeStatsGetter{report: report}, nil, 0)
	s, ok := r.sample()
	if !ok {
		t.Fatal("expected sample from RR-only report")
	}
	if s.LossPct == nil {
		t.Fatalf("LossPct nil")
	}
	if s.RttMs != nil {
		t.Fatalf("RttMs should be nil without ICE pair")
	}
	if s.ConnType != "" {
		t.Fatalf("ConnType should be empty without ICE pair")
	}
}

func TestReporterSampleOnlyICEPair(t *testing.T) {
	// Only ICE pair (rtt + bytes + conn_type), no RR. Should still emit.
	report := pionwebrtc.StatsReport{
		"candidate-local-0": pionwebrtc.ICECandidateStats{
			ID:            "candidate-local-0",
			CandidateType: pionwebrtc.ICECandidateTypeHost,
		},
		"pair-0": pionwebrtc.ICECandidatePairStats{
			Nominated:            true,
			State:                pionwebrtc.StatsICECandidatePairStateSucceeded,
			CurrentRoundTripTime: 0.050,
			BytesSent:            100,
			BytesReceived:        90,
			LocalCandidateID:     "candidate-local-0",
		},
	}
	r := NewReporter(&fakeStatsGetter{report: report}, nil, 0)
	s, ok := r.sample()
	if !ok {
		t.Fatal("expected sample from ICE-only report")
	}
	if s.LossPct != nil {
		t.Fatal("LossPct should be nil without RR")
	}
	if s.RttMs == nil {
		t.Fatal("RttMs nil despite ICE pair")
	}
	if s.ConnType != "host" {
		t.Fatalf("ConnType: got %q", s.ConnType)
	}
}

func TestReporterRunCancelsCleanly(t *testing.T) {
	calls := 0
	r := NewReporter(
		&fakeStatsGetter{report: pionwebrtc.StatsReport{}},
		func(Sample) error { calls++; return nil },
		10*time.Millisecond,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	r.Run(ctx) // must return within the context deadline
}

// --- Test helpers ---

type fakeInput struct {
	FractionLost  float64
	JitterSec     float64
	RttSec        float64
	LocalCandType pionwebrtc.ICECandidateType
	BytesSent     uint64
	BytesReceived uint64
}

func buildFakeReport(t *testing.T, in fakeInput) pionwebrtc.StatsReport {
	t.Helper()
	report := pionwebrtc.StatsReport{}
	report["remote-inbound-0"] = pionwebrtc.RemoteInboundRTPStreamStats{
		FractionLost: in.FractionLost,
		Jitter:       in.JitterSec,
	}
	report["candidate-local-0"] = pionwebrtc.ICECandidateStats{
		ID:            "candidate-local-0",
		CandidateType: in.LocalCandType,
	}
	report["pair-0"] = pionwebrtc.ICECandidatePairStats{
		Nominated:            true,
		State:                pionwebrtc.StatsICECandidatePairStateSucceeded,
		CurrentRoundTripTime: in.RttSec,
		BytesSent:            in.BytesSent,
		BytesReceived:        in.BytesReceived,
		LocalCandidateID:     "candidate-local-0",
	}
	return report
}
