package signaling

// LineStore is the subset of line.Store the signaling layer needs to fetch
// per-line settings. Kept as a small interface here so the signaling package
// does not import the real internal/line package and stays a leaf in the
// dependency graph.
type LineStore interface {
	LineSettingsByNumber(number string) (*LineSettings, error)
}
