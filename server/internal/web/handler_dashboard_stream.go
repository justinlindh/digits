package web

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/household"
)

// dashTickInterval is how often the dashboard SSE handler re-renders even
// when no Notify has fired. Two purposes: (a) the LOCAL TIME LED tracks
// real time without needing per-second wakes, (b) the periodic frame doubles
// as a keep-alive so proxies don't close idle streams.
const dashTickInterval = 15 * time.Second

// handleDashboardStream opens an SSE stream for the AM-theme top-row
// counters and clock. Sends one initial "status" event with the current
// snapshot, then a fresh snapshot on every Notify from the dashboard
// broadcaster (call start/end, line online/offline) and at least once
// per dashTickInterval so the clock advances.
//
// Auth: protected mux already gates this. Household scope is derived from
// the user's primary household, mirroring handleDashboard.
func (h *Handler) handleDashboardStream(w http.ResponseWriter, r *http.Request) {
	if h.dashEvents == nil {
		// Broadcaster not wired (test harness or misconfig); 404 keeps the
		// surface invisible rather than serving a stream that never updates.
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return
	}
	hh := h.activeHousehold(r)

	flusher, ok := startSSE(w, r)
	if !ok {
		return
	}

	if err := h.writeDashStatus(w, flusher, r, hh); err != nil {
		return
	}

	sub, unsub := h.dashEvents.Subscribe()
	defer unsub()

	tick := time.NewTicker(dashTickInterval)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub:
		case <-tick.C:
		}
		if err := h.writeDashStatus(w, flusher, r, hh); err != nil {
			return
		}
	}
}

func (h *Handler) writeDashStatus(w http.ResponseWriter, flusher http.Flusher, r *http.Request, hh *household.Household) error {
	vm := h.computeDashStatus(r, hh)
	fragment, err := h.renderDashStatus(vm)
	if err != nil {
		slog.ErrorContext(r.Context(), "dashboard SSE: render failed", "err", err)
		return err
	}
	if err := writeSSE(w, "status", fragment); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// computeDashStatus snapshots the household-scoped counters and the
// household-local clock for the top-row partial. Same scoping rules as
// handleDashboard: only the user's own lines count toward ON·CALL and
// LINES·ONLINE; FAMILIES is the count of accepted household links.
func (h *Handler) computeDashStatus(r *http.Request, hh *household.Household) dashStatusVM {
	ld := h.buildLinesData(r, hh, "")

	ownNums := make(map[string]struct{}, len(ld.Lines))
	onlineCount := 0
	for _, l := range ld.Lines {
		ownNums[l.Line.Number] = struct{}{}
		if l.Online {
			onlineCount++
		}
	}

	activeCount := 0
	for _, pair := range h.tracker.Active() {
		if _, ok := ownNums[pair.Caller]; ok {
			activeCount++
			continue
		}
		if _, ok := ownNums[pair.Callee]; ok {
			activeCount++
		}
	}

	var householdID string
	if hh != nil {
		householdID = hh.ID
	}
	families := len(h.buildLinkedFamilies(r.Context(), householdID))

	// Household.Location() is nil-safe and falls back to UTC, so we deliberately
	// skip an explicit hh != nil guard here even though hh.ID above takes one.
	return dashStatusVM{
		ActiveCalls:    activeCount,
		OnlineLines:    onlineCount,
		LinkedFamilies: families,
		Now:            time.Now().In(hh.Location()),
	}
}

func (h *Handler) renderDashStatus(vm dashStatusVM) (string, error) {
	var buf bytes.Buffer
	if err := h.tmplDashboardAMStatus.ExecuteTemplate(&buf, "dashboard-am-status", vm); err != nil {
		return "", fmt.Errorf("render dashboard-am-status: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}
