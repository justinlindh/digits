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
	} {
		if !strings.Contains(wire, key) {
			t.Errorf("wire missing %q in %s", key, wire)
		}
	}
	// Removed fields must not ride the wire.
	for _, banned := range []string{"max_message_seconds", "max_stored_messages", "retrieval_code"} {
		if strings.Contains(wire, banned) {
			t.Errorf("wire must not carry %q, got %s", banned, wire)
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

// TestLineSettingsVoicemailZeroValuesRoundTrip pins the contract that
// inner Voicemail fields have no omitempty: a non-nil pointer to a
// zero-value Voicemail must serialize every key with its zero value so
// the daemon can distinguish "server didn't set this" (nil outer) from
// "server set this to zero" (outer present with explicit zero).
func TestLineSettingsVoicemailZeroValuesRoundTrip(t *testing.T) {
	in := &Message{
		Type: TypeLineSettings,
		LineSettings: &LineSettings{
			VoiceStyle: "copper",
			Voicemail:  &Voicemail{}, // all zero values
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(b)
	for _, key := range []string{
		`"enabled":false`,
		`"ring_timeout_seconds":0`,
	} {
		if !strings.Contains(wire, key) {
			t.Errorf("expected zero-value %q in wire, got %s", key, wire)
		}
	}
}
