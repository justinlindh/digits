package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/version"
)

type dashboardData struct {
	Page               string
	Version            string
	CallHistoryEnabled bool
	HouseholdName      string
	HouseholdDND       bool
	Stats              dashStats
	Lines              []lineRow
	CallsTodayRecent   []callRow
	CallsTodayTotalMin int
	LinkedFamilies     []linkedFamilyRow
	User               *auth.User
	Now                time.Time
	ActiveLine         string
	ActivePeer         string
	ActiveElapsed      string
}

type callRow struct {
	StartedAt time.Time
	LineName  string
	PeerName  string
	Direction string // "in" or "out"
	DurationS int
}

type dashStats struct {
	TotalLines  int
	OnlineLines int
	ActiveCalls int
}

type activePair struct {
	Caller string
	Callee string
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	active := h.tracker.Active()
	ld := h.buildLinesData(r, "")
	user := auth.UserFromContext(r.Context())
	hhName, callHistoryEnabled, hhDND, loc := h.householdContext(r)
	now := time.Now().In(loc)

	// Determine current household ID for linked-family lookup.
	var householdID string
	if h.householdStore != nil && user != nil {
		if households, err := h.householdStore.GetForUser(r.Context(), user.ID); err == nil && len(households) > 0 {
			householdID = households[0].ID
		}
	}

	// Build set of own line numbers for active-call resolution and for
	// scoping call-history queries to this household.
	ownLineByNumber := make(map[string]*lineRow, len(ld.Lines))
	ownNumbers := make([]string, 0, len(ld.Lines))
	for i := range ld.Lines {
		ownLineByNumber[ld.Lines[i].Line.Number] = &ld.Lines[i]
		ownNumbers = append(ownNumbers, ld.Lines[i].Line.Number)
	}

	// Build linked-family index for peer-name resolution.
	linkedFamilies := h.buildLinkedFamilies(ctx, householdID)
	linkedLineIndex := buildLinkedLineIndex(linkedFamilies)

	// Annotate lines with active-call state and count household-scoped active
	// calls. When both sides of the call are own lines (intra-household),
	// each card uses the other local line's name as its peer instead of a
	// phone-number fallback.
	var activeCount int
	for _, pair := range active {
		if ownLineByNumber[pair.Caller] != nil || ownLineByNumber[pair.Callee] != nil {
			activeCount++
		}
		callerRow := ownLineByNumber[pair.Caller]
		calleeRow := ownLineByNumber[pair.Callee]
		elapsed := fmtElapsed(time.Since(pair.StartedAt))
		if callerRow != nil {
			callerRow.OnCall = true
			callerRow.OnCallElapsed = elapsed
			if calleeRow != nil {
				callerRow.OnCallPeerName = calleeRow.Line.Name
			} else {
				callerRow.OnCallPeerName = resolvePeerName(pair.Callee, linkedLineIndex)
			}
			if id, ok := h.tracker.CallIDFor(ctx, callerRow.Line.Number); ok {
				callerRow.OnCallID = id
			}
		}
		if calleeRow != nil {
			calleeRow.OnCall = true
			calleeRow.OnCallElapsed = elapsed
			if callerRow != nil {
				calleeRow.OnCallPeerName = callerRow.Line.Name
			} else {
				calleeRow.OnCallPeerName = resolvePeerName(pair.Caller, linkedLineIndex)
			}
			if id, ok := h.tracker.CallIDFor(ctx, calleeRow.Line.Number); ok {
				calleeRow.OnCallID = id
			}
		}
	}

	// Pluck the first household-scoped active call for the AM dashboard's
	// single session panel. Deterministic "first" by Lines order beats the
	// silent "last" that a template-side scan would produce.
	var activeLine, activePeer, activeElapsed string
	for _, lr := range ld.Lines {
		if lr.OnCall {
			activeLine = lr.Line.Name
			activePeer = lr.OnCallPeerName
			activeElapsed = lr.OnCallElapsed
			break
		}
	}

	// Build today's call rows when history is enabled. Scoped to this
	// household's own line numbers so one family never sees another's
	// activity. "Today" is relative to the household's configured timezone,
	// not server-UTC.
	var callsTodayRecent []callRow
	var callsTodayTotalSec int
	if callHistoryEnabled && len(ownNumbers) > 0 {
		recent, _ := h.tracker.RecentForPhones(ctx, ownNumbers, 20)
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		for _, c := range recent {
			if !c.StartedAt.After(today) {
				continue
			}
			var direction, lineName, peerNumber string
			if lr := ownLineByNumber[c.Caller]; lr != nil {
				direction = "out"
				lineName = lr.Line.Name
				peerNumber = c.Callee
			} else if lr := ownLineByNumber[c.Callee]; lr != nil {
				direction = "in"
				lineName = lr.Line.Name
				peerNumber = c.Caller
			} else {
				continue
			}
			callsTodayRecent = append(callsTodayRecent, callRow{
				StartedAt: c.StartedAt.In(loc),
				LineName:  lineName,
				PeerName:  resolvePeerName(peerNumber, linkedLineIndex),
				Direction: direction,
				DurationS: c.DurationS,
			})
			callsTodayTotalSec += c.DurationS
		}
	}

	data := dashboardData{
		Page:               "dashboard",
		Version:            version.Version,
		CallHistoryEnabled: callHistoryEnabled,
		HouseholdName:      hhName,
		HouseholdDND:       hhDND,
		Stats: dashStats{
			TotalLines:  len(ld.Lines),
			OnlineLines: countOnline(ld.Lines),
			ActiveCalls: activeCount,
		},
		Lines:              ld.Lines,
		CallsTodayRecent:   callsTodayRecent,
		CallsTodayTotalMin: (callsTodayTotalSec + 30) / 60, // +30 to round to nearest minute
		LinkedFamilies:     linkedFamilies,
		User:               user,
		Now:                now,
		ActiveLine:         activeLine,
		ActivePeer:         activePeer,
		ActiveElapsed:      activeElapsed,
	}
	renderWith(w, h.tmplDashboard, layoutFor(r), data)
}

