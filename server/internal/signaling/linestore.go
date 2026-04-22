package signaling

import "context"

// LineStore is the subset of line.Store the signaling layer needs to fetch
// per-line settings. Kept as a small interface here so the dependency on
// internal/line is isolated to a single adapter file (linestore_adapter.go)
// rather than threaded through the whole signaling package.
type LineStore interface {
	LineSettingsByNumber(ctx context.Context, number string) (*LineSettings, error)
}
