//go:build integration

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The GET half of a magic link must be inert: it renders a form that POSTs to
// the same URL and leaves the link unconsumed, so a mail security scanner
// fetching the URL cannot sign anyone in.
func TestHandleMagicLinkConfirm_RendersFormWithoutConsuming(t *testing.T) {
	h, s, _ := newTestHandlers(t)
	token, err := s.CreateMagicLink(context.Background(), "confirm@test.com", MagicLinkTTL, "")
	if err != nil {
		t.Fatalf("CreateMagicLink: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/magic/"+token, nil)
	req.SetPathValue("token", token)
	w := httptest.NewRecorder()
	h.HandleMagicLinkConfirm(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "action=\"/auth/magic/"+token+"\"") || !strings.Contains(body, "method=\"POST\"") {
		t.Errorf("body missing a POST form back to the same URL: %s", body)
	}
	if cookies := w.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("GET set cookies %v, want none", cookies)
	}
	if _, _, err := s.ValidateMagicLink(context.Background(), token); err != nil {
		t.Errorf("link was consumed by GET: %v", err)
	}
	if _, err := s.GetUserByEmail(context.Background(), "confirm@test.com"); err == nil {
		t.Error("GET created a user")
	}
}

// A bad token still renders the form rather than revealing anything; the POST
// is where validity is decided.
func TestHandleMagicLinkConfirm_UnknownTokenStillRendersForm(t *testing.T) {
	h, _, _ := newTestHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/auth/magic/nope", nil)
	req.SetPathValue("token", "nope")
	w := httptest.NewRecorder()
	h.HandleMagicLinkConfirm(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "action=\"/auth/magic/nope\"") {
		t.Errorf("got %d %q, want 200 with the form", w.Code, w.Body.String())
	}
}
