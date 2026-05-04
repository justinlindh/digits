package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/justinlindh/digits/server/internal/auth"
	emailpkg "github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/household"
)

type settingsMember struct {
	UserID string
	Email  string
	Name   string
	IsYou  bool
}

type settingsData struct {
	chromeData
	Saved          bool
	Error          string
	Members        []settingsMember
	PendingInvites []*household.HouseholdInvite
}

func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	data := settingsData{
		chromeData: h.newChromeDataWithHouseholds(r, "settings"),
		Saved:      r.URL.Query().Get("saved") == "1",
		Error:      r.URL.Query().Get("error"),
	}

	hh := data.Household
	if hh != nil && user != nil {
		members, _ := h.householdStore.GetMembersWithUsers(r.Context(), hh.ID)
		for _, m := range members {
			data.Members = append(data.Members, settingsMember{
				UserID: m.UserID,
				Email:  m.Email,
				Name:   m.Name,
				IsYou:  m.UserID == user.ID,
			})
		}
		if h.inviteStore != nil {
			data.PendingInvites, _ = h.inviteStore.GetPendingForHousehold(r.Context(), hh.ID)
		}
	}

	renderWith(w, h.tmplSettings, layoutFor(r), data)
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

func (h *Handler) handleSettingsDoNotDisturb(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
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
	householdID := households[0].ID
	if err := h.householdStore.SetDoNotDisturb(r.Context(), householdID, enabled); err != nil {
		slog.Error("set do not disturb failed", "err", err, "household_id", householdID)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	lines, err := h.lineStore.ListByHousehold(r.Context(), householdID)
	if err != nil {
		slog.Error("list lines for DND fan-out failed", "err", err, "household_id", householdID)
		http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
		return
	}
	for _, ln := range lines {
		if pushErr := h.pushLineSettings(ln.Number, ln.Settings, enabled); pushErr != nil {
			slog.Warn("DND fan-out push failed", "number", ln.Number, "err", pushErr)
		}
	}
	if isHTMX(r) {
		households[0].DoNotDisturb = enabled
		data := h.buildLinesData(r, households[0], "")
		renderWith(w, h.tmplPhones, partialFor(r, "dnd-response", "dnd-response-am"), data)
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

func (h *Handler) handleHouseholdInvitePost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	hh := h.activeHousehold(r)
	if hh == nil {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	inviteEmail := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	if inviteEmail == "" || !strings.Contains(inviteEmail, "@") {
		http.Redirect(w, r, "/settings?error=invalid+email", http.StatusSeeOther)
		return
	}

	isMember, err := h.householdStore.IsMemberByEmail(r.Context(), hh.ID, inviteEmail)
	if err != nil {
		slog.Error("check member email failed", "err", err)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if isMember {
		http.Redirect(w, r, "/settings?error=already+a+member", http.StatusSeeOther)
		return
	}

	inv, err := h.inviteStore.CreateInvite(r.Context(), hh.ID, inviteEmail, user.ID)
	if err != nil {
		slog.Error("create invite failed", "err", err)
		http.Redirect(w, r, "/settings?error=invite+failed", http.StatusSeeOther)
		return
	}

	link := fmt.Sprintf("%s/invite/%s", h.cfg.BaseURL, inv.Token)
	subject, body := emailpkg.HouseholdInviteEmail(hh.Name, userDisplayLabel(user), link)
	if err := h.emailer.Send(inviteEmail, subject, body); err != nil {
		slog.Error("invite email failed", "email", inviteEmail, "err", err)
	}

	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleHouseholdInviteCancelPost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	inviteID := r.PathValue("id")
	if err := h.inviteStore.CancelInvite(r.Context(), inviteID); err != nil {
		slog.Error("cancel invite failed", "err", err)
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleHouseholdMemberRemovePost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	hh := h.activeHousehold(r)
	if hh == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	targetUserID := r.PathValue("id")

	count, err := h.householdStore.MemberCount(r.Context(), hh.ID)
	if err != nil {
		slog.Error("member count failed", "err", err)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if count <= 1 {
		http.Redirect(w, r, "/settings?error=last+member", http.StatusSeeOther)
		return
	}

	if err := h.householdStore.RemoveMember(r.Context(), targetUserID, hh.ID); err != nil {
		slog.Error("remove member failed", "err", err)
	}
	if targetUserID == user.ID {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleHouseholdSwitchPost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	householdID := r.FormValue("household_id")

	_, err := h.householdStore.GetRole(r.Context(), user.ID, householdID)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := h.authStore.SetActiveHousehold(r.Context(), cookie.Value, householdID); err != nil {
		slog.Error("switch household failed", "err", err)
	}

	referer := r.Header.Get("Referer")
	if referer == "" {
		referer = "/"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}
