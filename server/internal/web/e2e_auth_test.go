package web

import (
	"database/sql"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/email/emailtest"
	"github.com/justinlindh/digits/server/internal/signaling"
)

// setupAuthTestServer creates a test HTTP server with auth enabled.
// It uses a lazily-opened (not pinged) sql.DB so no real Postgres is needed
// for tests that don't perform any DB operations (e.g., redirect on no cookie,
// or rendering the login page).
func setupAuthTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	// Open a sql.DB lazily: no Ping, so no real connection is required.
	// Tests that don't trigger DB operations (no-cookie redirect, login page render)
	// work fine without a live database.
	rawDB, err := sql.Open("postgres", "postgres://test:test@localhost/testdb?sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })

	authStore := auth.NewStore(rawDB)

	// Google OAuth disabled (empty credentials)
	googleAuth := auth.NewGoogleAuth("", "", "", "", authStore)

	// Noop email sender
	emailSender := emailtest.NewSender()

	// Parse login template from the embedded FS
	loginTmpl, err := template.New("").Funcs(TemplateFuncs()).ParseFS(TemplateFS(), "templates/layout-v2.html", "templates/_partials.html", "templates/login.html")
	if err != nil {
		t.Fatalf("parse login template: %v", err)
	}

	authHandlers := auth.NewHandlers(authStore, googleAuth, emailSender, "http://localhost", "", loginTmpl, false)

	hub := signaling.NewHub()
	tracker := calls.New(nil) // nil DB: no DB calls expected in these tests
	relay := signaling.NewRelay(hub, tracker, nil, nil)

	h, err := NewHandler(Deps{
		Hub:          hub,
		Tracker:      tracker,
		Relay:        relay,
		AuthStore:    authStore,
		AuthHandlers: authHandlers,
		GoogleAuth:   googleAuth,
	}, HandlerConfig{})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)
	return srv
}

// TestAuthRedirectsToLogin verifies that GET / without a session cookie
// redirects to /auth/login with a 302 or 303 status.
func TestAuthRedirectsToLogin(t *testing.T) {
	srv := setupAuthTestServer(t)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // do not follow redirects
		},
	}

	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 302 or 303 redirect, got %d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/auth/login") {
		t.Errorf("expected redirect to /auth/login, got Location: %s", loc)
	}
}

// TestLoginPageReturns200 verifies that GET /auth/login returns 200
// with the login form HTML content.
func TestLoginPageReturns200(t *testing.T) {
	srv := setupAuthTestServer(t)

	resp, err := http.Get(srv.URL + "/auth/login")
	if err != nil {
		t.Fatalf("GET /auth/login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var bodyBuf strings.Builder
	if _, err := io.Copy(&bodyBuf, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := bodyBuf.String()

	// Verify login form is present
	if !strings.Contains(body, "Sign in") && !strings.Contains(body, "login") && !strings.Contains(body, "email") {
		t.Errorf("login page body does not appear to contain login form content; body snippet: %.200s", body)
	}
}
