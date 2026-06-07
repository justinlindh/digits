package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
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
		members, err := h.householdStore.GetMembersWithUsers(r.Context(), hh.ID)
		if err != nil {
			slog.ErrorContext(r.Context(), "get household members failed", "household_id", hh.ID, "err", err)
		}
		for _, m := range members {
			data.Members = append(data.Members, settingsMember{
				UserID: m.UserID,
				Email:  m.Email,
				Name:   m.Name,
				IsYou:  m.UserID == user.ID,
			})
		}
		if h.inviteStore != nil {
			data.PendingInvites, err = h.inviteStore.GetPendingForHousehold(r.Context(), hh.ID)
			if err != nil {
				slog.ErrorContext(r.Context(), "get pending invites failed", "household_id", hh.ID, "err", err)
			}
		}
	}

	renderWith(r.Context(), w, h.tmplSettings, layoutFor(r), data)
}

func (h *Handler) handleSettingsHouseholdPost(w http.ResponseWriter, r *http.Request) {
	_, hh, ok := h.requireHouseholdAdmin(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name != "" {
		if err := h.householdStore.UpdateName(r.Context(), hh.ID, name); err != nil {
			slog.ErrorContext(r.Context(), "update household name failed", "household_id", hh.ID, "err", err)
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsCallHistory(w http.ResponseWriter, r *http.Request) {
	_, hh, ok := h.requireHouseholdAdmin(w, r)
	if !ok {
		return
	}
	enabled := r.FormValue("enabled") == "true"
	if err := h.householdStore.SetCallHistoryEnabled(r.Context(), hh.ID, enabled); err != nil {
		slog.ErrorContext(r.Context(), "set call history failed", "err", err)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsDoNotDisturb(w http.ResponseWriter, r *http.Request) {
	_, hh, ok := h.requireHouseholdAdmin(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	enabled := r.FormValue("enabled") == "true"
	if err := h.lineStore.SetAllSilentByHousehold(r.Context(), hh.ID, enabled); err != nil {
		slog.ErrorContext(r.Context(), "set all silent failed", "err", err, "household_id", hh.ID)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	lines, err := h.lineStore.ListByHousehold(r.Context(), hh.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "list lines for silence fan-out failed", "err", err, "household_id", hh.ID)
		http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
		return
	}
	for _, ln := range lines {
		updated := ln.Settings
		updated.SilentMode = enabled
		if pushErr := h.pushLineSettings(ln.Number, updated); pushErr != nil {
			slog.WarnContext(r.Context(), "silence fan-out push failed", "number", ln.Number, "err", pushErr)
		}
	}
	if isHTMX(r) {
		data := h.buildLinesData(r, hh, "")
		renderWith(r.Context(), w, h.tmplPhones, partialFor(r, "dnd-response", "dnd-response-am"), data)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsTimezone(w http.ResponseWriter, r *http.Request) {
	_, hh, ok := h.requireHouseholdAdmin(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	tz := strings.TrimSpace(r.FormValue("timezone"))
	if tz != "" {
		if err := h.householdStore.SetTimezone(r.Context(), hh.ID, tz); err != nil {
			slog.WarnContext(r.Context(), "set timezone failed", "err", err)
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// handleUserPrefPost shares the boilerplate between the theme/CRT/appearance
// post handlers: require a session, parse the form, read field, persist via
// save. On any failure it redirects back to /settings; on success it
// redirects to /settings?saved=1 so the page can flash a confirmation.
func (h *Handler) handleUserPrefPost(w http.ResponseWriter, r *http.Request, field string, save func(ctx context.Context, userID, value string) error) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if !parseForm(w, r) {
		return
	}
	value := r.FormValue(field)
	if err := save(r.Context(), user.ID, value); err != nil {
		slog.ErrorContext(r.Context(), "set "+field+" failed", "err", err, field, value)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsTheme(w http.ResponseWriter, r *http.Request) {
	h.handleUserPrefPost(w, r, "theme", func(ctx context.Context, userID, value string) error {
		return h.authStore.SetTheme(ctx, userID, auth.Theme(value))
	})
}

func (h *Handler) handleSettingsCRTMode(w http.ResponseWriter, r *http.Request) {
	h.handleUserPrefPost(w, r, "crt_mode", func(ctx context.Context, userID, value string) error {
		return h.authStore.SetCRTMode(ctx, userID, auth.CRTMode(value))
	})
}

func (h *Handler) handleSettingsAppearance(w http.ResponseWriter, r *http.Request) {
	h.handleUserPrefPost(w, r, "appearance", func(ctx context.Context, userID, value string) error {
		return h.authStore.SetAppearance(ctx, userID, auth.Appearance(value))
	})
}

func (h *Handler) handleHouseholdInvitePost(w http.ResponseWriter, r *http.Request) {
	user, hh, ok := h.requireHouseholdAdmin(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(r.FormValue("email")))
	if err != nil {
		http.Redirect(w, r, "/settings?error=invalid+email", http.StatusSeeOther)
		return
	}
	inviteEmail := strings.ToLower(addr.Address)

	isMember, err := h.householdStore.IsMemberByEmail(r.Context(), hh.ID, inviteEmail)
	if err != nil {
		slog.ErrorContext(r.Context(), "check member email failed", "err", err)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if isMember {
		http.Redirect(w, r, "/settings?error=already+a+member", http.StatusSeeOther)
		return
	}

	pending, err := h.inviteStore.IsPendingForHouseholdEmail(r.Context(), hh.ID, inviteEmail)
	if err != nil {
		slog.ErrorContext(r.Context(), "check pending invite failed", "err", err)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if pending {
		http.Redirect(w, r, "/settings?error=already+invited", http.StatusSeeOther)
		return
	}

	inv, err := h.inviteStore.CreateInvite(r.Context(), hh.ID, inviteEmail, user.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "create invite failed", "err", err)
		http.Redirect(w, r, "/settings?error=invite+failed", http.StatusSeeOther)
		return
	}

	link := fmt.Sprintf("%s/invite/%s", h.cfg.BaseURL, inv.Token)
	subject, body := emailpkg.HouseholdInviteEmail(hh.Name, userDisplayLabel(user), link)
	if err := h.emailer.Send(inviteEmail, subject, body); err != nil {
		slog.ErrorContext(r.Context(), "invite email failed", "email", inviteEmail, "err", err)
	}

	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleHouseholdInviteCancelPost(w http.ResponseWriter, r *http.Request) {
	_, hh, ok := h.requireHouseholdAdmin(w, r)
	if !ok {
		return
	}
	inviteID := r.PathValue("id")
	inv, err := h.inviteStore.GetByID(r.Context(), inviteID)
	if err != nil || inv.HouseholdID != hh.ID {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if err := h.inviteStore.CancelInvite(r.Context(), inviteID); err != nil {
		slog.ErrorContext(r.Context(), "cancel invite failed", "err", err)
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleHouseholdMemberRemovePost(w http.ResponseWriter, r *http.Request) {
	user, hh, ok := h.requireHouseholdAdmin(w, r)
	if !ok {
		return
	}
	targetUserID := r.PathValue("id")

	count, err := h.householdStore.MemberCount(r.Context(), hh.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "member count failed", "err", err)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if count <= 1 {
		http.Redirect(w, r, "/settings?error=last+member", http.StatusSeeOther)
		return
	}

	if err := h.householdStore.RemoveMember(r.Context(), targetUserID, hh.ID); err != nil {
		slog.ErrorContext(r.Context(), "remove member failed", "err", err)
	}
	if targetUserID == user.ID {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleAccountDeletePost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	households, err := h.householdStore.GetForUser(r.Context(), user.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "get households for account deletion failed", "user_id", user.ID, "err", err)
		http.Redirect(w, r, "/settings?error=deletion+failed", http.StatusSeeOther)
		return
	}

	for _, hh := range households {
		count, err := h.householdStore.MemberCount(r.Context(), hh.ID)
		if err != nil {
			slog.ErrorContext(r.Context(), "member count failed during account deletion", "household_id", hh.ID, "err", err)
			http.Redirect(w, r, "/settings?error=deletion+failed", http.StatusSeeOther)
			return
		}

		if count <= 1 {
			lines, err := h.lineStore.ListByHousehold(r.Context(), hh.ID)
			if err != nil {
				slog.ErrorContext(r.Context(), "list lines for deletion failed", "household_id", hh.ID, "err", err)
				http.Redirect(w, r, "/settings?error=deletion+failed", http.StatusSeeOther)
				return
			}
			for _, ln := range lines {
				h.tracker.ClearByNumber(r.Context(), ln.Number)
				for _, conn := range h.hub.GetAll(ln.Number) {
					h.hub.Unregister(ln.Number, conn)
				}
			}
			if err := h.householdStore.Delete(r.Context(), hh.ID); err != nil {
				slog.ErrorContext(r.Context(), "delete household failed", "household_id", hh.ID, "err", err)
				http.Redirect(w, r, "/settings?error=deletion+failed", http.StatusSeeOther)
				return
			}
		} else {
			// CASCADE on household_members.user_id will clean this up even if
			// RemoveMember fails, but we attempt the explicit remove first.
			if err := h.householdStore.RemoveMember(r.Context(), user.ID, hh.ID); err != nil {
				slog.ErrorContext(r.Context(), "remove member during account deletion failed", "user_id", user.ID, "household_id", hh.ID, "err", err)
			}
		}
	}

	if err := h.authStore.DeleteUser(r.Context(), user.ID); err != nil {
		slog.ErrorContext(r.Context(), "delete user failed", "user_id", user.ID, "err", err)
		http.Redirect(w, r, "/settings?error=deletion+failed", http.StatusSeeOther)
		return
	}

	h.authHandlers.ClearSessionCookie(w)
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (h *Handler) handleHouseholdSwitchPost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	if !parseForm(w, r) {
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
		slog.ErrorContext(r.Context(), "switch household failed", "err", err)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
