package web

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/line"
)

// requireLineOwnership looks up a line by its number and verifies the
// authenticated user's household owns it. Returns the line on success, or
// nil after writing a 404 — unauthenticated, unknown, and unauthorized
// responses are intentionally indistinguishable to avoid leaking whether a
// given number exists.
func (h *Handler) requireLineOwnership(w http.ResponseWriter, r *http.Request, number string) *line.Line {
	ln, err := h.lineStore.GetByNumber(r.Context(), number)
	if err != nil {
		http.NotFound(w, r)
		return nil
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return nil
	}
	if h.householdStore == nil {
		http.NotFound(w, r)
		return nil
	}
	households, err := h.householdStore.GetForUser(r.Context(), user.ID)
	if err != nil || len(households) == 0 {
		http.NotFound(w, r)
		return nil
	}
	for _, hh := range households {
		if hh.ID == ln.HouseholdID {
			return ln
		}
	}
	http.NotFound(w, r)
	return nil
}

// ownedLinesForUser returns the lines owned by any household the user belongs
// to, keyed by number, plus the primary household ID. Returns (nil, "", false)
// if the user has no households or any lookup fails — caller writes 404, same
// response shape as nonexistent.
func (h *Handler) ownedLinesForUser(ctx context.Context, user *auth.User) (map[string]*line.Line, string, bool) {
	households, err := h.householdStore.GetForUser(ctx, user.ID)
	if err != nil {
		slog.Error("link_health: list households failed", "user_id", user.ID, "err", err)
		return nil, "", false
	}
	if len(households) == 0 {
		return nil, "", false
	}
	lines := make(map[string]*line.Line)
	for _, hh := range households {
		hhLines, err := h.lineStore.ListByHousehold(ctx, hh.ID)
		if err != nil {
			slog.Error("link_health: list lines failed", "household_id", hh.ID, "err", err)
			return nil, "", false
		}
		for i := range hhLines {
			ln := hhLines[i]
			lines[ln.Number] = &ln
		}
	}
	return lines, households[0].ID, true
}

// requireCallEndpointOwnership verifies the authenticated user owns either
// endpoint of the call (across ANY household the user belongs to). Returns
// the call, the owned-lines map (keyed by number), the primary household ID,
// and true on success. On any failure, writes 404 (unauthorized and
// nonexistent are indistinguishable).
//
// Implementation detail: always performs the same sequence of DB queries
// regardless of auth outcome, to avoid a timing side channel on call-id
// enumeration. Linked households do NOT grant access to telemetry; only
// direct household ownership of a call endpoint does.
func (h *Handler) requireCallEndpointOwnership(w http.ResponseWriter, r *http.Request, callID int64) (calls.Call, map[string]*line.Line, string, bool) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return calls.Call{}, nil, "", false
	}

	// Always do both queries in the same order, regardless of miss reason.
	ownedLines, primaryHH, ok := h.ownedLinesForUser(r.Context(), user)
	call, callErr := h.tracker.GetCall(r.Context(), callID)

	if callErr != nil {
		slog.Error("link_health: get call failed", "call_id", callID, "err", callErr)
		http.NotFound(w, r)
		return calls.Call{}, nil, "", false
	}
	if !ok || call.ID == 0 {
		http.NotFound(w, r)
		return calls.Call{}, nil, "", false
	}

	_, ownsCaller := ownedLines[call.Caller]
	_, ownsCallee := ownedLines[call.Callee]
	if !ownsCaller && !ownsCallee {
		http.NotFound(w, r)
		return calls.Call{}, nil, "", false
	}
	return call, ownedLines, primaryHH, true
}

// loadConferenceForUser runs the constant-time dual-query sequence the
// conference auth helpers share: snapshot the user's owned lines and fetch
// the conference, both unconditionally, then 404 on any failure. Callers
// layer their own ownership predicate on the returned (conf, ownedLines).
func (h *Handler) loadConferenceForUser(w http.ResponseWriter, r *http.Request, confID uuid.UUID, errLog string) (*calls.ConferenceSummary, map[string]*line.Line, string, bool) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return nil, nil, "", false
	}
	ownedLines, primaryHH, ok := h.ownedLinesForUser(r.Context(), user)
	conf, confErr := h.tracker.GetConferenceByID(r.Context(), confID)
	if confErr != nil {
		slog.Error(errLog+": get conference failed", "conf_id", confID, "err", confErr)
		http.NotFound(w, r)
		return nil, nil, "", false
	}
	if !ok || conf == nil {
		http.NotFound(w, r)
		return nil, nil, "", false
	}
	return conf, ownedLines, primaryHH, true
}

