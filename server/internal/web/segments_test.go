package web

import "testing"

func TestToSegments(t *testing.T) {
	tests := []struct {
		name          string
		val           float32
		perSegment    float32
		badThreshold  float32
		warnThreshold float32
		wantLit       int
		wantSeverity  string
	}{
		{name: "zero is dark", val: 0, perSegment: pctPerSegment, badThreshold: pctBadThreshold, warnThreshold: pctWarnThreshold, wantLit: 0, wantSeverity: ""},
		{name: "tiny positive lights one segment", val: 0.3, perSegment: pctPerSegment, badThreshold: pctBadThreshold, warnThreshold: pctWarnThreshold, wantLit: 1, wantSeverity: ""},
		{name: "warn threshold boundary", val: pctWarnThreshold, perSegment: pctPerSegment, badThreshold: pctBadThreshold, warnThreshold: pctWarnThreshold, wantLit: 1, wantSeverity: "warn"},
		{name: "bad threshold boundary", val: pctBadThreshold, perSegment: pctPerSegment, badThreshold: pctBadThreshold, warnThreshold: pctWarnThreshold, wantLit: 1, wantSeverity: "bad"},
		{name: "mid range pct", val: 50, perSegment: pctPerSegment, badThreshold: pctBadThreshold, warnThreshold: pctWarnThreshold, wantLit: 5, wantSeverity: "bad"},
		{name: "over range clamps to segmentCount", val: 200, perSegment: pctPerSegment, badThreshold: pctBadThreshold, warnThreshold: pctWarnThreshold, wantLit: segmentCount, wantSeverity: "bad"},
		{name: "ms good", val: 6, perSegment: msPerSegment, badThreshold: msBadThreshold, warnThreshold: msWarnThreshold, wantLit: 1, wantSeverity: ""},
		{name: "ms warn boundary", val: msWarnThreshold, perSegment: msPerSegment, badThreshold: msBadThreshold, warnThreshold: msWarnThreshold, wantLit: 3, wantSeverity: "warn"},
		{name: "ms bad boundary", val: msBadThreshold, perSegment: msPerSegment, badThreshold: msBadThreshold, warnThreshold: msWarnThreshold, wantLit: 6, wantSeverity: "bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toSegments(tt.val, tt.perSegment, tt.badThreshold, tt.warnThreshold)
			if got.Lit != tt.wantLit {
				t.Errorf("Lit = %d, want %d", got.Lit, tt.wantLit)
			}
			if got.Severity != tt.wantSeverity {
				t.Errorf("Severity = %q, want %q", got.Severity, tt.wantSeverity)
			}
		})
	}
}
