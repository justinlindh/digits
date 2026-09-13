//go:build integration

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/justinlindh/digits/server/internal/auth"
)

// Through the real router and templates: opening the emailed link is inert,
// and only the form submission on that page signs the user in.
func TestMagicLink_GetIsInertAndPostSignsIn(t *testing.T) {
	h, _, authStore := setupHandler(t)
	ctx := context.Background()
	token, err := authStore.CreateMagicLink(ctx, "scanner-victim@example.com", auth.MagicLinkTTL, "")
	if err != nil {
		t.Fatalf("CreateMagicLink: %v", err)
	}
	t.Cleanup(func() {
		if u, err := authStore.GetUserByEmail(ctx, "scanner-victim@example.com"); err == nil {
			_ = authStore.DeleteUser(ctx, u.ID)
		}
	})

	// A link scanner's fetch.
	req := httptest.NewRequest(http.MethodGet, "/auth/magic/"+token, nil)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET: got %d, want 200; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `action="/auth/magic/`+token+`"`) || !strings.Contains(body, `method="POST"`) {
		t.Errorf("GET body missing the POST form")
	}
	if !strings.Contains(body, "Sign in") {
		t.Errorf("GET body missing the sign-in button")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Errorf("GET set cookies, want none")
	}
	if _, err := authStore.GetUserByEmail(ctx, "scanner-victim@example.com"); err == nil {
		t.Fatal("GET created an account")
	}

	// The person pressing the button.
	req = httptest.NewRequest(http.MethodPost, "/auth/magic/"+token, nil)
	w = httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST: got %d, want 303; body: %s", w.Code, w.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName && c.Value != "" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("POST did not set a session cookie")
	}
	if _, err := authStore.GetUserByEmail(ctx, "scanner-victim@example.com"); err != nil {
		t.Errorf("POST did not create the account: %v", err)
	}

	// The link is single-use: a second POST is refused.
	req = httptest.NewRequest(http.MethodPost, "/auth/magic/"+token, nil)
	w = httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "invalid") {
		t.Errorf("second POST: got %d %q, want redirect with invalid-link error", w.Code, w.Header().Get("Location"))
	}
}
