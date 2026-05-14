package owebrtc

import (
	"testing"

	pionwebrtc "github.com/pion/webrtc/v4"
)

func BenchmarkReporterSample(b *testing.B) {
	report := buildFakeReport(b, fakeInput{
		JitterSec:     0.0154,
		RttSec:        0.072,
		LocalCandType: pionwebrtc.ICECandidateTypeSrflx,
		BytesSent:     5000,
		BytesReceived: 4800,
	})
	r := NewReporter(&fakeStatsGetter{report: report}, nil, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.sample()
	}
}

// TestReporterSampleCostBound fails when sample() takes longer than a
// budget that is already far larger than the expected ARM cost. This guards
// against regressions that would silently inflate per-call CPU on Pi Zero
// 2 W hardware (where telemetry claims "invisible" overhead -- see the spec
// §7 measurement plan).
func TestReporterSampleCostBound(t *testing.T) {
	// Budget: 500 μs per sample. At the default 2s cadence this is
	// 0.025% of a core -- well above the expected cost of reading a
	// handful of Pion counters. A regression above this bound means
	// something changed structurally; this test flags it early.
	const budgetNs = 500_000
	res := testing.Benchmark(BenchmarkReporterSample)
	if res.NsPerOp() > budgetNs {
		t.Fatalf("Reporter.sample() too slow: %d ns/op > %d ns/op budget", res.NsPerOp(), budgetNs)
	}
}
