package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/household"
)

type inviteData struct {
	chromeData
	Token         string
	InviterName   string
	InviteEmail   string
	State         string
	CurrentEmail  string
	GoogleEnabled bool
}

func (h *Handler) userFromSessionCookie(r *http.Request) (*auth.User, string) {
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		return nil, ""
	}
	sess, err := h.authStore.ValidateSession(r.Context(), cookie.Value)
	if err != nil {
		return nil, ""
	}
	user, _ := h.authStore.GetUserByID(r.Context(), sess.UserID)
	return user, cookie.Value
}

func (h *Handler) handleInviteGet(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	inv, err := h.inviteStore.GetByToken(r.Context(), token)
	if err != nil || inv.Status != household.InviteStatusPending || inv.ExpiresAt.Before(time.Now()) {
		renderWith(r.Context(), w, h.tmplInvite, "layout-v2.html", inviteData{
			chromeData: newChromeData("invite", nil, nil),
			State:      "invalid",
		})
		return
	}

	hh, err := h.householdStore.GetByID(r.Context(), inv.HouseholdID)
	if err != nil {
		slog.ErrorContext(r.Context(), "invite: household lookup failed", "err", err)
		renderWith(r.Context(), w, h.tmplInvite, "layout-v2.html", inviteData{
			chromeData: newChromeData("invite", nil, nil),
			State:      "invalid",
		})
		return
	}

	inviter, _ := h.authStore.GetUserByID(r.Context(), inv.InvitedBy)
	inviterName := ""
	if inviter != nil {
		inviterName = userDisplayLabel(inviter)
	}

	user, _ := h.userFromSessionCookie(r)

	data := inviteData{
		chromeData:    newChromeData("invite", user, hh),
		Token:         token,
		InviterName:   inviterName,
		InviteEmail:   inv.Email,
		GoogleEnabled: h.googleAuth != nil && h.googleAuth.Enabled(),
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

	renderWith(r.Context(), w, h.tmplInvite, "layout-v2.html", data)
}

func (h *Handler) handleInviteAcceptPost(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	user, sessionToken := h.userFromSessionCookie(r)
	if user == nil {
		http.Redirect(w, r, "/invite/"+token, http.StatusSeeOther)
		return
	}

	inv, err := h.householdStore.RedeemInvite(r.Context(), token, user.ID, user.Email)
	if err != nil {
		if !isExpectedInviteRedemptionError(err) {
			slog.ErrorContext(r.Context(), "redeem invite failed", "err", err)
		}
		http.Redirect(w, r, "/invite/"+token, http.StatusSeeOther)
		return
	}

	if err := h.authStore.SetActiveHousehold(r.Context(), sessionToken, inv.HouseholdID); err != nil {
		slog.ErrorContext(r.Context(), "set active household failed", "err", err)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func isExpectedInviteRedemptionError(err error) bool {
	return errors.Is(err, household.ErrInviteExpiredOrUsed) || errors.Is(err, household.ErrInviteEmailMismatch)
}
