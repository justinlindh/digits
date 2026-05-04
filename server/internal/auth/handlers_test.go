//go:build integration

package auth

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/email"
)

// minimalTemplate builds a trivial template that satisfies ExecuteTemplate("layout-v2.html", data).
func minimalTemplate(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := template.New("layout-v2.html").Parse(`{{.Page}} google={{.GoogleEnabled}} error={{.Error}} success={{.Success}}`)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	return tmpl
}

func newTestHandlers(t *testing.T) (*Handlers, *Store, *email.NoopSender) {
	t.Helper()
	s := testDB(t)
	sender := email.NewNoopSender()
	google := NewGoogleAuth("", "", "", "", s)
	tmpl := minimalTemplate(t)
	h := NewHandlers(s, google, sender, "http://localhost", "", tmpl, false)
	return h, s, sender
}

func TestHandleLoginPage(t *testing.T) {
	h, _, _ := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/login?error=bad+thing&success=good+thing", nil)
	w := httptest.NewRecorder()
	h.HandleLoginPage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "login") {
		t.Errorf("expected Page=login in body, got: %s", body)
	}
	if !strings.Contains(body, "error=bad thing") {
		t.Errorf("expected error param in body, got: %s", body)
	}
	if !strings.Contains(body, "success=good thing") {
		t.Errorf("expected success param in body, got: %s", body)
	}
}

func TestHandleLoginPage_GoogleEnabled(t *testing.T) {
	s := testDB(t)
	sender := email.NewNoopSender()
	google := NewGoogleAuth("client-id", "client-secret", "http://localhost/callback", "", s)
	tmpl := minimalTemplate(t)
	h := NewHandlers(s, google, sender, "http://localhost", "", tmpl, false)

	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	w := httptest.NewRecorder()
	h.HandleLoginPage(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "google=true") {
		t.Errorf("expected GoogleEnabled=true in body, got: %s", body)
	}
}

