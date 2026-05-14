package signaling

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLineSettingsMessageJSONRoundTrip(t *testing.T) {
	in := &Message{
		Type:         TypeLineSettings,
		To:           "5550101",
		LineSettings: &LineSettings{VoiceStyle: "copper"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := ParseMessage(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Type != TypeLineSettings {
		t.Errorf("Type: got %q, want %q", got.Type, TypeLineSettings)
	}
	if got.LineSettings == nil || got.LineSettings.VoiceStyle != "copper" {
		t.Errorf("LineSettings: got %+v", got.LineSettings)
	}
}

// TestLineSettingsVoicemailJSONRoundTrip pins the on-the-wire shape of the
// voicemail sub-struct. Field names must match exactly what the daemon
// receiver decodes; this test breaks loudly if any tag drifts.
func TestLineSettingsVoicemailJSONRoundTrip(t *testing.T) {
	in := &Message{
		Type: TypeLineSettings,
		To:   "5550101",
		LineSettings: &LineSettings{
			VoiceStyle: "copper",
			Voicemail: &Voicemail{
				Enabled:            true,
				RingTimeoutSeconds: 30,
				MaxMessageSeconds:  120,
				MaxStoredMessages:  75,
				RetrievalCode:      "#42",
			},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Pin the exact wire keys; the daemon side decodes these literals.
	wire := string(b)
	for _, key := range []string{
		`"voicemail":`,
		`"enabled":true`,
		`"ring_timeout_seconds":30`,
		`"max_message_seconds":120`,
		`"max_stored_messages":75`,
		`"retrieval_code":"#42"`,
	} {
		if !strings.Contains(wire, key) {
			t.Errorf("wire missing %q in %s", key, wire)
		}
	}
	got, err := ParseMessage(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.LineSettings == nil || got.LineSettings.Voicemail == nil {
		t.Fatalf("LineSettings.Voicemail: got nil, want non-nil")
	}
	if *got.LineSettings.Voicemail != *in.LineSettings.Voicemail {
		t.Errorf("Voicemail round trip: got %+v, want %+v",
			*got.LineSettings.Voicemail, *in.LineSettings.Voicemail)
	}
}

// TestLineSettingsVoicemailOmittedWhenNil verifies that a nil Voicemail
// pointer marshals without emitting a "voicemail" key, so a pre-voicemail
// server staying silent does not surprise an old daemon.
func TestLineSettingsVoicemailOmittedWhenNil(t *testing.T) {
	in := &Message{
		Type:         TypeLineSettings,
		LineSettings: &LineSettings{VoiceStyle: "modern"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), `"voicemail"`) {
		t.Errorf("nil Voicemail should be omitted from JSON, got %s", b)
	}
}
