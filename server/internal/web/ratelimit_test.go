//go:build integration

package web

import (
	"net/http"
	"strings"
	"testing"

	"html/template"
	"net/http/httptest"
	"os"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
)

func setupRateLimitTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	lineStore := line.NewStore(database)
	deviceStore := device.NewStore(database)
	hub := signaling.NewHub()
	tracker := calls.New(database)
	relay := signaling.NewRelay(hub, tracker, nil, nil)

	authStore := auth.NewStoreFromDB(database.DB)
	googleAuth := auth.NewGoogleAuth("", "", "", "", authStore)
	emailSender := email.NewNoopSender()
	loginTmpl, err := template.New("").ParseFS(TemplateFS(), "templates/layout-v2.html", "templates/_partials.html", "templates/login.html")
	if err != nil {
		t.Fatalf("parse login template: %v", err)
	}
	authHandlers := auth.NewHandlers(authStore, googleAuth, emailSender, "http://localhost", "", loginTmpl, false)

	h, err := NewHandler(Deps{
		LineStore:    lineStore,
		DeviceStore:  deviceStore,
		Hub:          hub,
		Tracker:      tracker,
		Relay:        relay,
		AuthStore:    authStore,
		AuthHandlers: authHandlers,
		GoogleAuth:   googleAuth,
	}, HandlerConfig{Addr: ":0"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)
	return srv
}

func TestMagicLinkRateLimited(t *testing.T) {
	srv := setupRateLimitTestServer(t)

	client := &http.Client{
		// Don't follow redirects — we care about the immediate response code
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Send 5 requests — all should succeed (2xx or 3xx redirect, not 429)
	for i := 1; i <= 5; i++ {
		resp, err := client.Post(srv.URL+"/auth/magic", "application/x-www-form-urlencoded",
			strings.NewReader("email=test@example.com"))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("request %d: got 429, expected non-429 (within limit)", i)
		}
	}

	// 6th request should be rate-limited (429)
	resp, err := client.Post(srv.URL+"/auth/magic", "application/x-www-form-urlencoded",
		strings.NewReader("email=test@example.com"))
	if err != nil {
		t.Fatalf("6th request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th request: expected 429, got %d", resp.StatusCode)
	}
}
