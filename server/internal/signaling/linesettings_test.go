package signaling

import (
	"encoding/json"
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
