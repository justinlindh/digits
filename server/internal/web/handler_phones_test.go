package web

import (
	"strings"
	"testing"
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
