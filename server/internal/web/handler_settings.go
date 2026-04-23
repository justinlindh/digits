package web

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/version"
)

type settingsData struct {
	Page               string
	Version            string
	CallHistoryEnabled bool
	HouseholdName      string
	User               *auth.User
	Household          *household.Household
	Saved              bool
}

func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	var hh *household.Household
	if user != nil && h.householdStore != nil {
		households, _ := h.householdStore.GetForUser(r.Context(), user.ID)
		if len(households) > 0 {
			hh = households[0]
		}
	}
	hhName := ""
	if hh != nil {
		hhName = hh.Name
	}
	renderWith(w, h.tmplSettings, layoutFor(r), settingsData{
		Page:               "settings",
		Version:            version.Version,
		CallHistoryEnabled: h.callHistoryEnabled(r),
		HouseholdName:      hhName,
		User:               user,
		Household:          hh,
		Saved:              r.URL.Query().Get("saved") == "1",
	})
}

func (h *Handler) handleSettingsHouseholdPost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	households, _ := h.householdStore.GetForUser(r.Context(), user.ID)
	if len(households) == 0 {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name != "" {
		if err := h.householdStore.UpdateName(r.Context(), households[0].ID, name); err != nil {
			slog.Error("update household name failed", "household_id", households[0].ID, "err", err)
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}


func (h *Handler) handleSettingsCallHistory(w http.ResponseWriter, r *http.Request) {
	if h.householdStore == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	households, err := h.householdStore.GetForUser(r.Context(), user.ID)
	if err != nil || len(households) == 0 {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	enabled := r.FormValue("enabled") == "true"
	if err := h.householdStore.SetCallHistoryEnabled(r.Context(), households[0].ID, enabled); err != nil {
		slog.Error("set call history failed", "err", err)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsTimezone(w http.ResponseWriter, r *http.Request) {
	if h.householdStore == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	households, err := h.householdStore.GetForUser(r.Context(), user.ID)
	if err != nil || len(households) == 0 {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	tz := strings.TrimSpace(r.FormValue("timezone"))
	if tz != "" {
		if err := h.householdStore.SetTimezone(r.Context(), households[0].ID, tz); err != nil {
			slog.Warn("set timezone failed", "err", err)
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsTheme(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	theme := auth.Theme(r.FormValue("theme"))
	if err := h.authStore.SetTheme(r.Context(), user.ID, theme); err != nil {
		slog.Error("set theme failed", "err", err, "theme", theme)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsCRTMode(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	mode := auth.CRTMode(r.FormValue("crt_mode"))
	if err := h.authStore.SetCRTMode(r.Context(), user.ID, mode); err != nil {
		slog.Error("set crt_mode failed", "err", err, "crt_mode", mode)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsAppearance(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	appearance := auth.Appearance(r.FormValue("appearance"))
	if err := h.authStore.SetAppearance(r.Context(), user.ID, appearance); err != nil {
		slog.Error("set appearance failed", "err", err, "appearance", appearance)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}
