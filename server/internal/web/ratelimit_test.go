//go:build integration

package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/justinlindh/digits/server/internal/db"
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

	deps, _ := testDeps(t, database)
	h, err := NewHandler(deps, HandlerConfig{})
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
