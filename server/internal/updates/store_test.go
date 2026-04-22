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
		{"10.0.0", "9.0.0", 1},  // multi-digit
		{"1.10.0", "1.9.0", 1},  // multi-digit minor
		{"0.5.0", "0.4.9", 1},
	}
	for _, c := range cases {
		got := CompareSemver(c.a, c.b)
		if got != c.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
