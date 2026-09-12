package web

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/admin"
	"github.com/justinlindh/digits/server/internal/auth"
)

// adminWindow is the lookback for every "recent" figure on the admin page.
const adminWindow = 7 * 24 * time.Hour

// requireAdmin gates a route behind the configured admin allowlist. It runs
// inside RequireAuth, so the session is already validated; this only checks
// that the signed-in email is on the list. Every failure is a 404 rather
// than a 403 so the page's existence is never advertised to non-admins.
func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		if user == nil || !isAdminEmail(h.cfg.AdminEmails, user.Email) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isAdminEmail reports whether email is on the allowlist, comparing
// case-insensitively. An empty allowlist matches nothing.
func isAdminEmail(allow []string, email string) bool {
	for _, a := range allow {
		if strings.EqualFold(a, email) {
			return true
		}
	}
	return false
}

type adminData struct {
	chromeData
	WindowDays  int
	Timezone    string
	Totals      admin.Totals
	OnlineLines int
	ActiveCalls int
	Accounts    []admin.Account
	Households  []adminHouseholdRow
	Days        []admin.DayCount
}

// adminHouseholdRow adds the live online-line count to a stored household.
type adminHouseholdRow struct {
	admin.Household
	Online int
}

func (h *Handler) handleAdmin(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadAdminData(r)
	if err != nil {
		slog.ErrorContext(r.Context(), "admin: load failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// Always the intercom layout: this is an operator view, not a themed
	// household surface, and only one variant of the page exists.
	renderWith(r.Context(), w, h.tmplAdmin, "layout-v2.html", data)
}

// loadAdminData assembles the page model. Timestamps are localized to the
// admin's active household so the per-day buckets and the displayed dates
// agree with each other.
func (h *Handler) loadAdminData(r *http.Request) (adminData, error) {
	ctx := r.Context()
	now := time.Now()
	since := now.Add(-adminWindow)
	loc := h.activeHousehold(r).Location()

	data := adminData{
		chromeData: h.newChromeDataWithHouseholds(r, "admin"),
		WindowDays: int(adminWindow.Hours() / 24),
		Timezone:   loc.String(),
	}

	online := map[string]bool{}
	if h.hub != nil {
		for _, n := range h.hub.OnlineNumbers() {
			online[n] = true
		}
	}
	data.OnlineLines = len(online)
	if h.tracker != nil {
		data.ActiveCalls = len(h.tracker.Active(ctx))
	}
	if h.adminStore == nil {
		return data, nil
	}

	var err error
	if data.Totals, err = h.adminStore.Totals(ctx, since); err != nil {
		return adminData{}, err
	}
	if data.Accounts, err = h.adminStore.Accounts(ctx); err != nil {
		return adminData{}, err
	}
	for i := range data.Accounts {
		a := &data.Accounts[i]
		a.CreatedAt = a.CreatedAt.In(loc)
		if a.LastLoginAt != nil {
			t := a.LastLoginAt.In(loc)
			a.LastLoginAt = &t
		}
	}
	households, err := h.adminStore.Households(ctx, since)
	if err != nil {
		return adminData{}, err
	}
	for _, hh := range households {
		hh.CreatedAt = hh.CreatedAt.In(loc)
		row := adminHouseholdRow{Household: hh}
		for _, n := range hh.Lines {
			if online[n] {
				row.Online++
			}
		}
		data.Households = append(data.Households, row)
	}
	if data.Days, err = h.adminStore.CallsPerDay(ctx, since, now, loc); err != nil {
		return adminData{}, err
	}
	return data, nil
}
