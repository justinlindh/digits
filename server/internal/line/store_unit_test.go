package line

import "testing"

// These cover the pure phone-number helpers, which have no database
// dependency and so belong in the fast (untagged) test tier rather than
// behind //go:build integration alongside the Store DB tests.

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"3140001", "314-0001"},
		{"5551234", "555-1234"},
		{"1234567", "123-4567"},
		{"short", "short"},       // too short; returned as-is
		{"12345678", "12345678"}, // too long; returned as-is
	}
	for _, tt := range tests {
		got := FormatNumber(tt.input)
		if got != tt.want {
			t.Errorf("FormatNumber(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateNumber(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"3140001", false},
		{"314-0001", false}, // hyphenated form accepted
		{"12345", true},     // too short
		{"12345678", true},  // too long
		{"314-00001", true}, // too many digits
		{"3-140001", true},  // hyphen in wrong position
		{"abcdefg", true},   // non-digits
	}
	for _, tt := range tests {
		err := ValidateNumber(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateNumber(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
	}
}

func TestStripNumber(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"314-0001", "3140001"}, // hyphenated form stripped
		{"3140001", "3140001"},  // already bare; unchanged
		{"3-1-4-0-0-0-1", "3140001"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := StripNumber(tt.input); got != tt.want {
			t.Errorf("StripNumber(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
