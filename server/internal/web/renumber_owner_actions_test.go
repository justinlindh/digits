package web

import (
	"testing"

	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
)

func TestChangelogCountsKnownLineIdentityAfterNumberReuse(t *testing.T) {
	const number = "3140042"
	hub := signaling.NewHub()
	hub.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	oldOwner := &signaling.Conn{LineID: 1, HardwareID: "old-owner", PiVersion: "1.0.0", Send: make(chan []byte, 1)}
	newOwner := &signaling.Conn{LineID: 2, HardwareID: "new-owner", PiVersion: "2.0.0", Send: make(chan []byte, 1)}
	if err := hub.Register(number, oldOwner); err != nil {
		t.Fatal(err)
	}
	if err := hub.Register(number, newOwner); err != nil {
		t.Fatal(err)
	}
	idx := &updates.ReleaseIndex{Pi: updates.ComponentIndex{Releases: map[string]*updates.Release{
		"1.0.0": {Version: "1.0.0"},
		"2.0.0": {Version: "2.0.0"},
	}}}
	h := &Handler{hub: hub}

	got := h.buildChangelogSection(idx, updates.ComponentPi, []line.Line{{ID: 1, Number: number}})
	counts := make(map[string]int)
	for _, release := range got {
		counts[release.Version] = release.PhoneCount
		if release.TotalCount != 1 {
			t.Fatalf("total count = %d, want 1", release.TotalCount)
		}
	}
	if counts["1.0.0"] != 1 || counts["2.0.0"] != 0 {
		t.Fatalf("known owner counts = %#v, want only 1.0.0", counts)
	}
}
