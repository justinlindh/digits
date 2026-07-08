package signaling

import (
	"testing"

	"github.com/justinlindh/digits/server/internal/line"
)

// TestLineSettingsFromLine pins the wire-projection contract described in
// linestore_adapter.go: every user-configurable line field must ride the wire,
// and QuietHours must deliberately stay off it. Both the registration push and
// the on-change push go through this projection, so a field silently dropped
// here would drift the two apart without any test failing elsewhere.
func TestLineSettingsFromLine(t *testing.T) {
	in := line.Settings{
		VoiceStyle: line.VoiceStyleModern,
		SilentMode: true,
		AutoUpdate: true,
		Voicemail: line.Voicemail{
			Enabled:            true,
			RingTimeoutSeconds: 42,
		},
		// QuietHours is populated with a non-zero value to prove the
		// projection does not carry it onto the wire.
		QuietHours: line.QuietHours{
			Enabled: true,
			Start:   "22:00",
			End:     "07:00",
			Days:    line.AllDays(),
		},
	}

	got := LineSettingsFromLine(in)

	if got.VoiceStyle != in.VoiceStyle {
		t.Errorf("VoiceStyle: got %q, want %q", got.VoiceStyle, in.VoiceStyle)
	}
	if got.SilentMode != in.SilentMode {
		t.Errorf("SilentMode: got %v, want %v", got.SilentMode, in.SilentMode)
	}
	if got.AutoUpdate != in.AutoUpdate {
		t.Errorf("AutoUpdate: got %v, want %v", got.AutoUpdate, in.AutoUpdate)
	}
	if got.Voicemail == nil {
		t.Fatal("Voicemail: got nil, want non-nil projection")
	}
	if got.Voicemail.Enabled != in.Voicemail.Enabled {
		t.Errorf("Voicemail.Enabled: got %v, want %v", got.Voicemail.Enabled, in.Voicemail.Enabled)
	}
	if got.Voicemail.RingTimeoutSeconds != in.Voicemail.RingTimeoutSeconds {
		t.Errorf("Voicemail.RingTimeoutSeconds: got %d, want %d",
			got.Voicemail.RingTimeoutSeconds, in.Voicemail.RingTimeoutSeconds)
	}
}

// TestLineSettingsFromLine_ZeroVoicemail confirms a disabled/zero voicemail
// still projects to a non-nil pointer, so a device can distinguish "voicemail
// explicitly disabled" (non-nil, Enabled=false) from "settings push from a
// pre-voicemail server" (nil), the distinction protocol.go documents.
func TestLineSettingsFromLine_ZeroVoicemail(t *testing.T) {
	got := LineSettingsFromLine(line.Settings{})
	if got.Voicemail == nil {
		t.Fatal("Voicemail: got nil, want non-nil pointer even for the zero value")
	}
	if got.Voicemail.Enabled {
		t.Error("Voicemail.Enabled: got true, want false for the zero value")
	}
}
