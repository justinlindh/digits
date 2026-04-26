package email

import (
	"strings"
	"testing"
)

func TestMagicLinkEmailContainsBranding(t *testing.T) {
	subject, body := MagicLinkEmail("https://app.digits.family/auth/magic/abc123")
	if !strings.Contains(subject, "Digits") {
		t.Fatal("subject should mention Digits")
	}
	if !strings.Contains(body, "DIGITS") {
		t.Fatal("body should contain branded header")
	}
	if !strings.Contains(body, "abc123") {
		t.Fatal("body should contain the token link")
	}
	if !strings.Contains(body, "15 minutes") {
		t.Fatal("body should mention expiry")
	}
}

func TestAllEmailsHaveFooter(t *testing.T) {
	_, body := MagicLinkEmail("https://example.com/link")
	if !strings.Contains(body, "A phone for real conversations") {
		t.Fatal("email missing footer tagline")
	}
}
