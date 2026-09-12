//go:build integration

package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/db"
)

// createSecondUser seeds a distinct, onboarded user with a household and a
// live session so tests can act on an account other than the admin's.
func createSecondUser(t *testing.T, h *Handler, database *db.Database, authStore *auth.Store) (*auth.User, *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	u, err := authStore.CreateUser(ctx, "second@example.com", "Second User", nil)
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}
	t.Cleanup(func() { _ = authStore.DeleteUser(ctx, u.ID) })
	markUserOnboarded(t, authStore, u.ID)
	hh, err := h.householdStore.Create(ctx, "Second Household", u.ID)
	if err != nil {
		t.Fatalf("create second household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})
	token, _, err := authStore.CreateSession(ctx, u.ID, auth.SessionTTL)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	return u, &http.Cookie{Name: auth.CookieName, Value: token}
}

func postAdminAccount(h *Handler, cookie *http.Cookie, userID, action string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/admin/accounts/"+userID+"/"+action, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

func getPath(h *Handler, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

func TestAdminDisable_NonAdmin_NotFound(t *testing.T) {
	h, database, authStore := setupAdminHandler(t, "someone-else@example.com")
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	target, _ := createSecondUser(t, h, database, authStore)
	if w := postAdminAccount(h, cookie, target.ID, "disable"); w.Code != http.StatusNotFound {
		t.Errorf("non-admin disable: got %d, want 404", w.Code)
	}
	if w := postAdminAccount(h, cookie, target.ID, "enable"); w.Code != http.StatusNotFound {
		t.Errorf("non-admin enable: got %d, want 404", w.Code)
	}
	if w := postAdminAccount(h, nil, target.ID, "disable"); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/auth/login" {
		t.Errorf("unauthenticated disable: got %d %q, want 303 to /auth/login", w.Code, w.Header().Get("Location"))
	}
	u, err := authStore.GetUserByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if u.DisabledAt != nil {
		t.Error("target was disabled by a non-admin request")
	}
}

func TestAdminDisable_ThenEnable(t *testing.T) {
	h, database, authStore := setupAdminHandler(t, "test@example.com")
	adminCookie, _ := setupAuthedHousehold(t, h, database, authStore)
	target, targetCookie := createSecondUser(t, h, database, authStore)

	// Before: the target can use the app.
	if w := getPath(h, targetCookie, "/settings"); w.Code != http.StatusOK {
		t.Fatalf("target /settings before disable: got %d, want 200", w.Code)
	}

	w := postAdminAccount(h, adminCookie, target.ID, "disable")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin" {
		t.Fatalf("disable: got %d %q, want 303 to /admin", w.Code, w.Header().Get("Location"))
	}

	// After: the existing session is gone and a new login is refused.
	if w := getPath(h, targetCookie, "/settings"); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/auth/login" {
		t.Errorf("target /settings after disable: got %d %q, want 303 to /auth/login", w.Code, w.Header().Get("Location"))
	}
	if _, _, err := authStore.CreateSession(context.Background(), target.ID, auth.SessionTTL); !errors.Is(err, auth.ErrAccountDisabled) {
		t.Errorf("new session for disabled user: err = %v, want ErrAccountDisabled", err)
	}
	// The admin page reflects the state.
	body := getPath(h, adminCookie, "/admin").Body.String()
	if !strings.Contains(body, "disabled") || !strings.Contains(body, "/admin/accounts/"+target.ID+"/enable") {
		t.Error("admin page missing disabled chip or enable action for the target")
	}

	w = postAdminAccount(h, adminCookie, target.ID, "enable")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin" {
		t.Fatalf("enable: got %d %q, want 303 to /admin", w.Code, w.Header().Get("Location"))
	}
	if _, _, err := authStore.CreateSession(context.Background(), target.ID, auth.SessionTTL); err != nil {
		t.Errorf("new session after enable: %v", err)
	}
}

func TestAdminDisable_SelfIsRefused(t *testing.T) {
	h, database, authStore := setupAdminHandler(t, "test@example.com")
	adminCookie, _ := setupAuthedHousehold(t, h, database, authStore)
	admin, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if w := postAdminAccount(h, adminCookie, admin.ID, "disable"); w.Code != http.StatusBadRequest {
		t.Errorf("self-disable: got %d, want 400", w.Code)
	}
	if w := getPath(h, adminCookie, "/admin"); w.Code != http.StatusOK {
		t.Errorf("admin still signed in: got %d, want 200", w.Code)
	}
}

func TestAdminDisable_UnknownUser_NotFound(t *testing.T) {
	h, database, authStore := setupAdminHandler(t, "test@example.com")
	adminCookie, _ := setupAuthedHousehold(t, h, database, authStore)
	if w := postAdminAccount(h, adminCookie, "00000000-0000-0000-0000-000000000000", "disable"); w.Code != http.StatusNotFound {
		t.Errorf("unknown user: got %d, want 404", w.Code)
	}
}

func TestAdminNavLink_OnlyForAdmins(t *testing.T) {
	h, database, authStore := setupAdminHandler(t, "test@example.com")
	adminCookie, _ := setupAuthedHousehold(t, h, database, authStore)
	_, otherCookie := createSecondUser(t, h, database, authStore)

	if body := getPath(h, adminCookie, "/settings").Body.String(); !strings.Contains(body, `href="/admin"`) {
		t.Error("admin's /settings is missing the /admin nav link")
	}
	if body := getPath(h, otherCookie, "/settings").Body.String(); strings.Contains(body, `href="/admin"`) {
		t.Error("non-admin's /settings shows the /admin nav link")
	}
}
