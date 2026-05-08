package signaling

import (
	"context"

	"github.com/justinlindh/digits/server/internal/line"
)

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

func (a *lineStoreAdapter) EffectiveLineSettings(ctx context.Context, number string) (*LineSettings, error) {
	settings, householdDND, err := a.inner.EffectiveSettingsByNumber(ctx, number)
	if err != nil {
		return nil, err
	}
	silent := line.EffectiveSilent(settings, householdDND)
	return &LineSettings{
		VoiceStyle: settings.VoiceStyle,
		SilentMode: silent,
		AutoUpdate: settings.AutoUpdate,
	}, nil
}

func (a *lineStoreAdapter) LineIdentifiers(ctx context.Context, number string) (int64, string, error) {
	l, err := a.inner.GetByNumber(ctx, number)
	if err != nil {
		return 0, "", err
	}
	return l.ID, l.HouseholdID, nil
}
