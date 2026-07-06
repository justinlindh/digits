package auth

import (
	"context"
	"net/http"
	"time"
)

type contextKey string

const userContextKey contextKey = "user"

const (
	CookieName   = "digits_session"
	SessionTTL   = 30 * 24 * time.Hour // 30 days
	MagicLinkTTL = 15 * time.Minute
)

// RequireAuth middleware redirects to /auth/login if no valid session.
func (s *Store) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		sess, err := s.ValidateAndRefreshSession(r.Context(), cookie.Value, SessionTTL)
		if err != nil {
			clearSessionCookie(w, s.CookieDomain)
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}

		user, err := s.GetUserByID(r.Context(), sess.UserID)
		if err != nil {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		ctx := ContextWithUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ContextWithUser returns a copy of ctx carrying the authenticated user.
func ContextWithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// UserFromContext extracts the authenticated user from context.
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(userContextKey).(*User)
	return u
}

// setSessionCookie writes the session cookie that authenticates subsequent
// requests. secure controls the Secure attribute: the production login paths
// pass true, while the dev-session path derives it from the request scheme so
// plain HTTP localhost still works.
func setSessionCookie(w http.ResponseWriter, domain, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Domain:   domain,
		Path:     "/",
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie emits expiring Set-Cookie headers for both the
// domain-scoped cookie (current config) and the host-scoped cookie (no Domain
// attribute). Early sessions set without an explicit Domain stick around under
// the origin host and are invisible to a domain-scoped clear, so after a
// COOKIE_DOMAIN change they can persist and keep failing validation forever.
// Writing both variants mops up either form.
func clearSessionCookie(w http.ResponseWriter, domain string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Domain:   domain,
		MaxAge:   -1,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
	})
	if domain != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			MaxAge:   -1,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
		})
	}
}
