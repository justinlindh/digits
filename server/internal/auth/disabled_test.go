//go:build integration

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSetDisabled_RevokesSessionsAndBlocksNewOnes(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "disabled@test.com", "Disabled User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.DisabledAt != nil {
		t.Fatalf("new user DisabledAt = %v, want nil", u.DisabledAt)
	}
	token, _, err := s.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.SetDisabled(ctx, u.ID, true); err != nil {
		t.Fatalf("SetDisabled(true): %v", err)
	}
	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.DisabledAt == nil {
		t.Error("DisabledAt = nil after disable, want timestamp")
	}
	if _, err := s.ValidateAndRefreshSession(ctx, token, time.Hour); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("existing session after disable: err = %v, want ErrInvalidSession", err)
	}
	if _, _, err := s.CreateSession(ctx, u.ID, time.Hour); !errors.Is(err, ErrAccountDisabled) {
		t.Errorf("CreateSession for disabled user: err = %v, want ErrAccountDisabled", err)
	}

	if err := s.SetDisabled(ctx, u.ID, false); err != nil {
		t.Fatalf("SetDisabled(false): %v", err)
	}
	got, err = s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.DisabledAt != nil {
		t.Errorf("DisabledAt = %v after enable, want nil", got.DisabledAt)
	}
	if _, _, err := s.CreateSession(ctx, u.ID, time.Hour); err != nil {
		t.Errorf("CreateSession after enable: %v", err)
	}
}

func TestSetDisabled_UnknownUser(t *testing.T) {
	s := testDB(t)
	err := s.SetDisabled(context.Background(), "00000000-0000-0000-0000-000000000000", true)
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("err = %v, want ErrUserNotFound", err)
	}
}

// A session that outlives the disable (raced past the delete, or minted on a
// replica) must still be refused by the middleware.
func TestRequireAuth_DisabledUser(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "disabled-mw@test.com", "Disabled MW", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	token, _, err := s.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET disabled_at = NOW() WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("flag disabled: %v", err)
	}

	handler := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for a disabled user")
	}))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/auth/login" {
		t.Errorf("got %d %q, want 303 to /auth/login", w.Code, w.Header().Get("Location"))
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("expected the session cookie to be cleared")
	}
}
