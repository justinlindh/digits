package web

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/version"
)

type inviteData struct {
	Page          string
	Version       string
	Token         string
	HouseholdName string
	InviterName   string
	InviteEmail   string
	State         string
	CurrentEmail  string
	GoogleEnabled bool
	LogoutURL     string
}

func (h *Handler) handleInviteGet(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	inv, err := h.inviteStore.GetByToken(r.Context(), token)
	if err != nil || inv.Status != "pending" || inv.ExpiresAt.Before(time.Now()) {
		renderWith(w, h.tmplInvite, "layout-v2.html", inviteData{
			Page:    "invite",
			Version: version.Version,
			State:   "invalid",
		})
		return
	}

	hh, err := h.householdStore.GetByID(r.Context(), inv.HouseholdID)
	if err != nil {
		slog.Error("invite: household lookup failed", "err", err)
		renderWith(w, h.tmplInvite, "layout-v2.html", inviteData{
			Page: "invite", Version: version.Version, State: "invalid",
		})
		return
	}

	inviter, _ := h.authStore.GetUserByID(r.Context(), inv.InvitedBy)
	inviterName := ""
	if inviter != nil {
		inviterName = userDisplayLabel(inviter)
	}

	data := inviteData{
		Page:          "invite",
		Version:       version.Version,
		Token:         token,
		HouseholdName: hh.Name,
		InviterName:   inviterName,
		InviteEmail:   inv.Email,
		GoogleEnabled: h.googleAuth != nil && h.googleAuth.Enabled(),
	}

	// This is a public route; try to load user from session cookie if present
	var user *auth.User
	if cookie, cookieErr := r.Cookie(auth.CookieName); cookieErr == nil {
		if sess, sessErr := h.authStore.ValidateSession(r.Context(), cookie.Value); sessErr == nil {
			user, _ = h.authStore.GetUserByID(r.Context(), sess.UserID)
		}
	}

	if user == nil {
		data.State = "login"
	} else if !strings.EqualFold(user.Email, inv.Email) {
		data.State = "wrong_email"
		data.CurrentEmail = user.Email
	} else {
		data.State = "accept"
		data.CurrentEmail = user.Email
	}

	renderWith(w, h.tmplInvite, "layout-v2.html", data)
}

func (h *Handler) handleInviteAcceptPost(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	var user *auth.User
	cookie, err := r.Cookie(auth.CookieName)
	if err == nil {
		if sess, sessErr := h.authStore.ValidateSession(r.Context(), cookie.Value); sessErr == nil {
			user, _ = h.authStore.GetUserByID(r.Context(), sess.UserID)
		}
	}
	if user == nil {
		http.Redirect(w, r, "/invite/"+token, http.StatusSeeOther)
		return
	}

	inv, err := h.inviteStore.GetByToken(r.Context(), token)
	if err != nil || inv.Status != "pending" || inv.ExpiresAt.Before(time.Now()) {
		http.Redirect(w, r, "/invite/"+token, http.StatusSeeOther)
		return
	}

	if !strings.EqualFold(user.Email, inv.Email) {
		http.Redirect(w, r, "/invite/"+token, http.StatusSeeOther)
		return
	}

	if _, err := h.inviteStore.AcceptInvite(r.Context(), token); err != nil {
		slog.Error("accept invite failed", "err", err)
		http.Redirect(w, r, "/invite/"+token, http.StatusSeeOther)
		return
	}

	if err := h.householdStore.AddMember(r.Context(), user.ID, inv.HouseholdID, "admin"); err != nil {
		slog.Error("add member failed", "err", err)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if err := h.authStore.SetActiveHousehold(r.Context(), cookie.Value, inv.HouseholdID); err != nil {
		slog.Error("set active household failed", "err", err)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
