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
	in := fakeInput{
		JitterSec:     0.0154,
		RttSec:        0.072,
		LocalCandType: pionwebrtc.ICECandidateTypeSrflx,
		BytesSent:     5000,
		BytesReceived: 4800,
	}
	r := NewReporter(&fakeStatsGetter{report: buildFakeReport(t, in)}, nil, 0)

	s, ok := r.sample()
	if !ok {
		t.Fatal("expected sample, got drop")
	}
	// First sample has no prior baseline, so LossPct stays nil even though
	// jitter is populated immediately from InboundRTPStreamStats.
	if s.LossPct != nil {
		t.Fatalf("loss_pct: got %v want nil on first sample", *s.LossPct)
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

func TestReporterSampleOnlyInboundRTP(t *testing.T) {
	// Only InboundRTP data (jitter), no ICE pair. Should still emit a sample
	// because JitterMs is populated.
	report := pionwebrtc.StatsReport{
		"inbound-0": pionwebrtc.InboundRTPStreamStats{
			Jitter: 0.010,
		},
	}
	r := NewReporter(&fakeStatsGetter{report: report}, nil, 0)
	s, ok := r.sample()
	if !ok {
		t.Fatal("expected sample from InboundRTP-only report")
	}
	if s.JitterMs == nil {
		t.Fatalf("JitterMs nil")
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
	JitterSec     float64
	RttSec        float64
	LocalCandType pionwebrtc.ICECandidateType
	BytesSent     uint64
	BytesReceived uint64
}

func buildFakeReport(tb testing.TB, in fakeInput) pionwebrtc.StatsReport {
	tb.Helper()
	report := pionwebrtc.StatsReport{}
	report["inbound-0"] = pionwebrtc.InboundRTPStreamStats{
		Jitter: in.JitterSec,
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

func TestSample_PopulatesJitterFromInboundRTP(t *testing.T) {
	g := &fakeStatsGetter{
		report: pionwebrtc.StatsReport{
			"id1": pionwebrtc.InboundRTPStreamStats{
				Jitter: 0.012, // 12 ms in seconds
			},
		},
	}
	r := NewReporter(g, nil, time.Second)
	s, ok := r.sample()
	if !ok {
		t.Fatal("expected sample, got drop")
	}
	if s.JitterMs == nil {
		t.Fatal("JitterMs nil")
	}
	if got := *s.JitterMs; got < 11.9 || got > 12.1 {
		t.Errorf("JitterMs=%v want ~12.0", got)
	}
}

func TestSample_PopulatesLossFromInboundRTPDelta(t *testing.T) {
	g := &fakeStatsGetter{}
	r := NewReporter(g, nil, time.Second)

	// First sample establishes baseline: 100 received, 0 lost.
	g.report = pionwebrtc.StatsReport{
		"id1": pionwebrtc.InboundRTPStreamStats{
			PacketsReceived: 100,
			PacketsLost:     0,
		},
	}
	if _, ok := r.sample(); !ok {
		t.Fatal("first sample dropped unexpectedly")
	}

	// Second sample: 200 received, 5 lost since baseline (5 / (100+5) ~= 4.76%).
	g.report = pionwebrtc.StatsReport{
		"id1": pionwebrtc.InboundRTPStreamStats{
			PacketsReceived: 200,
			PacketsLost:     5,
		},
	}
	s, ok := r.sample()
	if !ok {
		t.Fatal("second sample dropped")
	}
	if s.LossPct == nil {
		t.Fatal("LossPct nil on second sample")
	}
	if got := *s.LossPct; got < 4.5 || got > 5.0 {
		t.Errorf("LossPct=%v want ~4.76", got)
	}
}

func TestSample_LossNilOnCounterReset(t *testing.T) {
	g := &fakeStatsGetter{}
	r := NewReporter(g, nil, time.Second)

	// Baseline: 100 received, 5 lost.
	g.report = pionwebrtc.StatsReport{
		"id1": pionwebrtc.InboundRTPStreamStats{PacketsReceived: 100, PacketsLost: 5},
	}
	if _, ok := r.sample(); !ok {
		t.Fatal("baseline sample dropped")
	}

	// Counter reset: PacketsLost decreases (track replacement).
	g.report = pionwebrtc.StatsReport{
		"id1": pionwebrtc.InboundRTPStreamStats{PacketsReceived: 110, PacketsLost: 2},
	}
	s, ok := r.sample()
	if !ok {
		t.Fatal("post-reset sample dropped")
	}
	if s.LossPct != nil {
		t.Errorf("LossPct should be nil on counter reset, got %v", *s.LossPct)
	}
}

func TestSample_LossNilOnZeroTrafficWindow(t *testing.T) {
	g := &fakeStatsGetter{}
	r := NewReporter(g, nil, time.Second)

	// Two identical samples: no traffic moved.
	g.report = pionwebrtc.StatsReport{
		"id1": pionwebrtc.InboundRTPStreamStats{PacketsReceived: 50, PacketsLost: 0},
	}
	if _, ok := r.sample(); !ok {
		t.Fatal("baseline sample dropped")
	}
	s, ok := r.sample()
	if !ok {
		t.Fatal("zero-traffic sample dropped")
	}
	if s.LossPct != nil {
		t.Errorf("LossPct should be nil on zero-traffic window, got %v", *s.LossPct)
	}
}

func TestSample_LossOneHundredOnAllLossWindow(t *testing.T) {
	g := &fakeStatsGetter{}
	r := NewReporter(g, nil, time.Second)

	// Baseline.
	g.report = pionwebrtc.StatsReport{
		"id1": pionwebrtc.InboundRTPStreamStats{PacketsReceived: 100, PacketsLost: 0},
	}
	if _, ok := r.sample(); !ok {
		t.Fatal("baseline sample dropped")
	}

	// All-loss window: PacketsReceived flat, PacketsLost gained 10.
	g.report = pionwebrtc.StatsReport{
		"id1": pionwebrtc.InboundRTPStreamStats{PacketsReceived: 100, PacketsLost: 10},
	}
	s, ok := r.sample()
	if !ok {
		t.Fatal("all-loss sample dropped")
	}
	if s.LossPct == nil {
		t.Fatal("LossPct nil")
	}
	if got := *s.LossPct; got < 99.9 || got > 100.1 {
		t.Errorf("LossPct=%v want 100.0", got)
	}
}