// requireConferenceOwnership verifies the authenticated user directly owns
// at least one conference member line (across any household the user belongs
// to). Linked households do NOT grant observation; only direct household
// ownership of a member line does. Returns the conference, the owned-lines
// map (keyed by number), the primary household ID, and true on success. On
// any failure, writes 404 (unauthorized and nonexistent are
// indistinguishable).
//
// Mirrors requireCallEndpointOwnership's constant-time query sequence to
// avoid a timing side channel on conference-id enumeration.
func (h *Handler) requireConferenceOwnership(w http.ResponseWriter, r *http.Request, confID uuid.UUID) (*calls.ConferenceSummary, map[string]*line.Line, string, bool) {
	conf, ownedLines, primaryHH, ok := h.loadConferenceForUser(w, r, confID, "conference_link_health")
	if !ok {
		return nil, nil, "", false
	}
	for _, member := range conf.Members {
		if _, owns := ownedLines[member]; owns {
			return conf, ownedLines, primaryHH, true
		}
	}
	http.NotFound(w, r)
	return nil, nil, "", false
}

// requireConferenceHostOwnership verifies the authenticated user's
// household directly owns the conference host phone. Non-host-household
// observers receive 404 (same information-hiding posture as
// requireConferenceOwnership). Used to gate the kick endpoint and the
// kick buttons on the deck.
//
// Query sequence matches requireCallEndpointOwnership and
// requireConferenceOwnership to avoid a timing side channel on
// conference-id enumeration.
func (h *Handler) requireConferenceHostOwnership(w http.ResponseWriter, r *http.Request, confID uuid.UUID) (*calls.ConferenceSummary, map[string]*line.Line, string, bool) {
	conf, ownedLines, primaryHH, ok := h.loadConferenceForUser(w, r, confID, "conference_kick")
	if !ok {
		return nil, nil, "", false
	}
	if _, owns := ownedLines[conf.Host]; !owns {
		http.NotFound(w, r)
		return nil, nil, "", false
	}
	return conf, ownedLines, primaryHH, true
}

// resolveMemberDisplayName picks the best label for a member phone.
// Priority: owned-line name (only if non-empty), linked-index peer name,
// bare number fallback.
func resolveMemberDisplayName(number string, ownedLines map[string]*line.Line, linkedIndex map[string]string) string {
	if ln, ok := ownedLines[number]; ok && ln != nil && ln.Name != "" {
		return ln.Name
	}
	if name := resolvePeerName(number, linkedIndex); name != "" {
		return name
	}
	return number
}

// userDisplayLabel returns a human-friendly label for a user: Name if set,
// else the email local-part, else the bare email. Nil user returns "".
func userDisplayLabel(u *auth.User) string {
	if u == nil {
		return ""
	}
	if u.Name != "" {
		return u.Name
	}
	if at := strings.IndexByte(u.Email, '@'); at > 0 {
		return u.Email[:at]
	}
	return u.Email
}

// householdNumbers returns the set of phone numbers belonging to the
// authenticated user's household. Returns nil if the user has no household.
func (h *Handler) householdNumbers(r *http.Request) map[string]bool {
	user := auth.UserFromContext(r.Context())
	if user == nil || h.householdStore == nil {
		return nil
	}
	households, err := h.householdStore.GetForUser(r.Context(), user.ID)
	if err != nil || len(households) == 0 {
		return nil
	}
	lines, err := h.lineStore.ListByHousehold(r.Context(), households[0].ID)
	if err != nil {
		return nil
	}
	nums := make(map[string]bool, len(lines))
	for _, l := range lines {
		nums[l.Number] = true
	}
	return nums
}

// householdContext returns the household name, call-history flag, and timezone location for the current user.
func (h *Handler) householdContext(r *http.Request) (name string, callHistory bool, loc *time.Location) {
	if h.householdStore == nil {
		return "", false, time.UTC
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		return "", false, time.UTC
	}
	households, err := h.householdStore.GetForUser(r.Context(), user.ID)
	if err != nil || len(households) == 0 {
		return "", false, time.UTC
	}
	return households[0].Name, households[0].CallHistoryEnabled, households[0].Location()
}

func (h *Handler) callHistoryEnabled(r *http.Request) bool {
	_, ch, _ := h.householdContext(r)
	return ch
}

func (h *Handler) householdNameFromContext(r *http.Request) string {
	name, _, _ := h.householdContext(r)
	return name
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

func renderWith(w http.ResponseWriter, t *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template render failed", "template", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// layoutFor returns the layout template name for the current user's theme.
// Falls back to direction C when no theme is set (unauthenticated or new user).
func layoutFor(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil {
		switch u.Theme {
		case auth.ThemeDialup:
			return "layout-dialup.html"
		case auth.ThemeAnsweringMachine:
			return "layout-answering-machine.html"
		}
	}
	return "layout-v2.html"
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

