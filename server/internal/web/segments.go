package web

// segDesc drives bar segment rendering. Lit is the count (0..segmentCount) of
// segments that should be rendered as lit; Severity ("" | "warn" | "bad")
// controls their color via CSS classes.
type segDesc struct {
	Lit      int
	Severity string
}

// Thresholds for the pctToSegments (packet-loss %) and msToSegments (jitter ms)
// template helpers. Each bar has segmentCount slots; the per-slot divisor
// determines how many slots light up for a given measurement value.
const (
	segmentCount     = 10
	pctPerSegment    = float32(10.0)
	pctBadThreshold  = float32(2.0)
	pctWarnThreshold = float32(0.5)
	msPerSegment     = float32(6.0)
	msBadThreshold   = float32(40.0)
	msWarnThreshold  = float32(20.0)
)

// toSegments converts a measured value to a segDesc for health-bar rendering.
// val is clamped to [0, segmentCount]; at least one segment lights up for any
// positive value.
func toSegments(val, perSegment, badThreshold, warnThreshold float32) segDesc {
	lit := int(val / perSegment)
	if val > 0 && lit == 0 {
		lit = 1
	}
	lit = min(lit, segmentCount)
	sev := ""
	switch {
	case val >= badThreshold:
		sev = "bad"
	case val >= warnThreshold:
		sev = "warn"
	}
	return segDesc{Lit: lit, Severity: sev}
}
