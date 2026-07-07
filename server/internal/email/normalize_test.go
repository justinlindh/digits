package email

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercases", "John@Example.com", "john@example.com"},
		{"trims surrounding whitespace", "  user@example.com\t", "user@example.com"},
		{"trims and lowercases together", "  Mixed.Case@Example.COM ", "mixed.case@example.com"},
		{"already canonical is unchanged", "user@example.com", "user@example.com"},
		{"empty stays empty", "", ""},
		{"whitespace only collapses to empty", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.in); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
