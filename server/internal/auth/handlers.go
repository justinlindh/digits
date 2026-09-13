package auth

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/version"
)

// Metrics records aggregate, non-identifying auth outcomes. It is implemented
// by *metrics.Registry; the interface lives here so the auth package does not
// import the metrics package and so handler tests can pass a fake or nil. All
// methods must tolerate a nil receiver value via the guarded package helpers.
type Metrics interface {
	ObserveLogin(method, result string)
	ObserveMagicLink(event string)
}

// InviteChecker reports whether an address holds a pending household invite.
// It is the second of the two things that entitle an address to a magic link
// (the first being an existing account).
type InviteChecker interface {
	HasPendingInvite(ctx context.Context, addr string) (bool, error)
}

// honeypotField is the name of the hidden login-form input that people never
// see and form bots fill in. Any submission carrying a value is dropped.
const honeypotField = "website"

// Handlers provides HTTP handlers for login, magic link, and logout flows.
type Handlers struct {
	store        *Store
	google       *GoogleAuth
	emailer      email.Sender
	baseURL      string
	cookieDomain string // optional, e.g. ".digits.family" for subdomain sharing
	loginTmpl    *template.Template
	devMode      bool
	metrics      Metrics       // may be nil
	invites      InviteChecker // may be nil: then only existing accounts get links
	// alwaysAllowed are lowercased addresses that may request a link with no
	// account or invite. Deployment config supplies them (the admin
	// allowlist), which is how a fresh install creates its first account.
	alwaysAllowed []string
}

// SetInviteChecker wires the pending-invite lookup used to decide whether an
// address without an account may receive a magic link.
func (h *Handlers) SetInviteChecker(c InviteChecker) {
	h.invites = c
}

// SetAlwaysAllowed sets the addresses that may always request a magic link.
func (h *Handlers) SetAlwaysAllowed(addrs []string) {
	h.alwaysAllowed = addrs
}

// NewHandlers creates auth HTTP handlers.
// cookieDomain sets the cookie Domain attribute (e.g. ".digits.family"); pass "" to omit it.
// m may be nil, in which case auth metrics are not recorded.
func NewHandlers(store *Store, google *GoogleAuth, emailer email.Sender, baseURL, cookieDomain string, loginTmpl *template.Template, devMode bool, m Metrics) *Handlers {
	return &Handlers{
		store:        store,
		google:       google,
		emailer:      emailer,
		baseURL:      baseURL,
		cookieDomain: cookieDomain,
		loginTmpl:    loginTmpl,
		devMode:      devMode,
		metrics:      m,
	}
}

// mayReceiveMagicLink reports whether addr is entitled to a sign-in link: it
// is on the deployment's always-allowed list, has an account, or holds a
// pending household invite. Anything else is a stranger, and mailing
// strangers on a form submitter's behalf is what this refuses.
func (h *Handlers) mayReceiveMagicLink(ctx context.Context, addr string) (bool, error) {
	for _, a := range h.alwaysAllowed {
		if strings.EqualFold(a, addr) {
			return true, nil
		}
	}
	_, err := h.store.GetUserByEmail(ctx, addr)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, ErrUserNotFound) {
		return false, err
	}
	if h.invites == nil {
		return false, nil
	}
	return h.invites.HasPendingInvite(ctx, addr)
}

// suppressMagicLink is the response for a request the form accepts but will
// not mail: indistinguishable from a real send to the caller, counted as
// suppressed in metrics.
func (h *Handlers) suppressMagicLink(w http.ResponseWriter, r *http.Request) {
	h.observeMagicLink("suppressed")
	http.Redirect(w, r, "/auth/login?success=check+your+email", http.StatusSeeOther)
}

// observeLogin and observeMagicLink guard the nil-metrics case so call sites
// stay a single line.
func (h *Handlers) observeLogin(method, result string) {
	if h.metrics != nil {
		h.metrics.ObserveLogin(method, result)
	}
}

func (h *Handlers) observeMagicLink(event string) {
	if h.metrics != nil {
		h.metrics.ObserveMagicLink(event)
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
	emailAddr := email.Normalize(r.FormValue("email"))
	if emailAddr == "" {
		http.Redirect(w, r, "/auth/login?error=email+required", http.StatusSeeOther)
		return
	}
	returnTo := r.FormValue("return_to")

	// Every path that declines to send lands on the same redirect as a real
	// send, so the form never reveals whether an address is known.
	if r.FormValue(honeypotField) != "" {
		h.suppressMagicLink(w, r)
		return
	}
	known, err := h.mayReceiveMagicLink(r.Context(), emailAddr)
	if err != nil {
		slog.ErrorContext(r.Context(), "magic link eligibility check failed", "err", err)
		http.Redirect(w, r, "/auth/login?error=try+again", http.StatusSeeOther)
		return
	}
	if !known {
		h.suppressMagicLink(w, r)
		return
	}

	token, err := h.store.CreateMagicLink(r.Context(), emailAddr, MagicLinkTTL, returnTo)
	if errors.Is(err, ErrMagicLinkRateLimited) {
		h.suppressMagicLink(w, r)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "magic link creation failed", "err", err)
		http.Redirect(w, r, "/auth/login?error=try+again", http.StatusSeeOther)
		return
	}
	h.observeMagicLink("issued")

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
		h.observeLogin("magic_link", "failure")
		http.Redirect(w, r, "/auth/login?error=invalid+or+expired+link", http.StatusSeeOther)
		return
	}
	h.observeMagicLink("consumed")

	user, _, err := h.store.GetOrCreateUserByEmail(r.Context(), emailAddr)
	if err != nil {
		h.observeLogin("magic_link", "failure")
		slog.ErrorContext(r.Context(), "magic link verify: get or create user", "err", err)
		http.Error(w, "failed to look up user", http.StatusInternalServerError)
		return
	}

	if err := h.store.UpdateLastLogin(r.Context(), user.ID); err != nil {
		slog.ErrorContext(r.Context(), "failed to update last login", "user_id", user.ID, "err", err)
	}

	sessionToken, _, err := h.store.CreateSession(r.Context(), user.ID, SessionTTL)
	if err != nil {
		h.observeLogin("magic_link", "failure")
		if errors.Is(err, ErrAccountDisabled) {
			http.Redirect(w, r, "/auth/login?error=account+disabled", http.StatusSeeOther)
			return
		}
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	h.observeLogin("magic_link", "success")

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
		if errors.Is(err, ErrAccountDisabled) {
			http.Redirect(w, r, "/auth/login?error=account+disabled", http.StatusSeeOther)
			return
		}
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	h.observeLogin("dev", "success")

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
