package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
)

type dashboardData struct {
	chromeData
	Lines              []lineRow
	CallsTodayRecent   []callRow
	CallsTodayTotalMin int
	LinkedFamilies     []linkedFamilyRow
	Now                time.Time
	ActiveLine         string
	ActivePeer         string
	ActiveElapsed      string
	// Status is the subset rendered by the dashboard-am-status partial. The
	// partial is also rendered by the /api/dashboard/stream SSE handler, so
	// this struct is the contract between the page render and stream render.
	Status dashStatusVM
}

// dashStatusVM is what the dashboard-am-status partial reads. Both the page
// handler and the SSE handler populate it the same way so the partial output
// is identical at render time and at every subsequent SSE swap.
type dashStatusVM struct {
	ActiveCalls    int
	OnlineLines    int
	LinkedFamilies int
	Now            time.Time
}

type callRow struct {
	StartedAt time.Time
	LineName  string
	PeerName  string
	Direction string // "in" or "out"
	DurationS int
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
	hh := h.activeHousehold(r)
	ld := h.buildLinesData(r, hh, "")
	loc := hh.Location()
	now := time.Now().In(loc)

	callHistoryEnabled := hh != nil && hh.CallHistoryEnabled

	// Determine current household ID for linked-family lookup.
	var householdID string
	if hh != nil {
		householdID = hh.ID
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
			if id, ok := h.tracker.CallIDFor(callerRow.Line.Number); ok {
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
			if id, ok := h.tracker.CallIDFor(calleeRow.Line.Number); ok {
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
		recent, err := h.tracker.RecentForPhones(ctx, ownNumbers, 20)
		if err != nil {
			slog.WarnContext(ctx, "dashboard: recent calls lookup failed", "err", err)
		}
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
		chromeData:         h.newChromeDataWithHouseholds(r, "dashboard"),
		Lines:              ld.Lines,
		CallsTodayRecent:   callsTodayRecent,
		CallsTodayTotalMin: (callsTodayTotalSec + 30) / 60, // +30 to round to nearest minute
		LinkedFamilies:     linkedFamilies,
		Now:                now,
		ActiveLine:         activeLine,
		ActivePeer:         activePeer,
		ActiveElapsed:      activeElapsed,
		Status: dashStatusVM{
			ActiveCalls:    activeCount,
			OnlineLines:    countOnline(ld.Lines),
			LinkedFamilies: len(linkedFamilies),
			Now:            now,
		},
	}
	renderWith(r.Context(), w, h.tmplDashboard, layoutFor(r), data)
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
	h, m, s := clockParts(int(d.Seconds()))
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

type connectingData struct {
	chromeData
}

func (h *Handler) handleConnecting(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil || user.Theme != auth.ThemeDialup {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	renderWith(r.Context(), w, h.tmplConnecting, "connecting.html", connectingData{
		chromeData: h.newChromeDataWithHouseholds(r, "connecting"),
	})
}
