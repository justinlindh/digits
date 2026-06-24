package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests exercise the OAuth state/CSRF guards and the login redirect.
// None of these code paths touch the Store, so they run in the fast unit tier
// with a nil store rather than requiring Postgres. Keeping them out of the
// integration tier means the CSRF protection is verified on every push, not
// only in the integration job.

func TestGoogleAuth_HandleLogin_SetsStateCookie(t *testing.T) {
	g := NewGoogleAuth("test-client-id", "test-secret", "http://localhost/callback", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/login", nil)
	w := httptest.NewRecorder()
	g.HandleLogin(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Errorf("expected 307, got %d", w.Code)
	}

	// Should set an oauth_state cookie
	cookies := w.Result().Cookies()
	var stateCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "oauth_state" {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("expected oauth_state cookie to be set")
	}
	if stateCookie.Value == "" {
		t.Error("oauth_state cookie should not be empty")
	}
	if !stateCookie.HttpOnly {
		t.Error("oauth_state cookie should be HttpOnly")
	}

	// Redirect URL should point to Google
	loc := w.Header().Get("Location")
	if loc == "" {
		t.Error("expected redirect Location header")
	}
}

func TestGoogleAuth_HandleCallback_MissingStateCookie(t *testing.T) {
	g := NewGoogleAuth("test-client-id", "test-secret", "http://localhost/callback", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=abc&code=xyz", nil)
	w := httptest.NewRecorder()
	g.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGoogleAuth_HandleCallback_StateMismatch(t *testing.T) {
	g := NewGoogleAuth("test-client-id", "test-secret", "http://localhost/callback", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=abc&code=xyz", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "different-state"})
	w := httptest.NewRecorder()
	g.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
