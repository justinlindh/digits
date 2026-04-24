package signaling

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// fakeLineStore is an in-memory LineStore for unit tests. It returns whatever
// LineSettings the caller wires in, keyed by phone number, and OR's a
// per-number household DND override into the SilentMode field on the way out
// so tests can exercise the effective-silent contract.
type fakeLineStore struct {
	settings map[string]*LineSettings
	dnd      map[string]bool
}

func newFakeLineStore() *fakeLineStore {
	return &fakeLineStore{
		settings: make(map[string]*LineSettings),
		dnd:      make(map[string]bool),
	}
}

func (f *fakeLineStore) set(number string, s *LineSettings) {
	f.settings[number] = s
}

func (f *fakeLineStore) setDND(number string, dnd bool) {
	f.dnd[number] = dnd
}

func (f *fakeLineStore) EffectiveLineSettings(ctx context.Context, number string) (*LineSettings, error) {
	s, ok := f.settings[number]
	if !ok {
		return nil, nil
	}
	out := *s
	if f.dnd[number] {
		out.SilentMode = true
	}
	return &out, nil
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

// TestOnRegistered_HouseholdDNDForcesSilent pins the OR contract: the line
// silent flag and the household do-not-disturb flag combine via OR before
// being pushed to the device. Whenever either is true, the device must see
// SilentMode: true; only when both are false should the device see
// SilentMode: false.
func TestOnRegistered_HouseholdDNDForcesSilent(t *testing.T) {
	cases := []struct {
		name         string
		lineSilent   bool
		householdDND bool
		wantSilent   bool
	}{
		{"both off", false, false, false},
		{"line silent only", true, false, true},
		{"DND only", false, true, true},
		{"both on", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewHub()
			lineStore := newFakeLineStore()
			lineStore.set("5550000", &LineSettings{
				VoiceStyle: "copper",
				SilentMode: tc.lineSilent,
			})
			lineStore.setDND("5550000", tc.householdDND)
			r := &Relay{Hub: hub, LineStore: lineStore}

			conn := &Conn{Send: make(chan []byte, 1), Number: "5550000"}
			hub.Register("5550000", conn)
			defer hub.Unregister("5550000", conn)

			r.OnRegistered(context.Background(), "5550000")

			select {
			case raw := <-conn.Send:
				var msg Message
				if err := json.Unmarshal(raw, &msg); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if msg.Type != TypeLineSettings {
					t.Fatalf("Type: got %q, want %q", msg.Type, TypeLineSettings)
				}
				if msg.LineSettings == nil {
					t.Fatal("LineSettings is nil")
				}
				if msg.LineSettings.SilentMode != tc.wantSilent {
					t.Errorf("SilentMode: got %v, want %v", msg.LineSettings.SilentMode, tc.wantSilent)
				}
			case <-time.After(time.Second):
				t.Fatal("no message sent")
			}
		})
	}
}