func TestHandleMagicLinkRequest_EmptyEmail(t *testing.T) {
	h, _, _ := newTestHandlers(t)

	form := url.Values{"email": {""}}
	req := httptest.NewRequest(http.MethodPost, "/auth/magic", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMagicLinkRequest(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=email+required") {
		t.Errorf("redirect location = %q, expected email required error", loc)
	}
}

func TestHandleMagicLinkRequest_Success(t *testing.T) {
	h, _, sender := newTestHandlers(t)

	form := url.Values{"email": {"user@example.com"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/magic", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleMagicLinkRequest(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "success=check+your+email") {
		t.Errorf("redirect location = %q, expected success message", loc)
	}
	if len(sender.Sent) != 1 {
		t.Fatalf("expected 1 email sent, got %d", len(sender.Sent))
	}
	if sender.Sent[0].To != "user@example.com" {
		t.Errorf("email sent to %q, want user@example.com", sender.Sent[0].To)
	}
	if !strings.Contains(sender.Sent[0].Body, "http://localhost/auth/magic/") {
		t.Errorf("email body missing magic link URL: %s", sender.Sent[0].Body)
	}
}

func TestHandleMagicLinkVerify_InvalidToken(t *testing.T) {
	h, _, _ := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/magic/bad-token", nil)
	req.SetPathValue("token", "bad-token")
	w := httptest.NewRecorder()
	h.HandleMagicLinkVerify(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=invalid+or+expired+link") {
		t.Errorf("redirect location = %q, expected invalid link error", loc)
	}
}

func TestHandleMagicLinkVerify_ValidToken_NewUser(t *testing.T) {
	h, s, _ := newTestHandlers(t)

	token, err := s.CreateMagicLink(context.Background(), "newuser@test.com", 15*time.Minute, "")
	if err != nil {
		t.Fatalf("CreateMagicLink: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/magic/"+token, nil)
	req.SetPathValue("token", token)
	w := httptest.NewRecorder()
	h.HandleMagicLinkVerify(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect location = %q, want /", loc)
	}

	// Should have set a session cookie
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == CookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie to be set")
	}
	if sessionCookie.Value == "" {
		t.Error("session cookie value should not be empty")
	}

	// User should exist now
	user, err := s.GetUserByEmail(context.Background(), "newuser@test.com")
	if err != nil {
		t.Fatalf("expected user to be created, got: %v", err)
	}
	if user.LastLoginAt == nil {
		t.Error("expected last_login_at to be set")
	}
}

func TestHandleMagicLinkVerify_ValidToken_ExistingUser(t *testing.T) {
	h, s, _ := newTestHandlers(t)

	_, err := s.CreateUser(context.Background(), "existing@test.com", "Existing", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	token, err := s.CreateMagicLink(context.Background(), "existing@test.com", 15*time.Minute, "")
	if err != nil {
		t.Fatalf("CreateMagicLink: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/magic/"+token, nil)
	req.SetPathValue("token", token)
	w := httptest.NewRecorder()
	h.HandleMagicLinkVerify(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect location = %q, want /", loc)
	}
}

func TestHandleLogout_WithSession(t *testing.T) {
	h, s, _ := newTestHandlers(t)

	user, err := s.CreateUser(context.Background(), "logout@test.com", "Logout User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	token, _, err := s.CreateSession(context.Background(), user.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	w := httptest.NewRecorder()
	h.HandleLogout(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/auth/login" {
		t.Errorf("redirect location = %q, want /auth/login", loc)
	}

	// Cookie should be cleared
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == CookieName && c.MaxAge < 0 {
			found = true
		}
	}
	if !found {
		t.Error("expected session cookie to be cleared")
	}

	// Session should be deleted from DB
	_, err = s.ValidateSession(context.Background(), token)
	if err == nil {
		t.Error("session should be invalid after logout")
	}
}

func newTestHandlersDevMode(t *testing.T) (*Handlers, *Store) {
	t.Helper()
	s := testDB(t)
	sender := email.NewNoopSender()
	google := NewGoogleAuth("", "", "", "", s)
	tmpl := minimalTemplate(t)
	h := NewHandlers(s, google, sender, "http://localhost", "", tmpl, true)
	return h, s
}

func TestHandleDevSession_DevModeEnabled(t *testing.T) {
	h, s := newTestHandlersDevMode(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/dev-session?email=test@example.com", nil)
	w := httptest.NewRecorder()
	h.HandleDevSession(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect location = %q, want /", loc)
	}

	// Should have set a session cookie
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == CookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie to be set")
	}
	if sessionCookie.Value == "" {
		t.Error("session cookie value should not be empty")
	}

	// User should exist
	user, err := s.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("expected user to be created, got: %v", err)
	}
	if user.LastLoginAt == nil {
		t.Error("expected last_login_at to be set")
	}
}

func TestHandleDevSession_DevModeDisabled(t *testing.T) {
	h, _, _ := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/dev-session?email=test@example.com", nil)
	w := httptest.NewRecorder()
	h.HandleDevSession(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleDevSession_DefaultEmail(t *testing.T) {
	h, s := newTestHandlersDevMode(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/dev-session", nil)
	w := httptest.NewRecorder()
	h.HandleDevSession(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}

	// Should have created user with default email
	_, err := s.GetUserByEmail(context.Background(), "e2e@example.com")
	if err != nil {
		t.Fatalf("expected user with default email to be created, got: %v", err)
	}
}

func TestHandleLogout_NoCookie(t *testing.T) {
	h, _, _ := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	w := httptest.NewRecorder()
	h.HandleLogout(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/auth/login" {
		t.Errorf("redirect location = %q, want /auth/login", loc)
	}
}

func TestHandleMagicLinkVerify_DialupThemeRedirectsToConnecting(t *testing.T) {
	h, s, _ := newTestHandlers(t)

	// Create a user with the dialup theme set.
	u, err := s.CreateUser(context.Background(), "dialup@test.com", "Dialup User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := s.SetTheme(context.Background(), u.ID, ThemeDialup); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}

	token, err := s.CreateMagicLink(context.Background(), "dialup@test.com", 15*time.Minute, "")
	if err != nil {
		t.Fatalf("CreateMagicLink: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/magic/"+token, nil)
	req.SetPathValue("token", token)
	w := httptest.NewRecorder()
	h.HandleMagicLinkVerify(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/connecting" {
		t.Errorf("redirect location = %q, want /connecting", loc)
	}
}
