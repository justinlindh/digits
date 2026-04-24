package signaling

import "context"

// LineStore is the subset of line.Store the signaling layer needs to fetch
// per-line settings. Kept as a small interface here so the dependency on
// internal/line is isolated to a single adapter file (linestore_adapter.go)
// rather than threaded through the whole signaling package.
type LineStore interface {
	// EffectiveLineSettings returns the line's settings with SilentMode
	// already combined with the line's household do-not-disturb flag using
	// OR. Callers receive the value the device should treat as authoritative
	// at ring time, with no further composition required.
	EffectiveLineSettings(ctx context.Context, number string) (*LineSettings, error)
}
