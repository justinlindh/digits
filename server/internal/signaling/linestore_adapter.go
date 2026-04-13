package signaling

import "github.com/justinlindh/digits/server/internal/line"

// lineStoreAdapter wraps a real *line.Store so the signaling package can
// consume it through the LineStore interface without importing internal/line
// into its type system elsewhere.
type lineStoreAdapter struct {
	inner *line.Store
}

// NewLineStoreAdapter returns a signaling.LineStore backed by a real line.Store.
func NewLineStoreAdapter(s *line.Store) LineStore {
	return &lineStoreAdapter{inner: s}
}

func (a *lineStoreAdapter) LineSettingsByNumber(number string) (*LineSettings, error) {
	ln, err := a.inner.GetByNumber(number)
	if err != nil {
		return nil, err
	}
	return &LineSettings{VoiceStyle: ln.Settings.VoiceStyle}, nil
}
