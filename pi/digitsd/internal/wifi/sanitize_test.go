package wifi

import "testing"

func TestSanitizeSSID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii", "HomeNet", "HomeNet"},
		{"with space", "Home Net", "Home-Net"},
		{"with punctuation", "Bob's WiFi!", "Bobs-WiFi"},
		{"non-ascii dropped", "café", "caf"},
		{"empty becomes network", "", "network"},
		{"all punctuation becomes network", "!!!", "network"},
		{"underscores preserved as hyphens", "home_net", "home-net"},
		{"collapses multiple hyphens", "a__b", "a-b"},
		{"trims leading/trailing hyphens", "-home-", "home"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeSSID(tc.in)
			if got != tc.want {
				t.Errorf("SanitizeSSID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeSSIDTruncates(t *testing.T) {
	long := "a-very-long-ssid-name-that-is-way-past-the-filesystem-limit-for-sure-xxxxxx"
	got := SanitizeSSID(long)
	if len(got) != 50 {
		t.Errorf("len(%q) = %d, want 50", got, len(got))
	}
}
