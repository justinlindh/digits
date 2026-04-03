package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginPageRenders(t *testing.T) {
	cfg := &Config{
		Addr:        ":0",
		StatsURL:    "http://localhost:9999/internal/stats",
		StatsSecret: "test-secret",
	}
	srv := NewServer(cfg, nil, nil)

	req := httptest.NewRequest("GET", "/admin/login", nil)
	w := httptest.NewRecorder()
	srv.router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestDashboardRequiresAuth(t *testing.T) {
	cfg := &Config{
		Addr:        ":0",
		StatsURL:    "http://localhost:9999/internal/stats",
		StatsSecret: "test-secret",
	}
	srv := NewServer(cfg, nil, nil)

	req := httptest.NewRequest("GET", "/admin/", nil)
	w := httptest.NewRecorder()
	srv.router().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/admin/login" {
		t.Fatalf("expected redirect to /admin/login, got %s", loc)
	}
}
