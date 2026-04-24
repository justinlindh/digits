package line

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultSettingsVoiceStyleCopper(t *testing.T) {
	s := DefaultSettings()
	if s.VoiceStyle != VoiceStyleCopper {
		t.Errorf("VoiceStyle: got %q, want %q", s.VoiceStyle, VoiceStyleCopper)
	}
}

func TestSettingsMergePreservesUnset(t *testing.T) {
	base := DefaultSettings()
	patch := Settings{} // empty JSON from DB
	merged := base.Merge(patch)
	if merged.VoiceStyle != VoiceStyleCopper {
		t.Errorf("empty merge should keep default, got %q", merged.VoiceStyle)
	}
}

func TestSettingsMergeOverwrites(t *testing.T) {
	base := DefaultSettings()
	patch := Settings{VoiceStyle: VoiceStyleModern}
	merged := base.Merge(patch)
	if merged.VoiceStyle != VoiceStyleModern {
		t.Errorf("merge did not overwrite, got %q", merged.VoiceStyle)
	}
}

func TestSettingsJSONRoundTrip(t *testing.T) {
	in := Settings{VoiceStyle: VoiceStyleModern}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Settings
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round trip: got %+v, want %+v", out, in)
	}
}

func TestSettingsUnmarshalEmptyJSONIsZeroValue(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{}`), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.VoiceStyle != "" {
		t.Errorf("empty JSON: got VoiceStyle %q, want empty string", s.VoiceStyle)
	}
}

func TestSettingsNormalizeAcceptsKnownValues(t *testing.T) {
	for _, v := range []string{VoiceStyleCopper, VoiceStyleModern} {
		s := Settings{VoiceStyle: v}
		if got := s.Normalize().VoiceStyle; got != v {
			t.Errorf("Normalize(%q).VoiceStyle = %q", v, got)
		}
	}
}

func TestSettingsNormalizeCoercesUnknown(t *testing.T) {
	s := Settings{VoiceStyle: "bogus"}
	if got := s.Normalize().VoiceStyle; got != VoiceStyleCopper {
		t.Errorf("unknown value should fall back to copper, got %q", got)
	}
}

func TestSettingsSilentModeDefaultFalse(t *testing.T) {
	s := DefaultSettings()
	if s.SilentMode {
		t.Errorf("SilentMode default: got true, want false")
	}
}

func TestSettingsJSONRoundTripSilentMode(t *testing.T) {
	in := Settings{VoiceStyle: VoiceStyleCopper, SilentMode: true}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Settings
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round trip: got %+v, want %+v", out, in)
	}
}

func TestSettingsJSONOmitsSilentModeWhenFalse(t *testing.T) {
	// omitempty keeps the JSONB payload free of noise when silent is off.
	s := Settings{VoiceStyle: VoiceStyleCopper, SilentMode: false}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "silent_mode") {
		t.Errorf("expected silent_mode omitted when false, got %s", b)
	}
}

func TestSettingsMergeSilentModeFromPatch(t *testing.T) {
	base := DefaultSettings()
	patch := Settings{SilentMode: true}
	merged := base.Merge(patch)
	if !merged.SilentMode {
		t.Errorf("Merge did not take SilentMode from patch")
	}
	if merged.VoiceStyle != VoiceStyleCopper {
		t.Errorf("Merge clobbered VoiceStyle: got %q", merged.VoiceStyle)
	}
}

func TestEffectiveSilent(t *testing.T) {
	cases := []struct {
		name         string
		lineSilent   bool
		householdDND bool
		want         bool
	}{
		{"both off", false, false, false},
		{"line silent only", true, false, true},
		{"household DND only", false, true, true},
		{"both on", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EffectiveSilent(Settings{SilentMode: tc.lineSilent}, tc.householdDND)
			if got != tc.want {
				t.Errorf("EffectiveSilent(silent=%v, dnd=%v) = %v, want %v",
					tc.lineSilent, tc.householdDND, got, tc.want)
			}
		})
	}
}
