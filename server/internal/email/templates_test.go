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

func TestHouseholdInviteEmailContainsBranding(t *testing.T) {
	_, body := HouseholdInviteEmail("Smith Family", "ABC123", "https://app.digits.family")
	if !strings.Contains(body, "DIGITS") {
		t.Fatal("body should contain branded header")
	}
	if !strings.Contains(body, "Smith Family") {
		t.Fatal("body should mention household name")
	}
	if !strings.Contains(body, "ABC123") {
		t.Fatal("body should contain invite code")
	}
}

func TestAllEmailsHaveFooter(t *testing.T) {
	_, body1 := MagicLinkEmail("https://example.com/link")
	_, body2 := HouseholdInviteEmail("Test", "CODE", "https://example.com")

	for i, body := range []string{body1, body2} {
		if !strings.Contains(body, "Digits — A phone for real conversations") || !strings.Contains(body, "Digits —") {
			// Check for the key phrase, allowing for HTML entity encoding of em dash
			if !strings.Contains(body, "A phone for real conversations") {
				t.Fatalf("email %d missing footer tagline", i+1)
			}
		}
	}
}
