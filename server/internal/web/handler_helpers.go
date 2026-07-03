package web

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/updates"
	"github.com/justinlindh/digits/server/internal/version"
)

// requireLineOwnership looks up a line by its number and verifies the
// authenticated user's household owns it. Returns the line on success, or
// nil after writing a 404. Unauthenticated, unknown, and unauthorized
// responses are intentionally indistinguishable to avoid leaking whether a
// given number exists.
func (h *Handler) requireLineOwnership(w http.ResponseWriter, r *http.Request, number string) *line.Line {
	ln, _ := h.requireLineOwnershipWithHousehold(w, r, number)
	return ln
}

// requireLineOwnershipAdmin is requireLineOwnership with an additional admin
// role check. Used for destructive phone endpoints (delete, factory reset,
// restart, update, pair, add line).
func (h *Handler) requireLineOwnershipAdmin(w http.ResponseWriter, r *http.Request, number string) *line.Line {
	ln, hh := h.requireLineOwnershipWithHousehold(w, r, number)
	if ln == nil {
		return nil
	}
	if !h.isHouseholdAdmin(r, hh.ID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil
	}
	return ln
}

// isHouseholdAdmin reports whether the request's user is an admin of the given
// household. Best-effort: returns false on any lookup error or missing user.
// Used to gate the rendering of admin-only UI (the result is advisory; the
// POST handlers still enforce admin via requireLineOwnershipAdmin).
func (h *Handler) isHouseholdAdmin(r *http.Request, householdID string) bool {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		return false
	}
	role, err := h.householdStore.GetRole(r.Context(), user.ID, householdID)
	return err == nil && role == "admin"
}

// requireLineOwnershipWithHousehold is requireLineOwnership plus the matched
// household value, for callers that need the household state (e.g., DND)
// without an extra DB round-trip. On any failure it writes 404 and returns
// (nil, nil); the auth/lookup behavior is identical to requireLineOwnership.
func (h *Handler) requireLineOwnershipWithHousehold(w http.ResponseWriter, r *http.Request, number string) (*line.Line, *household.Household) {
	ln, err := h.lineStore.GetByNumber(r.Context(), number)
	if err != nil {
		http.NotFound(w, r)
		return nil, nil
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return nil, nil
	}
	if h.householdStore == nil {
		http.NotFound(w, r)
		return nil, nil
	}
	households, err := h.householdStore.GetForUser(r.Context(), user.ID)
	if err != nil || len(households) == 0 {
		http.NotFound(w, r)
		return nil, nil
	}
	for _, hh := range households {
		if hh.ID == ln.HouseholdID {
			return ln, hh
		}
	}
	http.NotFound(w, r)
	return nil, nil
}

// ownedLinesForUser returns the lines owned by any household the user belongs
// to, keyed by number, plus the primary household. Returns (nil, nil, false)
// if the user has no households or any lookup fails. Caller writes 404, same
// response shape as nonexistent.
func (h *Handler) ownedLinesForUser(ctx context.Context, user *auth.User) (map[string]*line.Line, *household.Household, bool) {
	households, err := h.householdStore.GetForUser(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "ownedLinesForUser: list households failed", "user_id", user.ID, "err", err)
		return nil, nil, false
	}
	if len(households) == 0 {
		return nil, nil, false
	}
	lines := make(map[string]*line.Line)
	for _, hh := range households {
		hhLines, err := h.lineStore.ListByHousehold(ctx, hh.ID)
		if err != nil {
			slog.ErrorContext(ctx, "ownedLinesForUser: list lines failed", "household_id", hh.ID, "err", err)
			return nil, nil, false
		}
		for i := range hhLines {
			ln := hhLines[i]
			lines[ln.Number] = &ln
		}
	}
	return lines, households[0], true
}

// parseCallID reads the "id" path value and parses it as a positive call ID.
// On a malformed or non-positive value it writes 404 (the same response the
// ownership check gives a nonexistent call) and returns (0, false), so call
// handlers can guard with a single `if !ok { return }`.
func parseCallID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	callID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || callID <= 0 {
		http.NotFound(w, r)
		return 0, false
	}
	return callID, true
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
func (h *Handler) requireCallEndpointOwnership(w http.ResponseWriter, r *http.Request, callID int64) (calls.Call, map[string]*line.Line, *household.Household, bool) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return calls.Call{}, nil, nil, false
	}

	// Always do both queries in the same order, regardless of miss reason.
	ownedLines, primaryHH, ok := h.ownedLinesForUser(r.Context(), user)
	call, callErr := h.tracker.GetCall(r.Context(), callID)

	if callErr != nil {
		slog.ErrorContext(r.Context(), "link_health: get call failed", "call_id", callID, "err", callErr)
		http.NotFound(w, r)
		return calls.Call{}, nil, nil, false
	}
	if !ok || call.ID == 0 {
		http.NotFound(w, r)
		return calls.Call{}, nil, nil, false
	}

	_, ownsCaller := ownedLines[call.Caller]
	_, ownsCallee := ownedLines[call.Callee]
	if !ownsCaller && !ownsCallee {
		http.NotFound(w, r)
		return calls.Call{}, nil, nil, false
	}
	return call, ownedLines, primaryHH, true
}

