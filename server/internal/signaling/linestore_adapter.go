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

// VoicemailFromLine projects a line.Voicemail into its wire representation.
// Adding a new voicemail field requires updating this helper and the matching
// receiver in pi/digitsd/internal/signal; collapsing the per-callsite field-
// copy into one place keeps the wire contract from drifting silently.
func VoicemailFromLine(v line.Voicemail) *Voicemail {
	return &Voicemail{
		Enabled:            v.Enabled,
		RingTimeoutSeconds: v.RingTimeoutSeconds,
		MaxMessageSeconds:  v.MaxMessageSeconds,
		MaxStoredMessages:  v.MaxStoredMessages,
		RetrievalCode:      v.RetrievalCode,
	}
}

func (a *lineStoreAdapter) EffectiveLineSettings(ctx context.Context, number string) (*LineSettings, error) {
	settings, err := a.inner.EffectiveSettingsByNumber(ctx, number)
	if err != nil {
		return nil, err
	}
	return &LineSettings{
		VoiceStyle: settings.VoiceStyle,
		SilentMode: settings.SilentMode,
		AutoUpdate: settings.AutoUpdate,
		Voicemail:  VoicemailFromLine(settings.Voicemail),
	}, nil
}

func (a *lineStoreAdapter) LineIdentifiers(ctx context.Context, number string) (int64, string, error) {
	l, err := a.inner.GetByNumber(ctx, number)
	if err != nil {
		return 0, "", err
	}
	return l.ID, l.HouseholdID, nil
}
