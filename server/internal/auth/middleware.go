package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

type contextKey string

const UserContextKey contextKey = "user"

const (
	CookieName = "digits_session"
	SessionTTL = 30 * 24 * time.Hour // 30 days
)

// RequireAuth middleware redirects to /auth/login if no valid session.
func (s *Store) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		sess, err := s.ValidateSession(cookie.Value)
		if err != nil {
			// Clear invalid cookie
			http.SetCookie(w, &http.Cookie{
				Name:   CookieName,
				Domain: s.CookieDomain,
				MaxAge: -1,
				Path:   "/",
			})
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		// Refresh session TTL on activity
		if err := s.RefreshSession(cookie.Value, SessionTTL); err != nil {
			slog.Warn("auth: failed to refresh session", "error", err)
		}

		user, err := s.GetUserByID(sess.UserID)
		if err != nil {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), UserContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserFromContext extracts the authenticated user from context.
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(UserContextKey).(*User)
	return u
}
