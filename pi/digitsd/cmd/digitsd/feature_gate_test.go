package main

import "testing"

func TestHookFlashCapable(t *testing.T) {
	cases := []struct {
		ver  string
		want bool
	}{
		{"1.4.0", false},                 // pre-flash
		{"1.4.9", false},                 // still pre-flash
		{"1.5.0", true},                  // first version with flash
		{"1.5.1", true},                  // patch
		{"1.6.0", true},                  // minor
		{"2.0.0", true},                  // major
		{"1.5.0-57-g1c86d2f-dirty", true}, // dev build past the 1.5.0 tag
		{"1.5.0-rc.1", true},             // pre-release of 1.5.0 (treated as capable)
		{"1.4.9-dirty", false},           // dev build of older tag stays incapable
		{"1.5.0+buildmeta", true},        // build metadata is stripped
		{"", false},                      // unknown
		{"dev", false},                   // non-semver
		{"invalid", false},               // non-semver
	}
	for _, c := range cases {
		got := hookFlashCapable(c.ver)
		if got != c.want {
			t.Errorf("hookFlashCapable(%q) = %v, want %v", c.ver, got, c.want)
		}
	}
}
