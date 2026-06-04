package main

import (
	"reflect"
	"slices"
	"testing"
)

func TestPairingAnnouncementClips(t *testing.T) {
	// The regression: 10+ minutes must cap to spoken_9, never the nonexistent
	// spoken_10 (which played as a silent number).
	for _, m := range []int{10, 11, 100} {
		clips := pairingAnnouncementClips("12", m)
		if !slices.Contains(clips, "spoken_9") {
			t.Errorf("minutesLeft=%d: want spoken_9, got %v", m, clips)
		}
		if slices.Contains(clips, "spoken_10") {
			t.Errorf("minutesLeft=%d: must not reference the nonexistent spoken_10", m)
		}
		if !slices.Contains(clips, "pairing_expires_minutes") {
			t.Errorf("minutesLeft=%d: want plural unit", m)
		}
	}

	// Exactly 1 minute uses the singular unit.
	one := pairingAnnouncementClips("9", 1)
	if !slices.Contains(one, "spoken_1") || !slices.Contains(one, "pairing_expires_minute") {
		t.Errorf("minutesLeft=1: want spoken_1 + singular unit, got %v", one)
	}
	if slices.Contains(one, "pairing_expires_minutes") {
		t.Error("minutesLeft=1 must use the singular unit, not plural")
	}

	// 0 / negative floor to 1 (singular).
	zero := pairingAnnouncementClips("9", 0)
	if !slices.Contains(zero, "spoken_1") || !slices.Contains(zero, "pairing_expires_minute") {
		t.Errorf("minutesLeft=0: want clamp to 1, got %v", zero)
	}

	// Full sequence: code digits expand in order, framed by the fixed clips.
	got := pairingAnnouncementClips("314", 5)
	want := []string{
		"pairing_silence", "pairing_welcome",
		"spoken_3", "spoken_1", "spoken_4",
		"pairing_expires_prefix", "spoken_5", "pairing_expires_minutes",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sequence mismatch:\n got %v\nwant %v", got, want)
	}
}