// buildLinkedFamilies fetches the list of linked households and their lines
// for the given householdID. Returns an empty slice if householdID is empty or
// the lookup fails.
func (h *Handler) buildLinkedFamilies(ctx context.Context, householdID string) []linkedFamilyRow {
	if householdID == "" || h.linkStore == nil {
		return nil
	}
	activeLinks, err := h.linkStore.GetLinkedHouseholds(ctx, householdID)
	if err != nil {
		slog.Error("buildLinkedFamilies: get linked households failed", "err", err)
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
		slog.Error("buildLinkedFamilies: batch list lines failed", "err", err)
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

// resolvePeerName returns the friendly name for a peer number using the linked
// line index, falling back to fmtPhone formatting.
func resolvePeerName(number string, linkedLines map[string]string) string {
	if name, ok := linkedLines[number]; ok {
		return name
	}
	return line.FormatNumber(number)
}

func countOnline(lines []lineRow) int {
	n := 0
	for _, l := range lines {
		if l.Online {
			n++
		}
	}
	return n
}

// fmtElapsed renders a duration as "m:ss" for short calls and "h:mm:ss"
// for calls that run past an hour. Used by the Dashboard active-call callout.
func fmtElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}


type connectingData struct {
	Page          string
	Version       string
	HouseholdName string
	HouseholdDND  bool
	User          *auth.User
}

func (h *Handler) handleConnecting(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil || user.Theme != auth.ThemeDialup {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	hhName, _, hhDND, _ := h.householdContext(r)
	renderWith(w, h.tmplConnecting, "connecting.html", connectingData{
		Page:          "connecting",
		Version:       version.Version,
		HouseholdName: hhName,
		HouseholdDND:  hhDND,
		User:          user,
	})
}

// requireLineOwnership looks up a line by number and verifies the authenticated
// user's household owns it. Returns the line on success, or nil after writing
