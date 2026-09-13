//go:build integration

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/justinlindh/digits/server/internal/email/emailtest"
)

type fakeInviteChecker struct{ pending map[string]bool }

func (f fakeInviteChecker) HasPendingInvite(_ context.Context, addr string) (bool, error) {
	return f.pending[addr], nil
}

func newGateHandlers(t *testing.T) (*Handlers, *Store, *emailtest.Sender, *fakeMetrics) {
	t.Helper()
	s := testDB(t)
	sender := emailtest.NewSender()
	m := &fakeMetrics{}
	google := NewGoogleAuth("", "", "", "", s, m)
	h := NewHandlers(s, google, sender, "http://localhost", "", minimalTemplate(t), false, m)
	return h, s, sender, m
}

func postMagicRequest(h *Handlers, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/magic", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMagicLinkRequest(w, req)
	return w
}

func wantCheckYourEmail(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "success=check+your+email") {
		t.Errorf("got %d %q, want the same check-your-email redirect as a real send", w.Code, w.Header().Get("Location"))
	}
}

// An address with no account and no invite gets the same response as a real
// send, and nothing is mailed. This is what keeps harvested lists from turning
// the login form into a way to email strangers.
func TestMagicLinkRequest_UnknownAddress_SendsNothing(t *testing.T) {
	h, _, sender, m := newGateHandlers(t)

	wantCheckYourEmail(t, postMagicRequest(h, url.Values{"email": {"stranger@example.com"}}))
	if len(sender.Sent) != 0 {
		t.Errorf("sent %d emails to an unknown address, want 0", len(sender.Sent))
	}
	if len(m.magicLinks) != 1 || m.magicLinks[0] != "suppressed" {
		t.Errorf("magic link events = %v, want [suppressed]", m.magicLinks)
	}
}

func TestMagicLinkRequest_ExistingAccount_Sends(t *testing.T) {
	h, s, sender, m := newGateHandlers(t)
	if _, err := s.CreateUser(context.Background(), "member@example.com", "", nil); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Case and whitespace on the form must not dodge the lookup.
	wantCheckYourEmail(t, postMagicRequest(h, url.Values{"email": {"  Member@Example.com "}}))
	if len(sender.Sent) != 1 {
		t.Fatalf("sent %d emails to an existing account, want 1", len(sender.Sent))
	}
	if sender.Sent[0].To != "member@example.com" {
		t.Errorf("sent to %q, want the normalized address", sender.Sent[0].To)
	}
	if len(m.magicLinks) != 1 || m.magicLinks[0] != "issued" {
		t.Errorf("magic link events = %v, want [issued]", m.magicLinks)
	}
}

func TestMagicLinkRequest_PendingInvite_Sends(t *testing.T) {
	h, _, sender, _ := newGateHandlers(t)
	h.SetInviteChecker(fakeInviteChecker{pending: map[string]bool{"invited@example.com": true}})

	wantCheckYourEmail(t, postMagicRequest(h, url.Values{"email": {"invited@example.com"}}))
	if len(sender.Sent) != 1 {
		t.Errorf("sent %d emails to an invited address, want 1", len(sender.Sent))
	}
}

// Addresses on the deployment's admin allowlist can always request a link,
// even with no account yet: that is how a fresh install gets its first user
// without Google configured.
func TestMagicLinkRequest_BootstrapAddress_Sends(t *testing.T) {
	h, _, sender, m := newGateHandlers(t)
	h.SetAlwaysAllowed([]string{"operator@example.com"})

	wantCheckYourEmail(t, postMagicRequest(h, url.Values{"email": {"Operator@Example.com"}}))
	if len(sender.Sent) != 1 {
		t.Errorf("sent %d emails to the bootstrap address, want 1", len(sender.Sent))
	}
	if len(m.magicLinks) != 1 || m.magicLinks[0] != "issued" {
		t.Errorf("magic link events = %v, want [issued]", m.magicLinks)
	}
}

func TestMagicLinkRequest_Honeypot_SendsNothing(t *testing.T) {
	h, s, sender, m := newGateHandlers(t)
	if _, err := s.CreateUser(context.Background(), "real@example.com", "", nil); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	wantCheckYourEmail(t, postMagicRequest(h, url.Values{"email": {"real@example.com"}, "website": {"http://spam.example"}}))
	if len(sender.Sent) != 0 {
		t.Errorf("sent %d emails on a honeypot submission, want 0", len(sender.Sent))
	}
	if len(m.magicLinks) != 1 || m.magicLinks[0] != "suppressed" {
		t.Errorf("magic link events = %v, want [suppressed]", m.magicLinks)
	}
}

// At most three links per address per hour, so a real inbox cannot be
// flooded with sign-in mail by anyone who knows the address.
func TestMagicLinkRequest_PerAddressCap(t *testing.T) {
	h, s, sender, m := newGateHandlers(t)
	if _, err := s.CreateUser(context.Background(), "capped@example.com", "", nil); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	for i := 0; i < 4; i++ {
		wantCheckYourEmail(t, postMagicRequest(h, url.Values{"email": {"capped@example.com"}}))
	}
	if len(sender.Sent) != 3 {
		t.Errorf("sent %d emails in four requests, want 3", len(sender.Sent))
	}
	if len(m.magicLinks) != 4 || m.magicLinks[3] != "suppressed" {
		t.Errorf("magic link events = %v, want three issued then suppressed", m.magicLinks)
	}
}

func TestCreateMagicLink_CapIsPerHour(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := s.CreateMagicLink(ctx, "hourly@example.com", MagicLinkTTL, ""); err != nil {
			t.Fatalf("link %d: %v", i, err)
		}
	}
	if _, err := s.CreateMagicLink(ctx, "hourly@example.com", MagicLinkTTL, ""); !errors.Is(err, ErrMagicLinkRateLimited) {
		t.Fatalf("fourth link: err = %v, want ErrMagicLinkRateLimited", err)
	}
	// A consumed link is not a flood: marking one used frees a slot.
	if _, err := s.db.ExecContext(ctx, `UPDATE magic_links SET used = TRUE WHERE email = $1 AND token_hash = (SELECT token_hash FROM magic_links WHERE email = $1 LIMIT 1)`, "hourly@example.com"); err != nil {
		t.Fatalf("consume one link: %v", err)
	}
	if _, err := s.CreateMagicLink(ctx, "hourly@example.com", MagicLinkTTL, ""); err != nil {
		t.Fatalf("link after one consumed: %v", err)
	}
	if _, err := s.CreateMagicLink(ctx, "hourly@example.com", MagicLinkTTL, ""); !errors.Is(err, ErrMagicLinkRateLimited) {
		t.Fatalf("cap should be full again: err = %v, want ErrMagicLinkRateLimited", err)
	}
	// Age the earlier links out of the window; the cap releases.
	if _, err := s.db.ExecContext(ctx, `UPDATE magic_links SET created_at = created_at - interval '2 hours' WHERE email = $1`, "hourly@example.com"); err != nil {
		t.Fatalf("age links: %v", err)
	}
	if _, err := s.CreateMagicLink(ctx, "hourly@example.com", MagicLinkTTL, ""); err != nil {
		t.Errorf("link after window: %v", err)
	}
}
