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

func TestSettingsAutoUpdateDefaultFalse(t *testing.T) {
	s := DefaultSettings()
	if s.AutoUpdate {
		t.Errorf("AutoUpdate default: got true, want false")
	}
}

func TestSettingsJSONRoundTripAutoUpdate(t *testing.T) {
	in := Settings{VoiceStyle: VoiceStyleCopper, AutoUpdate: true}
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

func TestSettingsJSONOmitsAutoUpdateWhenFalse(t *testing.T) {
	s := Settings{VoiceStyle: VoiceStyleCopper, AutoUpdate: false}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "auto_update") {
		t.Errorf("expected auto_update omitted when false, got %s", b)
	}
}

func TestSettingsMergeAutoUpdateFromPatch(t *testing.T) {
	base := DefaultSettings()
	patch := Settings{AutoUpdate: true}
	merged := base.Merge(patch)
	if !merged.AutoUpdate {
		t.Errorf("Merge did not take AutoUpdate from patch")
	}
	if merged.VoiceStyle != VoiceStyleCopper {
		t.Errorf("Merge clobbered VoiceStyle: got %q", merged.VoiceStyle)
	}
}

func TestDefaultVoicemailMatchesPhaseOneSpec(t *testing.T) {
	v := DefaultVoicemail()
	if v.Enabled {
		t.Errorf("Enabled default: got true, want false")
	}
	if v.RingTimeoutSeconds != 20 {
		t.Errorf("RingTimeoutSeconds default: got %d, want 20", v.RingTimeoutSeconds)
	}
	if v.MaxMessageSeconds != 90 {
		t.Errorf("MaxMessageSeconds default: got %d, want 90", v.MaxMessageSeconds)
	}
	if v.MaxStoredMessages != 50 {
		t.Errorf("MaxStoredMessages default: got %d, want 50", v.MaxStoredMessages)
	}
	if v.RetrievalCode != "*98" {
		t.Errorf("RetrievalCode default: got %q, want %q", v.RetrievalCode, "*98")
	}
}

func TestDefaultSettingsIncludesVoicemailDefaults(t *testing.T) {
	s := DefaultSettings()
	if s.Voicemail != DefaultVoicemail() {
		t.Errorf("DefaultSettings.Voicemail: got %+v, want %+v", s.Voicemail, DefaultVoicemail())
	}
}

