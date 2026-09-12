package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/justinlindh/digits/server/internal/auth"
)

func TestRequireAdmin(t *testing.T) {
	cases := []struct {
		name     string
		allow    []string
		user     *auth.User
		wantCode int
	}{
		{"no allowlist configured", nil, &auth.User{Email: "admin@example.com"}, http.StatusNotFound},
		{"no user in context", []string{"admin@example.com"}, nil, http.StatusNotFound},
		{"user not on allowlist", []string{"admin@example.com"}, &auth.User{Email: "someone@example.com"}, http.StatusNotFound},
		{"user on allowlist", []string{"admin@example.com"}, &auth.User{Email: "admin@example.com"}, http.StatusOK},
		{"email match is case-insensitive", []string{"admin@example.com"}, &auth.User{Email: "Admin@Example.COM"}, http.StatusOK},
		{"second entry matches", []string{"first@example.com", "admin@example.com"}, &auth.User{Email: "admin@example.com"}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewHandler(Deps{}, HandlerConfig{AdminEmails: tc.allow})
			if err != nil {
				t.Fatalf("NewHandler: %v", err)
			}
			called := false
			gated := h.requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			if tc.user != nil {
				req = req.WithContext(auth.ContextWithUser(req.Context(), tc.user))
			}
			w := httptest.NewRecorder()
			gated.ServeHTTP(w, req)
			if w.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tc.wantCode)
			}
			if called != (tc.wantCode == http.StatusOK) {
				t.Errorf("next called = %v, want %v", called, tc.wantCode == http.StatusOK)
			}
		})
	}
}