// loadConferenceForUser runs the constant-time dual-query sequence the
// conference auth helpers share: snapshot the user's owned lines and fetch
// the conference, both unconditionally, then 404 on any failure. Callers
// layer their own ownership predicate on the returned (conf, ownedLines).
func (h *Handler) loadConferenceForUser(w http.ResponseWriter, r *http.Request, confID uuid.UUID, errLog string) (*calls.ConferenceSummary, map[string]*line.Line, *household.Household, bool) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return nil, nil, nil, false
	}
	ownedLines, primaryHH, ok := h.ownedLinesForUser(r.Context(), user)
	conf, confErr := h.tracker.GetConferenceByID(r.Context(), confID)
	if confErr != nil {
		slog.ErrorContext(r.Context(), errLog+": get conference failed", "conf_id", confID, "err", confErr)
		http.NotFound(w, r)
		return nil, nil, nil, false
	}
	if !ok || conf == nil {
		http.NotFound(w, r)
		return nil, nil, nil, false
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
func (h *Handler) requireConferenceOwnership(w http.ResponseWriter, r *http.Request, confID uuid.UUID) (*calls.ConferenceSummary, map[string]*line.Line, *household.Household, bool) {
	conf, ownedLines, primaryHH, ok := h.loadConferenceForUser(w, r, confID, "conference_link_health")
	if !ok {
		return nil, nil, nil, false
	}
	for _, member := range conf.Members {
		if _, owns := ownedLines[member]; owns {
			return conf, ownedLines, primaryHH, true
		}
	}
	http.NotFound(w, r)
	return nil, nil, nil, false
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
func (h *Handler) requireConferenceHostOwnership(w http.ResponseWriter, r *http.Request, confID uuid.UUID) (*calls.ConferenceSummary, map[string]*line.Line, *household.Household, bool) {
	conf, ownedLines, primaryHH, ok := h.loadConferenceForUser(w, r, confID, "conference_kick")
	if !ok {
		return nil, nil, nil, false
	}
	if _, owns := ownedLines[conf.Host]; !owns {
		http.NotFound(w, r)
		return nil, nil, nil, false
	}
	return conf, ownedLines, primaryHH, true
}

// nameResolver labels a phone number for display. It bundles the two maps
// (the user's owned lines and the linked-families index) that the link-health
// handlers always compute together and thread down to their build/render
// helpers purely to name endpoints. Construct one at the top of a handler and
// pass it down instead of the two maps.
type nameResolver struct {
	ownedLines  map[string]*line.Line
	linkedIndex map[string]string
}

// display picks the best label for a member phone. Priority: owned-line name
// (only if non-empty), linked-index peer name, bare number fallback.
func (nr nameResolver) display(number string) string {
	if ln, ok := nr.ownedLines[number]; ok && ln != nil && ln.Name != "" {
		return ln.Name
	}
	if name := resolvePeerName(number, nr.linkedIndex); name != "" {
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
	hh := h.activeHousehold(r)
	if hh == nil {
		return nil
	}
	lines, err := h.lineStore.ListByHousehold(r.Context(), hh.ID)
	if err != nil {
		return nil
	}
	nums := make(map[string]bool, len(lines))
	for _, l := range lines {
		nums[l.Number] = true
	}
	return nums
}

// resolveActiveHousehold returns the user's full household list along with
// the entry currently selected as active. The active entry is the one whose
// ID matches the session cookie's active_household_id; on any miss (cookie
// absent, ID unset, ID not in the list) it falls back to households[0].
// Returns (nil, nil) when the request has no authenticated user, the
// household store is unset, GetForUser fails, or the user has no
// households.
func (h *Handler) resolveActiveHousehold(r *http.Request) (*household.Household, []*household.Household) {
	if h.householdStore == nil {
		return nil, nil
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		return nil, nil
	}
	households, err := h.householdStore.GetForUser(r.Context(), user.ID)
	if err != nil || len(households) == 0 {
		return nil, nil
	}
	active := households[0]
	cookie, cookieErr := r.Cookie(auth.CookieName)
	if cookieErr == nil && h.authStore != nil {
		activeID, err := h.authStore.ActiveHouseholdID(r.Context(), cookie.Value)
		if err != nil {
			slog.WarnContext(r.Context(), "active household lookup failed", "user", user.ID, "err", err)
		} else if activeID != "" {
			for _, hh := range households {
				if hh.ID == activeID {
					active = hh
					break
				}
			}
		}
	}
	return active, households
}

// activeHousehold returns the household the user is currently viewing. Reads
// active_household_id from the session; falls back to the first household
// when unset or when the user is no longer a member.
func (h *Handler) activeHousehold(r *http.Request) *household.Household {
	active, _ := h.resolveActiveHousehold(r)
	return active
}

// requireHouseholdAdmin checks that the requesting user holds the "admin" role
// in the active household. Returns (user, household, true) on success. On
// failure it writes an appropriate redirect or 403 and returns (nil, nil, false).
func (h *Handler) requireHouseholdAdmin(w http.ResponseWriter, r *http.Request) (*auth.User, *household.Household, bool) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return nil, nil, false
	}
	hh := h.activeHousehold(r)
	if hh == nil {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return nil, nil, false
	}
	role, err := h.householdStore.GetRole(r.Context(), user.ID, hh.ID)
	if err != nil || role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, nil, false
	}
	return user, hh, true
}

