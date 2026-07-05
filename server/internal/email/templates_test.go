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
	if !strings.Contains(body, "a phone for real conversations") {
		t.Fatal("email missing footer tagline")
	}
}

func TestHouseholdInviteEmail(t *testing.T) {
	subject, body := HouseholdInviteEmail("Lindh Family", "Justin", "https://app.digits.family/invite/abc123")

	if !strings.Contains(subject, "Lindh Family") {
		t.Errorf("subject should contain household name, got: %s", subject)
	}
	if !strings.Contains(body, "Justin") {
		t.Error("body should contain inviter name")
	}
	if !strings.Contains(body, "Lindh Family") {
		t.Error("body should contain household name")
	}
	if !strings.Contains(body, "https://app.digits.family/invite/abc123") {
		t.Error("body should contain invite link")
	}
	if !strings.Contains(body, "Accept Invite") {
		t.Error("body should contain CTA button text")
	}
	if !strings.Contains(body, "7 days") {
		t.Error("body should mention 7-day expiry")
	}
}
