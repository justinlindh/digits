package web

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/version"
)

type callsData struct {
	Page               string
	Version            string
	CallHistoryEnabled bool
	HouseholdName      string
	Entries            []calls.HistoryEntry
	User               *auth.User
}

func (h *Handler) handleCalls(w http.ResponseWriter, r *http.Request) {
	hhName, callHistory, loc := h.householdContext(r)
	if !callHistory {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	var entries []calls.HistoryEntry

	// Scope call log to the user's household lines
	user := auth.UserFromContext(r.Context())
	if user != nil && h.lineStore != nil && h.householdStore != nil {
		households, err := h.householdStore.GetForUser(user.ID)
		if err != nil {
			slog.Error("get households for user failed", "user_id", user.ID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if len(households) > 0 {
			lines, err := h.lineStore.ListByHousehold(households[0].ID)
			if err != nil {
				slog.Error("list lines for household failed", "household_id", households[0].ID, "err", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			if len(lines) > 0 {
				numbers := make([]string, len(lines))
				for i, l := range lines {
					numbers[i] = l.Number
				}
				hist, err := h.tracker.RecentHistoryForPhones(numbers, 100)
				if err != nil {
					slog.Error("query recent history failed", "err", err)
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
				entries = hist
			}
		}
	}
	if entries == nil {
		entries = []calls.HistoryEntry{}
	}

	// Localize timestamps for display.
	for i := range entries {
		if entries[i].Call != nil {
			entries[i].Call.StartedAt = entries[i].Call.StartedAt.In(loc)
		}
		if entries[i].Conference != nil {
			entries[i].Conference.CreatedAt = entries[i].Conference.CreatedAt.In(loc)
		}
		entries[i].SortTime = entries[i].SortTime.In(loc)
	}

	renderWith(w, h.tmplCalls, layoutFor(r), callsData{Page: "calls", Version: version.Version, CallHistoryEnabled: callHistory, HouseholdName: hhName, Entries: entries, User: user})
}

// ---- Settings ----

type callLiveDetailData struct {
	Page               string
	Version            string
	User               *auth.User
	HouseholdName      string
	CallHistoryEnabled bool
	Call               calls.Call
	Caller             LinkHealthEndpointResp
	Callee             LinkHealthEndpointResp
	Ended              bool
	ForceEndedBy       string
}

// handleCallLiveDetail renders the observation-deck page for a specific call.
// Auth uses the same ownership check as the JSON endpoint. Ended calls are
// NOT 404'd here — they render in terminal state with the last-known samples
// from DB fallback, making the URL useful as a postmortem surface.
func (h *Handler) handleCallLiveDetail(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	callID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || callID <= 0 {
		http.NotFound(w, r)
		return
	}
	call, ownedLines, _, ok := h.requireCallEndpointOwnership(w, r, callID)
	if !ok {
		return
	}

	user := auth.UserFromContext(r.Context())
	linkedIndex := h.linkedIndexForCall(r.Context(), ownedLines)
	callerEp, err := h.buildLinkHealthEndpoint(r.Context(), call.ID, call.Caller, linkedIndex, ownedLines)
	if err != nil {
		slog.Error("call-live: build caller endpoint failed", "call_id", callID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	calleeEp, err := h.buildLinkHealthEndpoint(r.Context(), call.ID, call.Callee, linkedIndex, ownedLines)
	if err != nil {
		slog.Error("call-live: build callee endpoint failed", "call_id", callID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	data := callLiveDetailData{
		Page:               "call-live",
		Version:            version.Version,
		User:               user,
		HouseholdName:      h.householdNameFromContext(r),
		CallHistoryEnabled: h.callHistoryEnabled(r),
		Call:               call,
		Caller:             callerEp,
		Callee:             calleeEp,
		Ended:              call.Status == "ended",
		ForceEndedBy:       h.forceEndedLabel(call),
	}

	renderWith(w, h.tmplCallLiveDetail, layoutFor(r), data)
}

// forceEndedLabel returns the display label for who force-ended a call.
// Returns "" if peer-initiated or user lookup fails.
func (h *Handler) forceEndedLabel(call calls.Call) string {
	if call.ForceEndedBy == nil {
		return ""
	}
	u, err := h.authStore.GetUserByID(*call.ForceEndedBy)
	if err != nil || u == nil {
		return ""
	}
	return userDisplayLabel(u)
}
