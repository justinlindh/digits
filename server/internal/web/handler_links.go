package web

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/household"
)

type linksData struct {
	chromeData
	LinkedFamilies []linkedFamilyRow
	PendingInvites []linkRow
	CreatedCode    string
	Accepted       bool
	Revoked        bool
	Canceled       bool
	Conflicts      string
	Error          string
}

type linkRow struct {
	ID         string
	InviteCode string
	Status     string
	CreatedAt  time.Time
	AcceptedAt *time.Time
}

func (h *Handler) handleLinksGet(w http.ResponseWriter, r *http.Request) {
	_, myHousehold, ok := h.requireHouseholdMember(w, r)
	if !ok {
		return
	}

	data := linksData{
		chromeData:  h.newChromeDataWithHouseholds(r, "links"),
		CreatedCode: r.URL.Query().Get("created"),
		Accepted:    r.URL.Query().Get("accepted") == "1",
		Revoked:     r.URL.Query().Get("revoked") == "1",
		Canceled:    r.URL.Query().Get("canceled") == "1",
		Conflicts:   r.URL.Query().Get("conflicts"),
		Error:       r.URL.Query().Get("error"),
	}

	data.LinkedFamilies = h.buildLinkedFamilies(r.Context(), myHousehold.ID)

	// Pending invites sent by this household
	pending, err := h.linkStore.GetPendingForHousehold(r.Context(), myHousehold.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "get pending links failed", "err", err)
	}
	for _, l := range pending {
		data.PendingInvites = append(data.PendingInvites, linkRow{
			ID:         l.ID,
			InviteCode: l.InviteCode,
			Status:     l.Status,
			CreatedAt:  l.CreatedAt,
		})
	}

	renderWith(r.Context(), w, h.tmplLinks, layoutFor(r), data)
}

func (h *Handler) handleLinksInvitePost(w http.ResponseWriter, r *http.Request) {
	user, myHousehold, ok := h.requireHouseholdAdmin(w, r)
	if !ok {
		return
	}

	link, err := h.linkStore.CreateInvite(r.Context(), myHousehold.ID, user.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "create invite failed", "err", err)
		http.Redirect(w, r, "/links?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/links?created="+link.InviteCode, http.StatusSeeOther)
}

func (h *Handler) handleLinksAcceptPost(w http.ResponseWriter, r *http.Request) {
	user, myHousehold, ok := h.requireHouseholdMember(w, r)
	if !ok {
		return
	}

	if !parseForm(w, r) {
		return
	}
	code := strings.TrimSpace(strings.ToUpper(r.FormValue("code")))
	if code == "" {
		http.Redirect(w, r, "/links?error=invite+code+required", http.StatusSeeOther)
		return
	}

	link, err := h.linkStore.AcceptInvite(r.Context(), code, user.ID, myHousehold.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "accept invite failed", "err", err)
		http.Redirect(w, r, "/links?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}

	// Check for number conflicts between the two households
	bID := ""
	if link.HouseholdBID != nil {
		bID = *link.HouseholdBID
	}
	conflicts, err := h.linkStore.FindNumberConflicts(r.Context(), link.HouseholdAID, bID)
	if err != nil {
		slog.WarnContext(r.Context(), "find number conflicts failed", "err", err)
	}
	if len(conflicts) > 0 {
		var names []string
		for _, c := range conflicts {
			names = append(names, c.Number)
		}
		slog.WarnContext(r.Context(), "number conflicts on link accept", "conflicts", names)
		http.Redirect(w, r, "/links?accepted=1&conflicts="+strings.Join(names, ","), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/links?accepted=1", http.StatusSeeOther)
}

func (h *Handler) handleLinksRevokePost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	id := r.PathValue("id")

	// Look up the link and verify the user belongs to one of the linked
	// households before allowing revocation.
	link, err := h.linkStore.GetByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if h.householdStore == nil {
		http.NotFound(w, r)
		return
	}
	households, err := h.householdStore.GetForUser(r.Context(), user.ID)
	if err != nil || len(households) == 0 {
		http.NotFound(w, r)
		return
	}
	isAdmin := false
	for _, hh := range households {
		if hh.ID == link.HouseholdAID || (link.HouseholdBID != nil && hh.ID == *link.HouseholdBID) {
			if h.isHouseholdAdmin(r, hh.ID) {
				isAdmin = true
				break
			}
		}
	}
	if !isAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	wasPending := link.Status == household.LinkStatusPending

	if err := h.linkStore.RevokeLink(r.Context(), id, user.ID); err != nil {
		slog.ErrorContext(r.Context(), "revoke link failed", "link_id", id, "err", err)
		http.Redirect(w, r, "/links?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}

	target := "/links?revoked=1"
	if wasPending {
		target = "/links?canceled=1"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
