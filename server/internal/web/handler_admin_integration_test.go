//go:build integration

package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
)

// setupAdminHandler is setupHandler with an admin allowlist applied.
func setupAdminHandler(t *testing.T, allow ...string) (*Handler, *db.Database, *auth.Store) {
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
	deps, authStore := testDeps(t, database)
	h, err := NewHandler(deps, HandlerConfig{AdminEmails: allow})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h, database, authStore
}

func getAdmin(h *Handler, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

func TestAdminPage_Unauthenticated_RedirectsToLogin(t *testing.T) {
	h, _, _ := setupAdminHandler(t, "test@example.com")
	w := getAdmin(h, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/auth/login" {
		t.Errorf("got %d %q, want 303 to /auth/login", w.Code, w.Header().Get("Location"))
	}
}

func TestAdminPage_NonAdmin_NotFound(t *testing.T) {
	h, database, authStore := setupAdminHandler(t, "someone-else@example.com")
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	if w := getAdmin(h, cookie); w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

func TestAdminPage_Unconfigured_NotFound(t *testing.T) {
	h, database, authStore := setupAdminHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	if w := getAdmin(h, cookie); w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

func TestAdminPage_Admin_RendersOverview(t *testing.T) {
	h, database, authStore := setupAdminHandler(t, "test@example.com")
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	ln, _ := setupLineWithConn(t, h, database, hh, "7000001", "Admin Test Line")
	h.hub.UpdateDeviceInfo(ln.Number, signaling.DeviceInfoParams{PiVersion: "0.6.0", FirmwareVersion: "1.3.0"})
	h.SetReleases(updates.FakeReleaseIndex())

	w := getAdmin(h, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// Reported device versions, the fixture's latest releases, and the line
	// name all render; the device is behind on both components.
	for _, want := range []string{"test@example.com", hh.Name, line.FormatNumber(ln.Number), ln.Name, "0.6.0", "1.3.0", "0.7.0", "1.4.0", "behind"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// A registered hub connection shows the line as online.
	if !strings.Contains(body, "1 online") {
		t.Errorf("body missing online count for the registered line")
	}
}
