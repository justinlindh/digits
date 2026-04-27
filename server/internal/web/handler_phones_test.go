package web

import (
	"strings"
	"testing"
)

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
