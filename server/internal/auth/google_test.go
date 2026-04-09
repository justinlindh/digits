package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGoogleAuth_Enabled(t *testing.T) {
	s := testDB(t)

	tests := []struct {
		name     string
		clientID string
		secret   string
		want     bool
	}{
		{"both set", "id", "secret", true},
		{"empty id", "", "secret", false},
		{"empty secret", "id", "", false},
		{"both empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGoogleAuth(tt.clientID, tt.secret, "http://localhost/callback", "", s)
			if got := g.Enabled(); got != tt.want {
				t.Errorf("Enabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGoogleAuth_HandleLogin_SetsStateCookie(t *testing.T) {
	s := testDB(t)
	g := NewGoogleAuth("test-client-id", "test-secret", "http://localhost/callback", "", s)

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
	s := testDB(t)
	g := NewGoogleAuth("test-client-id", "test-secret", "http://localhost/callback", "", s)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=abc&code=xyz", nil)
	w := httptest.NewRecorder()
	g.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGoogleAuth_HandleCallback_StateMismatch(t *testing.T) {
	s := testDB(t)
	g := NewGoogleAuth("test-client-id", "test-secret", "http://localhost/callback", "", s)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=abc&code=xyz", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "different-state"})
	w := httptest.NewRecorder()
	g.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