func TestSettingsJSONRoundTripVoicemail(t *testing.T) {
	in := Settings{
		VoiceStyle: VoiceStyleCopper,
		Voicemail: Voicemail{
			Enabled:            true,
			RingTimeoutSeconds: 30,
			MaxMessageSeconds:  120,
			MaxStoredMessages:  75,
			RetrievalCode:      "#42",
		},
	}
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

func TestSettingsMergeVoicemailLayersOnDefaults(t *testing.T) {
	base := DefaultSettings()
	patch := Settings{Voicemail: Voicemail{
		Enabled:            true,
		RingTimeoutSeconds: 30,
		// MaxMessageSeconds, MaxStoredMessages, RetrievalCode unset (zero)
		// should keep defaults from base.
	}}
	merged := base.Merge(patch)
	if !merged.Voicemail.Enabled {
		t.Errorf("Enabled not propagated from patch")
	}
	if merged.Voicemail.RingTimeoutSeconds != 30 {
		t.Errorf("RingTimeoutSeconds: got %d, want 30", merged.Voicemail.RingTimeoutSeconds)
	}
	if merged.Voicemail.MaxMessageSeconds != DefaultVoicemailMaxMessageSeconds {
		t.Errorf("MaxMessageSeconds: got %d, want default %d",
			merged.Voicemail.MaxMessageSeconds, DefaultVoicemailMaxMessageSeconds)
	}
	if merged.Voicemail.RetrievalCode != DefaultVoicemailRetrievalCode {
		t.Errorf("RetrievalCode: got %q, want default %q",
			merged.Voicemail.RetrievalCode, DefaultVoicemailRetrievalCode)
	}
}

func TestSettingsNormalizeClampsOutOfRangeInts(t *testing.T) {
	cases := []struct {
		name string
		in   Voicemail
		want Voicemail
	}{
		{
			name: "zero values get defaults",
			in:   Voicemail{Enabled: true, RetrievalCode: "*98"},
			want: Voicemail{
				Enabled:            true,
				RingTimeoutSeconds: 20,
				MaxMessageSeconds:  90,
				MaxStoredMessages:  50,
				RetrievalCode:      "*98",
			},
		},
		{
			name: "below-min ring timeout reset",
			in: Voicemail{
				Enabled:            true,
				RingTimeoutSeconds: 1,
				MaxMessageSeconds:  60,
				MaxStoredMessages:  10,
				RetrievalCode:      "*99",
			},
			want: Voicemail{
				Enabled:            true,
				RingTimeoutSeconds: 20, // 1 < min(5) → default
				MaxMessageSeconds:  60,
				MaxStoredMessages:  10,
				RetrievalCode:      "*99",
			},
		},
		{
			name: "above-max max message reset",
			in: Voicemail{
				Enabled:            true,
				RingTimeoutSeconds: 30,
				MaxMessageSeconds:  600,
				MaxStoredMessages:  10,
				RetrievalCode:      "*99",
			},
			want: Voicemail{
				Enabled:            true,
				RingTimeoutSeconds: 30,
				MaxMessageSeconds:  90, // 600 > max(180) → default
				MaxStoredMessages:  10,
				RetrievalCode:      "*99",
			},
		},
		{
			name: "below-min max stored reset",
			in: Voicemail{
				Enabled:            false,
				RingTimeoutSeconds: 30,
				MaxMessageSeconds:  60,
				MaxStoredMessages:  2,
				RetrievalCode:      "*98",
			},
			want: Voicemail{
				Enabled:            false,
				RingTimeoutSeconds: 30,
				MaxMessageSeconds:  60,
				MaxStoredMessages:  50, // 2 < min(5) → default
				RetrievalCode:      "*98",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.Normalize()
			if got != tc.want {
				t.Errorf("Normalize: got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSettingsNormalizeRetrievalCodeFallback(t *testing.T) {
	cases := map[string]string{
		"":         "*98",      // empty
		"1234567":  "*98",      // too long
		"a":        "*98",      // bad chars
		"123":      "*98",      // digits only, no * or #
		"12345":    "*98",      // digits only, no * or # (would shadow 7-digit dialing)
		"*98":      "*98",      // valid: star prefix
		"#42":      "#42",      // valid: hash prefix
		"1#":       "1#",       // valid: hash with digit
		"*123":     "*123",     // valid: longer with star
		"##":       "##",       // valid: shortest acceptable
		"123#":     "123#",     // valid: trailing hash
	}
	for in, want := range cases {
		got := (Voicemail{
			RingTimeoutSeconds: 20,
			MaxMessageSeconds:  90,
			MaxStoredMessages:  50,
			RetrievalCode:      in,
		}).Normalize().RetrievalCode
		if got != want {
			t.Errorf("Normalize(%q).RetrievalCode = %q, want %q", in, got, want)
		}
	}
}

func TestIsValidRetrievalCode(t *testing.T) {
	valid := []string{"*98", "#42", "*123", "##", "1#", "*0", "1234*", "*1234"}
	for _, code := range valid {
		if !IsValidRetrievalCode(code) {
			t.Errorf("IsValidRetrievalCode(%q) = false, want true", code)
		}
	}
	invalid := []string{
		"",        // empty
		"1",       // too short
		"1234567", // too long
		"abc",     // bad chars
		"*",       // 1-char, too short
		"123",     // no * or #
		"12345",   // no * or #
		"1 2",     // space
	}
	for _, code := range invalid {
		if IsValidRetrievalCode(code) {
			t.Errorf("IsValidRetrievalCode(%q) = true, want false", code)
		}
	}
}

func TestSettingsScanFromEmptyJSONAppliesDefaults(t *testing.T) {
	// Mirrors what store.scanSettings does for a fresh row.
	var patch Settings
	if err := json.Unmarshal([]byte(`{}`), &patch); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	merged := DefaultSettings().Merge(patch).Normalize()
	if merged.Voicemail != DefaultVoicemail() {
		t.Errorf("empty JSONB should leave defaults intact, got %+v", merged.Voicemail)
	}
}
