//go:build integration

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestE2EAdminLoginAndDashboard(t *testing.T) {
	dsn := os.Getenv("TEST_ADMIN_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_ADMIN_DATABASE_URL not set")
	}

	// Start a mock stats server
	statsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin-Secret") != "e2e-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_users":5,"total_households":3,"total_phones":7,"online_phones":2,"active_calls":1,"total_links":2}`))
	}))
	defer statsSrv.Close()

	db, err := OpenAdmin(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	t.Cleanup(func() {
		_, _ = db.DB.Exec("DELETE FROM admin_sessions")
		_, _ = db.DB.Exec("DELETE FROM admin_users WHERE username = 'e2eadmin'")
	})

	authStore := NewAuthStore(db)
	hash, _ := HashSecret("e2epass")
	if _, err := authStore.CreateAdmin(context.Background(), "e2eadmin", hash); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Addr:        ":0",
		StatsURL:    statsSrv.URL,
		StatsSecret: "e2e-secret",
	}
	srv := NewServer(cfg, db, authStore)
	router := srv.router()

	// 1. Login page renders
	req := httptest.NewRequest("GET", "/admin/login", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login page: expected 200, got %d", w.Code)
	}

	// 2. Dashboard redirects without auth
	req = httptest.NewRequest("GET", "/admin/", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("dashboard no auth: expected 303, got %d", w.Code)
	}

	// 3. Login with valid credentials
	form := url.Values{"username": {"e2eadmin"}, "password": {"e2epass"}}
	req = httptest.NewRequest("POST", "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login post: expected 303 redirect, got %d", w.Code)
	}

	// Extract session cookie
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "admin_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no admin_session cookie after login")
	}

	// 4. Dashboard with auth shows stats
	req = httptest.NewRequest("GET", "/admin/", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard with auth: expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "System Overview") {
		t.Error("dashboard missing 'System Overview' heading")
	}

	// 5. Logout clears session
	req = httptest.NewRequest("POST", "/admin/logout", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("logout: expected 303, got %d", w.Code)
	}
}
