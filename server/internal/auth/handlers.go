package auth

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

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
		slog.ErrorContext(r.Context(), "login template render failed", "err", err)
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
	returnTo := r.FormValue("return_to")

	token, err := h.store.CreateMagicLink(r.Context(), emailAddr, MagicLinkTTL, returnTo)
	if err != nil {
		slog.ErrorContext(r.Context(), "magic link creation failed", "err", err)
		http.Redirect(w, r, "/auth/login?error=try+again", http.StatusSeeOther)
		return
	}

	link := fmt.Sprintf("%s/auth/magic/%s", h.baseURL, token)

	if h.devMode {
		slog.WarnContext(r.Context(), "dev magic link", "email", emailAddr, "link", link)
	}

	subject, body := email.MagicLinkEmail(link)
	if err := h.emailer.Send(emailAddr, subject, body); err != nil {
		slog.ErrorContext(r.Context(), "magic link email failed", "err", err)
	}

	http.Redirect(w, r, "/auth/login?success=check+your+email", http.StatusSeeOther)
}

// HandleMagicLinkVerify validates a magic link token and creates a session.
func (h *Handlers) HandleMagicLinkVerify(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	emailAddr, returnTo, err := h.store.ValidateMagicLink(r.Context(), token)
	if err != nil {
		http.Redirect(w, r, "/auth/login?error=invalid+or+expired+link", http.StatusSeeOther)
		return
	}

	user, _, err := h.store.GetOrCreateUserByEmail(r.Context(), emailAddr)
	if err != nil {
		slog.ErrorContext(r.Context(), "magic link verify: get or create user", "err", err)
		http.Error(w, "failed to look up user", http.StatusInternalServerError)
		return
	}

	if err := h.store.UpdateLastLogin(r.Context(), user.ID); err != nil {
		slog.ErrorContext(r.Context(), "failed to update last login", "user_id", user.ID, "err", err)
	}

	sessionToken, _, err := h.store.CreateSession(r.Context(), user.ID, SessionTTL)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	setSessionCookie(w, h.cookieDomain, sessionToken, true)

	http.Redirect(w, r, safeReturnTo(returnTo, user), http.StatusSeeOther)
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

	slog.InfoContext(r.Context(), "dev-session requested", "email", emailAddr)

	user, created, err := h.store.GetOrCreateUserByEmail(r.Context(), emailAddr)
	if err != nil {
		slog.ErrorContext(r.Context(), "dev-session: get or create user", "err", err)
		http.Error(w, "failed to look up user", http.StatusInternalServerError)
		return
	}
	if created {
		// Skip /welcome for fresh dev-session users so e2e tests don't have to
		// click through the theme picker on every run. The picker can still be
		// exercised locally by flipping theme_chosen back to false in SQL.
		if err := h.store.MarkThemeChosen(r.Context(), user.ID); err != nil {
			slog.ErrorContext(r.Context(), "dev-session: mark theme chosen", "err", err, "user_id", user.ID)
		} else {
			user.ThemeChosen = true
		}
	}

	if err := h.store.UpdateLastLogin(r.Context(), user.ID); err != nil {
		slog.ErrorContext(r.Context(), "failed to update last login", "user_id", user.ID, "err", err)
	}

	sessionToken, _, err := h.store.CreateSession(r.Context(), user.ID, SessionTTL)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	// Dev-only: derive Secure from request scheme so plain HTTP localhost still works.
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	setSessionCookie(w, h.cookieDomain, sessionToken, secure)

	http.Redirect(w, r, LoginRedirectFor(user), http.StatusSeeOther)
}

// LoginRedirectFor returns the URL to redirect a newly-authenticated user to.
// For dial-up theme users, /connecting renders a modem-dialing intro whose
// Connect button provides the user gesture needed for post-auth audio.
// All other themes go straight to the dashboard. Exported so the welcome
// handler and onboarding handler can use the same theme-aware landing rule.
func LoginRedirectFor(u *User) string {
	if u != nil && u.Theme == ThemeDialup {
		return "/connecting"
	}
	return "/"
}

// isSafeRedirect rejects protocol-relative ("//") and host-relative ("/\") paths
// that browsers can resolve to an attacker-controlled host.
func isSafeRedirect(path string) bool {
	return path != "" && strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !strings.HasPrefix(path, "/\\")
}

// safeReturnTo validates a returnTo path to prevent open redirect attacks.
// It only allows paths that start with "/" but not "//" (which browsers treat as
// protocol-relative URLs). Falls back to LoginRedirectFor when the path is invalid.
func safeReturnTo(returnTo string, user *User) string {
	if isSafeRedirect(returnTo) {
		return returnTo
	}
	return LoginRedirectFor(user)
}

// ClearSessionCookie removes the session cookie from the response, handling
// both domain-scoped and host-scoped variants.
func (h *Handlers) ClearSessionCookie(w http.ResponseWriter) {
	clearSessionCookie(w, h.cookieDomain)
}

// HandleLogout destroys the session and clears the cookie.
// An optional "redirect" form parameter (must be a relative path) overrides
// the default redirect to /auth/login, which the invite page uses to send the
// user back to the invite after signing out of the wrong account.
func (h *Handlers) HandleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(CookieName)
	if err == nil {
		if err := h.store.DeleteSession(r.Context(), cookie.Value); err != nil {
			slog.ErrorContext(r.Context(), "failed to delete session", "err", err)
		}
	}
	clearSessionCookie(w, h.cookieDomain)
	redirect := "/auth/login"
	if redir := r.FormValue("redirect"); isSafeRedirect(redir) {
		redirect = redir
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}