// chromeData holds the fields every protected page-data struct shares for
// rendering the layout chrome (sidebar, nav, DND chip, version pill).
type chromeData struct {
	Page       string
	Version    string
	User       *auth.User
	Household  *household.Household
	Households []*household.Household
	HasUpdates bool
	allSilent  bool
}

func (c chromeData) HouseholdName() string {
	if c.Household == nil {
		return ""
	}
	return c.Household.Name
}

func (c chromeData) HouseholdDND() bool {
	return c.allSilent
}

// OverviewActive reports whether the Overview nav item should highlight.
// The pairing page and per-line detail pages (Page "phones") live under the
// Overview now that the standalone Lines nav item is gone.
func (c chromeData) OverviewActive() bool {
	return c.Page == "dashboard" || c.Page == "phones"
}

func (c chromeData) CallHistoryEnabled() bool {
	if c.Household == nil {
		return false
	}
	return c.Household.CallHistoryEnabled
}

func newChromeData(page string, user *auth.User, hh *household.Household) chromeData {
	return chromeData{
		Page:      page,
		Version:   version.Version,
		User:      user,
		Household: hh,
	}
}

func (h *Handler) newChromeDataWithHouseholds(r *http.Request, page string) chromeData {
	active, households := h.resolveActiveHousehold(r)
	cd := newChromeData(page, auth.UserFromContext(r.Context()), active)
	cd.Households = households
	if active != nil {
		cd.HasUpdates = h.hasPhoneUpdates(r.Context(), active.ID)
		if h.lineStore != nil {
			silent, err := h.lineStore.AllSilentByHousehold(r.Context(), active.ID)
			if err != nil {
				slog.ErrorContext(r.Context(), "check all-silent failed", "household_id", active.ID, "err", err)
			}
			cd.allSilent = silent
		}
	}
	return cd
}

// hasPhoneUpdates returns true if any phone in the household is behind the
// latest pi or firmware release.
func (h *Handler) hasPhoneUpdates(ctx context.Context, householdID string) bool {
	if h.releases == nil {
		return false
	}
	idx := h.releases.ReleaseIndex()
	if idx == nil {
		return false
	}
	latestPi := idx.Pi.Latest
	latestFw := idx.Firmware.Latest
	if latestPi == "" && latestFw == "" {
		return false
	}
	if h.lineStore == nil {
		return false
	}
	lines, err := h.lineStore.ListByHousehold(ctx, householdID)
	if err != nil {
		return false
	}
	lineNumbers := make([]string, len(lines))
	for i, l := range lines {
		lineNumbers[i] = l.Number
	}
	for _, number := range lineNumbers {
		for _, info := range h.hub.AllDeviceInfo(number) {
			if latestPi != "" && info.PiVersion != "" && updates.CompareSemver(info.PiVersion, latestPi) < 0 {
				return true
			}
			if latestFw != "" && info.FirmwareVersion != "" && updates.CompareSemver(info.FirmwareVersion, latestFw) < 0 {
				return true
			}
		}
	}
	return false
}

