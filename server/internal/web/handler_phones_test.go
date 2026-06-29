package web

import (
	"strings"
	"testing"

	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
)

func TestValidateDevModePassword(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "ok", in: "hunter2pw"},
		{name: "exactly min", in: "12345678"},
		{name: "exactly max", in: strings.Repeat("a", 72)},
		{name: "empty", in: "", wantErr: "password is required"},
		{name: "whitespace only", in: "   \t  ", wantErr: "password is required"},
		{name: "too short", in: "short", wantErr: "password must be at least 8 characters"},
		{name: "too long", in: strings.Repeat("a", 73), wantErr: "password must be at most 72 characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDevModePassword(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("want error %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateLineName(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr string
	}{
		{in: "Kitchen", want: "Kitchen"},
		{in: "  Kitchen  ", want: "Kitchen"},
		{in: "", wantErr: "name is required"},
		{in: "   ", wantErr: "name is required"},
		{in: strings.Repeat("a", 50), want: strings.Repeat("a", 50)},
		{in: strings.Repeat("a", 51), wantErr: "name too long"},
		{in: strings.Repeat("ä", 50), want: strings.Repeat("ä", 50)},
		{in: strings.Repeat("ä", 51), wantErr: "name too long"},
	}
	for _, tc := range cases {
		got, err := validateLineName(tc.in)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("validateLineName(%q): want error %q, got value=%q err=%v", tc.in, tc.wantErr, got, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("validateLineName(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("validateLineName(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOldestVersions(t *testing.T) {
	cases := []struct {
		name   string
		infos  []signaling.DeviceInfoSnapshot
		wantFw string
		wantPi string
	}{
		{name: "empty"},
		{
			name: "single device",
			infos: []signaling.DeviceInfoSnapshot{
				{FirmwareVersion: "1.2.0", PiVersion: "3.4.0"},
			},
			wantFw: "1.2.0",
			wantPi: "3.4.0",
		},
		{
			name: "picks oldest per component independently",
			infos: []signaling.DeviceInfoSnapshot{
				{FirmwareVersion: "1.5.0", PiVersion: "3.1.0"},
				{FirmwareVersion: "1.2.0", PiVersion: "3.9.0"},
			},
			wantFw: "1.2.0",
			wantPi: "3.1.0",
		},
		{
			name: "skips blank versions",
			infos: []signaling.DeviceInfoSnapshot{
				{FirmwareVersion: "", PiVersion: "3.4.0"},
				{FirmwareVersion: "1.2.0", PiVersion: ""},
			},
			wantFw: "1.2.0",
			wantPi: "3.4.0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fw, pi := oldestVersions(tc.infos)
			if fw != tc.wantFw || pi != tc.wantPi {
				t.Errorf("oldestVersions: got fw=%q pi=%q, want fw=%q pi=%q", fw, pi, tc.wantFw, tc.wantPi)
			}
		})
	}
}

func TestUpdateNotes(t *testing.T) {
	idx := &updates.ReleaseIndex{
		Pi: updates.ComponentIndex{
			Latest: "3.3.0",
			Releases: map[string]*updates.Release{
				"3.1.0": {Version: "3.1.0"},
				"3.2.0": {Version: "3.2.0"},
				"3.3.0": {Version: "3.3.0"},
			},
		},
		Firmware: updates.ComponentIndex{
			Latest: "1.3.0",
			Releases: map[string]*updates.Release{
				"1.1.0": {Version: "1.1.0"},
				"1.2.0": {Version: "1.2.0"},
				"1.3.0": {Version: "1.3.0"},
			},
		},
	}

	t.Run("nil index returns nothing", func(t *testing.T) {
		pi, fw := updateNotes(nil, []signaling.DeviceInfoSnapshot{{PiVersion: "3.1.0"}}, "3.3.0", "1.3.0")
		if pi != nil || fw != nil {
			t.Errorf("updateNotes(nil): got pi=%v fw=%v, want nil, nil", pi, fw)
		}
	})

	t.Run("ranges from oldest device up to latest", func(t *testing.T) {
		infos := []signaling.DeviceInfoSnapshot{
			{PiVersion: "3.1.0", FirmwareVersion: "1.2.0"},
			{PiVersion: "3.2.0", FirmwareVersion: "1.1.0"},
		}
		pi, fw := updateNotes(idx, infos, "3.3.0", "1.3.0")
		// Oldest Pi is 3.1.0, so notes span (3.1.0, 3.3.0]: 3.2.0 and 3.3.0.
		if got := versions(pi); !equal(got, []string{"3.3.0", "3.2.0"}) {
			t.Errorf("pi notes: got %v, want [3.3.0 3.2.0]", got)
		}
		// Oldest firmware is 1.1.0, so notes span (1.1.0, 1.3.0]: 1.2.0 and 1.3.0.
		if got := versions(fw); !equal(got, []string{"1.3.0", "1.2.0"}) {
			t.Errorf("fw notes: got %v, want [1.3.0 1.2.0]", got)
		}
	})

	t.Run("nothing behind latest yields no notes", func(t *testing.T) {
		infos := []signaling.DeviceInfoSnapshot{{PiVersion: "3.3.0", FirmwareVersion: "1.3.0"}}
		pi, fw := updateNotes(idx, infos, "3.3.0", "1.3.0")
		if len(pi) != 0 || len(fw) != 0 {
			t.Errorf("updateNotes (up to date): got pi=%v fw=%v, want empty", pi, fw)
		}
	})
}

func versions(rs []updates.Release) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Version
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
