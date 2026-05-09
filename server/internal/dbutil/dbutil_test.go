package dbutil

import (
	"testing"
)

func TestPlaceholders(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{n: -1, want: ""},
		{n: 0, want: ""},
		{n: 1, want: "$1"},
		{n: 2, want: "$1, $2"},
		{n: 3, want: "$1, $2, $3"},
		{n: 5, want: "$1, $2, $3, $4, $5"},
	}
	for _, tc := range tests {
		got := Placeholders(tc.n)
		if got != tc.want {
			t.Errorf("Placeholders(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
