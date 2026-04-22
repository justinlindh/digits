package auth

import (
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/version"
)

// Handlers provides HTTP handlers for login, magic link, and logout flows.
type Handlers struct {
	store        *Store
	google       *GoogleAuth
	emailer      email.Sender
	baseURL      string
	cookieDomain string // optional, e.g. ".digits.family" for subdomain sharing
	loginTmpl    *template.Template
	devMode      bool
}

// NewHandlers creates auth HTTP handlers.
// cookieDomain sets the cookie Domain attribute (e.g. ".digits.family"); pass "" to omit it.
func NewHandlers(store *Store, google *GoogleAuth, emailer email.Sender, baseURL, cookieDomain string, loginTmpl *template.Template, devMode bool) *Handlers {
	return &Handlers{
		store:        store,
		google:       google,
		emailer:      emailer,
		baseURL:      baseURL,
		cookieDomain: cookieDomain,
		loginTmpl:    loginTmpl,
		devMode:      devMode,
	}
}

// HandleLoginPage renders the login form.
func (h *Handlers) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Page":          "login",
		"Version":       version.Version,
		"GoogleEnabled": h.google.Enabled(),
		"Error":         r.URL.Query().Get("error"),
		"Success":       r.URL.Query().Get("success"),
	}
	if err := h.loginTmpl.ExecuteTemplate(w, "layout-v2.html", data); err != nil {
		slog.Error("login template render failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// HandleMagicLinkRequest creates a magic link token and emails it.
func (h *Handlers) HandleMagicLinkRequest(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/auth/login?error=bad+request", http.StatusSeeOther)
		return
	}
	emailAddr := r.FormValue("email")
	if emailAddr == "" {
		http.Redirect(w, r, "/auth/login?error=email+required", http.StatusSeeOther)
		return
	}

	token, err := h.store.CreateMagicLink(emailAddr, 15*time.Minute)
	if err != nil {
		slog.Error("magic link creation failed", "err", err)
		http.Redirect(w, r, "/auth/login?error=try+again", http.StatusSeeOther)
		return
	}

	link := fmt.Sprintf("%s/auth/magic/%s", h.baseURL, token)

	if h.devMode {
		slog.Warn("dev magic link", "email", emailAddr, "link", link)
	}

	subject, body := email.MagicLinkEmail(link)
	if err := h.emailer.Send(emailAddr, subject, body); err != nil {
		slog.Error("magic link email failed", "err", err)
	}

	http.Redirect(w, r, "/auth/login?success=check+your+email", http.StatusSeeOther)
}

// HandleMagicLinkVerify validates a magic link token and creates a session.
func (h *Handlers) HandleMagicLinkVerify(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	emailAddr, err := h.store.ValidateMagicLink(token)
	if err != nil {
		http.Redirect(w, r, "/auth/login?error=invalid+or+expired+link", http.StatusSeeOther)
		return
	}

	// Find or create user
	user, err := h.store.GetUserByEmail(emailAddr)
	if errors.Is(err, ErrUserNotFound) {
		user, err = h.store.CreateUser(emailAddr, "", nil)
		if err != nil {
			http.Error(w, "failed to create user", http.StatusInternalServerError)
			return
		}
	} else if err != nil {
		slog.Error("magic link verify: lookup user", "err", err)
		http.Error(w, "failed to look up user", http.StatusInternalServerError)
		return
	}

	if err := h.store.UpdateLastLogin(user.ID); err != nil {
		slog.Error("failed to update last login", "user_id", user.ID, "err", err)
	}

	sessionToken, _, err := h.store.CreateSession(user.ID, SessionTTL)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sessionToken,
		Domain:   h.cookieDomain,
		Path:     "/",
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, loginRedirectFor(user), http.StatusSeeOther)
}

// HandleDevSession creates an authenticated session in one round-trip for e2e testing.
// Only works when dev mode is enabled; returns 404 otherwise.
func (h *Handlers) HandleDevSession(w http.ResponseWriter, r *http.Request) {
	if !h.devMode {
		http.NotFound(w, r)
		return
	}

	emailAddr := r.URL.Query().Get("email")
	if emailAddr == "" {
		emailAddr = "e2e@example.com"
	}

	slog.Info("dev-session requested", "email", emailAddr)

	// Find or create user (same pattern as HandleMagicLinkVerify)
	user, err := h.store.GetUserByEmail(emailAddr)
	if errors.Is(err, ErrUserNotFound) {
		user, err = h.store.CreateUser(emailAddr, "", nil)
		if err != nil {
			http.Error(w, "failed to create user", http.StatusInternalServerError)
			return
		}
	} else if err != nil {
		slog.Error("dev-session: lookup user", "err", err)
		http.Error(w, "failed to look up user", http.StatusInternalServerError)
		return
	}

	if err := h.store.UpdateLastLogin(user.ID); err != nil {
		slog.Error("failed to update last login", "user_id", user.ID, "err", err)
	}

	sessionToken, _, err := h.store.CreateSession(user.ID, SessionTTL)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	// Secure=false: this endpoint only exists in dev mode and CI runs over
	// plain HTTP. Chromium won't send Secure cookies over http://localhost.
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sessionToken,
		Domain:   h.cookieDomain,
		Path:     "/",
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, loginRedirectFor(user), http.StatusSeeOther)
}

// loginRedirectFor returns the URL to redirect a newly-authenticated user to.
// For dial-up theme users, the welcome=1 query param triggers the login
// greeting sound in layout-dialup.html; other themes get a plain "/".
func loginRedirectFor(u *User) string {
	if u != nil && u.Theme == ThemeDialup {
		return "/?welcome=1"
	}
	return "/"
}

// HandleLogout destroys the session and clears the cookie.
func (h *Handlers) HandleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(CookieName)
	if err == nil {
		if err := h.store.DeleteSession(cookie.Value); err != nil {
			slog.Error("failed to delete session", "err", err)
		}
	}
	clearSessionCookie(w, h.cookieDomain)
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}
