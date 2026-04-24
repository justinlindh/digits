package web

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/version"
)

type linksData struct {
	Page               string
	Version            string
	CallHistoryEnabled bool
	HouseholdName      string
	HouseholdDND       bool
	LinkedFamilies     []linkedFamilyRow
	PendingInvites     []linkRow
	CreatedCode        string
	Accepted           bool
	Revoked            bool
	Canceled           bool
	Conflicts          string
	Error              string
	User               *auth.User
}

type linkedFamilyRow struct {
	ID         string
	Name       string
	Lines      []line.Line
	Status     string
	AcceptedAt *time.Time
}

type linkRow struct {
	ID             string
	OtherHousehold string
	InviteCode     string
	Status         string
	CreatedAt      time.Time
	AcceptedAt     *time.Time
}

func (h *Handler) handleLinksGet(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	myHousehold := h.primaryHousehold(r)
	if myHousehold == nil {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}

	data := linksData{
		Page:               "links",
		Version:            version.Version,
		CallHistoryEnabled: myHousehold.CallHistoryEnabled,
		HouseholdName:      myHousehold.Name,
		HouseholdDND:       myHousehold.DoNotDisturb,
		CreatedCode:        r.URL.Query().Get("created"),
		Accepted:           r.URL.Query().Get("accepted") == "1",
		Revoked:            r.URL.Query().Get("revoked") == "1",
		Canceled:           r.URL.Query().Get("canceled") == "1",
		Conflicts:          r.URL.Query().Get("conflicts"),
		Error:              r.URL.Query().Get("error"),
		User:               user,
	}

	// Active links — build connected family directory
	data.LinkedFamilies = h.buildLinkedFamilies(r.Context(), myHousehold.ID)

	// Pending invites sent by this household
	pending, err := h.linkStore.GetPendingForHousehold(r.Context(), myHousehold.ID)
	if err != nil {
		slog.Error("get pending links failed", "err", err)
	}
	for _, l := range pending {
		data.PendingInvites = append(data.PendingInvites, linkRow{
			ID:         l.ID,
			InviteCode: l.InviteCode,
			Status:     l.Status,
			CreatedAt:  l.CreatedAt,
		})
	}

	renderWith(w, h.tmplLinks, layoutFor(r), data)
}

func (h *Handler) handleLinksInvitePost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	myHousehold := h.primaryHousehold(r)
	if myHousehold == nil {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}

	link, err := h.linkStore.CreateInvite(r.Context(), myHousehold.ID, user.ID)
	if err != nil {
		slog.Error("create invite failed", "err", err)
		http.Redirect(w, r, "/links?error="+err.Error(), http.StatusSeeOther)
		return
	}

	// Send email notification to the creating user with the invite code
	if h.emailSender != nil && user.Email != "" {
		subj, body := email.HouseholdInviteEmail(myHousehold.Name, link.InviteCode, h.cfg.BaseURL)
		if err := h.emailSender.Send(user.Email, subj, body); err != nil {
			slog.Error("send invite email failed", "err", err)
		}
	}

	http.Redirect(w, r, "/links?created="+link.InviteCode, http.StatusSeeOther)
}

func (h *Handler) handleLinksAcceptPost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	myHousehold := h.primaryHousehold(r)
	if myHousehold == nil {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(strings.ToUpper(r.FormValue("code")))
	if code == "" {
		http.Redirect(w, r, "/links?error=invite+code+required", http.StatusSeeOther)
		return
	}

	link, err := h.linkStore.AcceptInvite(r.Context(), code, user.ID, myHousehold.ID)
	if err != nil {
		slog.Error("accept invite failed", "err", err)
		http.Redirect(w, r, "/links?error="+err.Error(), http.StatusSeeOther)
		return
	}

	// Check for number conflicts between the two households
	bID := ""
	if link.HouseholdBID != nil {
		bID = *link.HouseholdBID
	}
	conflicts, _ := h.linkStore.FindNumberConflicts(r.Context(), link.HouseholdAID, bID)
	if len(conflicts) > 0 {
		var names []string
		for _, c := range conflicts {
			names = append(names, c.Number)
		}
		slog.Warn("number conflicts on link accept", "conflicts", names)
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
	owned := false
	for _, hh := range households {
		if hh.ID == link.HouseholdAID || (link.HouseholdBID != nil && hh.ID == *link.HouseholdBID) {
			owned = true
			break
		}
	}
	if !owned {
		http.NotFound(w, r)
		return
	}

	wasPending := link.Status == "pending"

	if err := h.linkStore.RevokeLink(r.Context(), id, user.ID); err != nil {
		slog.Error("revoke link failed", "link_id", id, "err", err)
		http.Redirect(w, r, "/links?error="+err.Error(), http.StatusSeeOther)
		return
	}

	target := "/links?revoked=1"
	if wasPending {
		target = "/links?canceled=1"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

