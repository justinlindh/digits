package web

import (
	"log/slog"
	"net/http"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/version"
)

// welcomeData carries data for the /welcome template. Welcome is pre-theme
// (the user has not picked one yet) and renders its own neutral chrome, so
// it does not embed chromeData. Theme IDs are passed in from Go rather than
// hardcoded in the template so a future theme rename can't silently drift
// from auth.Theme*.
type welcomeData struct {
	Version       string
	User          *auth.User
	ThemeIntercom auth.Theme
	ThemeDialup   auth.Theme
	ThemeAM       auth.Theme
}

func (h *Handler) handleWelcomeGet(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	renderWith(w, h.tmplWelcome, "welcome.html", welcomeData{
		Version:       version.Version,
		User:          user,
		ThemeIntercom: auth.ThemeIntercom,
		ThemeDialup:   auth.ThemeDialup,
		ThemeAM:       auth.ThemeAnsweringMachine,
	})
}

func (h *Handler) handleWelcomePost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	theme := auth.Theme(r.FormValue("theme"))
	if !theme.Valid() {
		http.Redirect(w, r, "/welcome", http.StatusSeeOther)
		return
	}
	if err := h.authStore.SetTheme(r.Context(), user.ID, theme); err != nil {
		slog.Error("welcome: set theme failed", "err", err, "theme", theme, "user_id", user.ID)
		http.Error(w, "failed to save theme", http.StatusInternalServerError)
		return
	}
	if err := h.authStore.MarkThemeChosen(r.Context(), user.ID); err != nil {
		slog.Error("welcome: mark theme chosen failed", "err", err, "user_id", user.ID)
		http.Error(w, "failed to save theme", http.StatusInternalServerError)
		return
	}
	// Mutate the request-scoped User snapshot so LoginRedirectFor sees the
	// theme we just persisted; UserFromContext otherwise still holds the
	// pre-pick value and would mis-route dialup picks to / instead of
	// /connecting. Cheaper than a re-fetch for a once-per-account flow.
	user.Theme = theme
	user.ThemeChosen = true
	http.Redirect(w, r, auth.LoginRedirectFor(user), http.StatusSeeOther)
}
