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

// LineSettingsFromLine projects a line.Settings into the wire LineSettings the
// device consumes. It is the single source of truth for which line fields ride
// the wire (note QuietHours is deliberately not among them), so registration
// pushes and on-change pushes can never drift to different field sets.
func LineSettingsFromLine(s line.Settings) *LineSettings {
	return &LineSettings{
		VoiceStyle: s.VoiceStyle,
		SilentMode: s.SilentMode,
		AutoUpdate: s.AutoUpdate,
		Voicemail:  voicemailFromLine(s.Voicemail),
	}
}

// voicemailFromLine projects a line.Voicemail into its wire representation.
func voicemailFromLine(v line.Voicemail) *Voicemail {
	return &Voicemail{
		Enabled:            v.Enabled,
		RingTimeoutSeconds: v.RingTimeoutSeconds,
	}
}

func (a *lineStoreAdapter) EffectiveLineSettings(ctx context.Context, number string) (*LineSettings, error) {
	settings, err := a.inner.EffectiveSettingsByNumber(ctx, number)
	if err != nil {
		return nil, err
	}
	return LineSettingsFromLine(settings), nil
}

func (a *lineStoreAdapter) LineIdentifiers(ctx context.Context, number string) (int64, string, error) {
	l, err := a.inner.GetByNumber(ctx, number)
	if err != nil {
		return 0, "", err
	}
	return l.ID, l.HouseholdID, nil
}
