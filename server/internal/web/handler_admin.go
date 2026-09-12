package web

import (
	"net/http"
	"strings"

	"github.com/justinlindh/digits/server/internal/auth"
)

// requireAdmin gates a route behind the configured admin allowlist. It runs
// inside RequireAuth, so the session is already validated; this only checks
// that the signed-in email is on the list. Every failure is a 404 rather
// than a 403 so the page's existence is never advertised to non-admins.
func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		if user == nil || !isAdminEmail(h.cfg.AdminEmails, user.Email) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isAdminEmail reports whether email is on the allowlist, comparing
// case-insensitively. An empty allowlist matches nothing.
func isAdminEmail(allow []string, email string) bool {
	for _, a := range allow {
		if strings.EqualFold(a, email) {
			return true
		}
	}
	return false
}
