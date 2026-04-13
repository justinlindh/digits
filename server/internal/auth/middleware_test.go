//go:build integration

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequireAuth_MissingCookie(t *testing.T) {
	s := testDB(t)

	handler := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called without a cookie")
	}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/auth/login" {
		t.Errorf("redirect location = %q, want /auth/login", loc)
	}
}

func TestRequireAuth_InvalidSession(t *testing.T) {
	s := testDB(t)

	handler := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with an invalid session")
	}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "totally-bogus-token"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/auth/login" {
		t.Errorf("redirect location = %q, want /auth/login", loc)
	}

	// Should clear the invalid cookie
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == CookieName && c.MaxAge < 0 {
			found = true
		}
	}
	if !found {
		t.Error("expected invalid session cookie to be cleared")
	}
}

func TestRequireAuth_ValidSession(t *testing.T) {
	s := testDB(t)

	user, err := s.CreateUser("middleware@test.com", "MW User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	token, _, err := s.CreateSession(user.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var ctxUser *User
	handler := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ctxUser == nil {
		t.Fatal("expected user in context, got nil")
	}
	if ctxUser.ID != user.ID {
		t.Errorf("context user ID = %s, want %s", ctxUser.ID, user.ID)
	}
	if ctxUser.Email != "middleware@test.com" {
		t.Errorf("context user email = %s, want middleware@test.com", ctxUser.Email)
	}
}

