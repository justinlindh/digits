package wifi

import "strings"

// SanitizeSSID returns a filesystem-safe version of an SSID for use as part
// of a .nmconnection filename. Alphanumeric characters are preserved. Spaces,
// hyphens, and underscores become hyphens; runs collapse; leading/trailing
// hyphens are trimmed. Non-alphanumeric characters are dropped. Empty or
// all-punctuation input becomes "network". Result is truncated to 50 chars.
func SanitizeSSID(ssid string) string {
	var b strings.Builder
	for _, r := range ssid {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-")
	if out == "" {
		return "network"
	}
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}
