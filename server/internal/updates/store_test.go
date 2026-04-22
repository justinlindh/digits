package updates

import (
	"testing"
)

func TestSortedReleases(t *testing.T) {
	idx := &ReleaseIndex{
		Pi: ComponentIndex{
			Latest: "1.2.0",
			Releases: map[string]*Release{
				"1.0.0": {Version: "1.0.0"},
				"1.2.0": {Version: "1.2.0"},
				"1.1.0": {Version: "1.1.0"},
			},
		},
	}
	releases := idx.SortedReleases("pi")
	if len(releases) != 3 {
		t.Fatalf("expected 3 releases, got %d", len(releases))
	}
	// Newest first
	want := []string{"1.2.0", "1.1.0", "1.0.0"}
	for i, r := range releases {
		if r.Version != want[i] {
			t.Errorf("releases[%d].Version = %q, want %q", i, r.Version, want[i])
		}
	}
}

func TestSortedReleases_UnknownComponent(t *testing.T) {
	idx := &ReleaseIndex{}
	if got := idx.SortedReleases("bogus"); got != nil {
		t.Errorf("expected nil for unknown component, got %v", got)
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.0", "1.1.0", 1},
		{"1.1.0", "1.2.0", -1},
		{"1.2.0", "1.2.0", 0},
		{"2.0.0", "1.9.9", 1},
		{"10.0.0", "9.0.0", 1}, // multi-digit
		{"1.10.0", "1.9.0", 1}, // multi-digit minor
		{"0.5.0", "0.4.9", 1},
	}
	for _, c := range cases {
		got := CompareSemver(c.a, c.b)
		if got != c.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestRangeReleases(t *testing.T) {
	idx := &ReleaseIndex{
		Firmware: ComponentIndex{
			Latest: "1.5.0",
			Releases: map[string]*Release{
				"1.0.0": {Version: "1.0.0"},
				"1.2.0": {Version: "1.2.0"},
				"1.3.0": {Version: "1.3.0"},
				"1.4.0": {Version: "1.4.0"},
				"1.5.0": {Version: "1.5.0"},
			},
		},
	}

	tests := []struct {
		name      string
		component string
		from      string
		to        string
		want      []string
	}{
		{"two versions behind", "firmware", "1.3.0", "1.5.0", []string{"1.5.0", "1.4.0"}},
		{"one version behind", "firmware", "1.4.0", "1.5.0", []string{"1.5.0"}},
		{"up to date", "firmware", "1.5.0", "1.5.0", nil},
		{"from greater than to", "firmware", "1.6.0", "1.5.0", nil},
		{"empty from returns all up to and including to", "firmware", "", "1.3.0", []string{"1.3.0", "1.2.0", "1.0.0"}},
		{"unknown component", "bogus", "1.0.0", "1.5.0", nil},
		{"missing 'to' version", "firmware", "1.2.0", "9.9.9", []string{"1.5.0", "1.4.0", "1.3.0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idx.RangeReleases(tt.component, tt.from, tt.to)
			if len(got) != len(tt.want) {
				t.Fatalf("RangeReleases(%q,%q,%q) returned %d releases, want %d",
					tt.component, tt.from, tt.to, len(got), len(tt.want))
			}
			for i, r := range got {
				if r.Version != tt.want[i] {
					t.Errorf("[%d] Version = %q, want %q", i, r.Version, tt.want[i])
				}
			}
		})
	}
}
