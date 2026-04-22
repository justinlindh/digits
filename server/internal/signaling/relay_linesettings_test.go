package signaling

import (
	"context"
	"testing"
)

// fakeLineStore is an in-memory LineStore for unit tests. It returns whatever
// LineSettings the caller wires in, keyed by phone number.
type fakeLineStore struct {
	settings map[string]*LineSettings
}

func newFakeLineStore() *fakeLineStore {
	return &fakeLineStore{settings: make(map[string]*LineSettings)}
}

func (f *fakeLineStore) set(number string, s *LineSettings) {
	f.settings[number] = s
}

func (f *fakeLineStore) LineSettingsByNumber(ctx context.Context, number string) (*LineSettings, error) {
	return f.settings[number], nil
}

// TestOnRegisteredPushesSilentMode verifies that when OnRegistered is called
// for a number whose stored LineSettings has SilentMode: true, the hub
// receives a TypeLineSettings message with SilentMode: true and the correct
// VoiceStyle in the payload.
func TestOnRegisteredPushesSilentMode(t *testing.T) {
	hub := NewHub()
	store := newFakeLineStore()
	store.set("3140001", &LineSettings{
		VoiceStyle: "copper",
		SilentMode: true,
	})
	relay := NewRelay(hub, nil, nil, store)

	conn := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn)

	relay.OnRegistered(context.Background(), "3140001")

	select {
	case data := <-conn.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeLineSettings {
			t.Errorf("Type: got %q, want %q", msg.Type, TypeLineSettings)
		}
		if msg.LineSettings == nil {
			t.Fatal("LineSettings: got nil, want non-nil")
		}
		if !msg.LineSettings.SilentMode {
			t.Errorf("SilentMode: got false, want true")
		}
		if msg.LineSettings.VoiceStyle != "copper" {
			t.Errorf("VoiceStyle: got %q, want %q", msg.LineSettings.VoiceStyle, "copper")
		}
	default:
		t.Fatal("device did not receive a line_settings push after OnRegistered")
	}
}

// TestOnRegisteredPushesSilentModeFalseByDefault verifies that when
// OnRegistered is called for a number whose stored LineSettings has
// SilentMode: false, the pushed message also carries SilentMode: false.
// This pins the wire semantics at the relay boundary so a future refactor
// cannot silently (pun intended) drop the field.
func TestOnRegisteredPushesSilentModeFalseByDefault(t *testing.T) {
	hub := NewHub()
	store := newFakeLineStore()
	store.set("3140002", &LineSettings{
		VoiceStyle: "modern",
		SilentMode: false,
	})
	relay := NewRelay(hub, nil, nil, store)

	conn := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140002", conn)

	relay.OnRegistered(context.Background(), "3140002")

	select {
	case data := <-conn.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeLineSettings {
			t.Errorf("Type: got %q, want %q", msg.Type, TypeLineSettings)
		}
		if msg.LineSettings == nil {
			t.Fatal("LineSettings: got nil, want non-nil")
		}
		if msg.LineSettings.SilentMode {
			t.Errorf("SilentMode: got true, want false")
		}
		if msg.LineSettings.VoiceStyle != "modern" {
			t.Errorf("VoiceStyle: got %q, want %q", msg.LineSettings.VoiceStyle, "modern")
		}
	default:
		t.Fatal("device did not receive a line_settings push after OnRegistered")
	}
}