// parseForm calls r.ParseForm and writes a 400 on failure. Returns true on
// success so callers can guard with `if !parseForm(w, r) { return }`.
func parseForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	return true
}

func jsonError(ctx context.Context, w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.ErrorContext(ctx, "jsonError: encode failed", "err", err)
	}
}

// writeJSON sets the JSON content type and encodes v to the response body with
// an implicit 200 status. It is the success-path counterpart to jsonError;
// callers log the returned error with their own request context.
func writeJSON(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

func renderWith(ctx context.Context, w http.ResponseWriter, t *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		slog.ErrorContext(ctx, "template render failed", "template", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// renderWithStatus is renderWith with an explicit non-200 status. Headers must
// be set before WriteHeader, so we can't reuse renderWith after the caller has
// already written the status line.
func renderWithStatus(ctx context.Context, w http.ResponseWriter, t *template.Template, name string, data any, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		slog.ErrorContext(ctx, "template render failed", "template", name, "err", err)
	}
}

// layoutFor returns the layout template name for the current user's theme.
// Falls back to the intercom layout when no theme is set (unauthenticated or new user).
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

// partialFor picks an htmx partial template name based on the current user's
// theme. Returns the AM partial when the user is on the AM theme, else the
// intercom partial.
func partialFor(r *http.Request, intercom, am string) string {
	if u := auth.UserFromContext(r.Context()); u != nil && u.Theme == auth.ThemeAnsweringMachine {
		return am
	}
	return intercom
}

// linkedFamilyRow holds the display data for one linked household and its lines.
// Used by the dashboard, links, and link-health handlers to render linked-family
// information without repeated store round-trips.
type linkedFamilyRow struct {
	ID         string
	Name       string
	Lines      []line.Line
	Status     string
	AcceptedAt *time.Time
}

// buildLinkedFamilies fetches the list of linked households and their lines
// for the given householdID. Returns nil if householdID is empty or the lookup fails.
func (h *Handler) buildLinkedFamilies(ctx context.Context, householdID string) []linkedFamilyRow {
	if householdID == "" || h.linkStore == nil {
		return nil
	}
	activeLinks, err := h.linkStore.GetLinkedHouseholds(ctx, householdID)
	if err != nil {
		slog.ErrorContext(ctx, "buildLinkedFamilies: get linked households failed", "err", err)
		return nil
	}
	otherIDs := make([]string, 0, len(activeLinks))
	for _, l := range activeLinks {
		otherID := l.HouseholdAID
		if otherID == householdID && l.HouseholdBID != nil {
			otherID = *l.HouseholdBID
		}
		otherIDs = append(otherIDs, otherID)
	}
	linesByHousehold, err := h.lineStore.ListByHouseholds(ctx, otherIDs)
	if err != nil {
		slog.ErrorContext(ctx, "buildLinkedFamilies: batch list lines failed", "err", err)
	}
	var families []linkedFamilyRow
	for i, l := range activeLinks {
		otherID := otherIDs[i]
		otherName := otherID
		if other, err := h.householdStore.GetByID(ctx, otherID); err == nil {
			otherName = other.Name
		}
		families = append(families, linkedFamilyRow{
			ID:         l.ID,
			Name:       otherName,
			Lines:      linesByHousehold[otherID],
			Status:     l.Status,
			AcceptedAt: l.AcceptedAt,
		})
	}
	return families
}

// buildLinkedLineIndex flattens a slice of linkedFamilyRow into a map from
// line number to "FamilyName · LineName" for fast peer-name resolution.
func buildLinkedLineIndex(families []linkedFamilyRow) map[string]string {
	index := make(map[string]string)
	for _, f := range families {
		for _, l := range f.Lines {
			index[l.Number] = f.Name + " · " + l.Name
		}
	}
	return index
}

// linkedIndexForHousehold builds the linked-line display-name index for the
// caller's primary household, or returns nil when hh is nil (the caller owns
// no household). The link-health endpoints share this so the "resolve once per
// request" rule lives in one place.
func (h *Handler) linkedIndexForHousehold(ctx context.Context, hh *household.Household) map[string]string {
	if hh == nil {
		return nil
	}
	return buildLinkedLineIndex(h.buildLinkedFamilies(ctx, hh.ID))
}

// resolvePeerName returns the friendly name for a peer number using the linked
// line index, falling back to fmtPhone formatting.
func resolvePeerName(number string, linkedLines map[string]string) string {
	if name, ok := linkedLines[number]; ok {
		return name
	}
	return line.FormatNumber(number)
}
