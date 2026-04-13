package line

import (
	"encoding/json"
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
