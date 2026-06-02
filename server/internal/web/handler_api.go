package web

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/version"
)

func (h *Handler) handleInternalStats(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AdminSecret == "" ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Secret")), []byte(h.cfg.AdminSecret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	lineCount := 0
	if h.lineStore != nil {
		lines, err := h.lineStore.List(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "stats: list lines failed", "err", err)
			jsonError(r.Context(), w, "internal server error", http.StatusInternalServerError)
			return
		}
		lineCount = len(lines)
	}

	onlineCount := 0
	if h.hub != nil {
		onlineCount = len(h.hub.OnlineNumbers())
	}

	activeCallCount := 0
	if h.tracker != nil {
		activeCallCount = len(h.tracker.Active(r.Context()))
	}

	totalUsers := 0
	if h.authStore != nil {
		var err error
		totalUsers, err = h.authStore.CountUsers(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "stats: count users failed", "err", err)
			jsonError(r.Context(), w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	totalHouseholds := 0
	if h.householdStore != nil {
		var err error
		totalHouseholds, err = h.householdStore.CountHouseholds(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "stats: count households failed", "err", err)
			jsonError(r.Context(), w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	totalLinks := 0
	if h.linkStore != nil {
		var err error
		totalLinks, err = h.linkStore.CountActiveLinks(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "stats: count active links failed", "err", err)
			jsonError(r.Context(), w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"total_users":      totalUsers,
		"total_households": totalHouseholds,
		"total_lines":      lineCount,
		"online_lines":     onlineCount,
		"active_calls":     activeCallCount,
		"total_links":      totalLinks,
	}); err != nil {
		slog.ErrorContext(r.Context(), "stats: json encode failed", "err", err)
	}
}

func (h *Handler) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	hh := h.activeHousehold(r)
	ld := h.buildLinesData(r, hh, "")

	nums := make(map[string]bool, len(ld.Lines))
	var onlineCount int
	for _, row := range ld.Lines {
		nums[row.Line.Number] = true
		if row.Online {
			onlineCount++
		}
	}

	allActive := h.tracker.Active(r.Context())
	var activeCount int
	for _, a := range allActive {
		if nums[a.Caller] || nums[a.Callee] {
			activeCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"total_lines":  len(ld.Lines),
		"online_lines": onlineCount,
		"active_calls": activeCount,
	}); err != nil {
		slog.ErrorContext(r.Context(), "api status: json encode failed", "err", err)
	}
}

func (h *Handler) handleAPIActiveCalls(w http.ResponseWriter, r *http.Request) {
	nums := h.householdNumbers(r)
	allActive := h.tracker.Active(r.Context())
	var pairs []activePair
	for _, a := range allActive {
		if nums[a.Caller] || nums[a.Callee] {
			pairs = append(pairs, activePair{Caller: a.Caller, Callee: a.Callee})
		}
	}
	if pairs == nil {
		pairs = []activePair{}
	}

	// Return HTML for htmx, JSON for API clients
	if isHTMX(r) {
		renderWith(r.Context(), w, h.tmplActiveCalls, "active-calls-fragment", pairs)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(pairs); err != nil {
		slog.ErrorContext(r.Context(), "active calls: json encode failed", "err", err)
	}
}

func (h *Handler) handleAPINumberAvailable(w http.ResponseWriter, r *http.Request) {
	number := line.StripNumber(r.URL.Query().Get("number"))
	if err := line.ValidateNumber(number); err != nil {
		jsonError(r.Context(), w, err.Error(), http.StatusBadRequest)
		return
	}
	exists, err := h.lineStore.NumberExists(r.Context(), number)
	if err != nil {
		slog.ErrorContext(r.Context(), "number available check failed", "err", err)
		jsonError(r.Context(), w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]bool{"available": !exists}); err != nil {
		slog.ErrorContext(r.Context(), "number available: json encode failed", "err", err)
	}
}

func (h *Handler) handleAPIVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"version": version.Version,
		"commit":  version.Commit,
	}); err != nil {
		slog.ErrorContext(r.Context(), "api version: json encode failed", "err", err)
	}
}
