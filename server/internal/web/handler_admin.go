package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/admin"
	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
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
	Devices     []adminDeviceRow
	Days        []admin.DayCount
	// Latest released versions from the release index; empty when the
	// index is unavailable, in which case no device is flagged behind.
	LatestPiVersion       string
	LatestFirmwareVersion string
}

// adminHouseholdRow adds the live online-line count to a stored household.
type adminHouseholdRow struct {
	admin.Household
	Online int
}

// adminDeviceRow is one line's connected-device view: the oldest reported
// Pi and firmware versions across its devices, flagged when behind the
// latest release. Versions are only known while a device is connected.
type adminDeviceRow struct {
	Number          string
	Name            string
	Household       string
	Online          bool
	PiVersion       string
	PiBehind        bool
	FirmwareVersion string
	FirmwareBehind  bool
}

// adminDeviceRows builds one row per line in the order given. households
// maps household ID to display name; infoFor returns the connected-device
// snapshots for a line number. An empty latest version never flags a row.
func adminDeviceRows(lines []line.Line, households map[string]string, infoFor func(string) []signaling.DeviceInfoSnapshot, latestPi, latestFw string) []adminDeviceRow {
	rows := make([]adminDeviceRow, 0, len(lines))
	for _, l := range lines {
		infos := infoFor(l.Number)
		pi, fw := oldestVersions(infos)
		rows = append(rows, adminDeviceRow{
			Number:          l.Number,
			Name:            l.Name,
			Household:       households[l.HouseholdID],
			Online:          len(infos) > 0,
			PiVersion:       pi,
			PiBehind:        pi != "" && latestPi != "" && updates.CompareSemver(pi, latestPi) < 0,
			FirmwareVersion: fw,
			FirmwareBehind:  fw != "" && latestFw != "" && updates.CompareSemver(fw, latestFw) < 0,
		})
	}
	return rows
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
	householdNames := make(map[string]string, len(households))
	for _, hh := range households {
		householdNames[hh.ID] = hh.Name
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

	if h.releases != nil {
		if idx := h.releases.ReleaseIndex(); idx != nil {
			data.LatestPiVersion = idx.Pi.Latest
			data.LatestFirmwareVersion = idx.Firmware.Latest
		}
	}
	if h.lineStore != nil && h.hub != nil {
		lines, err := h.lineStore.List(ctx)
		if err != nil {
			return adminData{}, err
		}
		data.Devices = adminDeviceRows(lines, householdNames, h.hub.AllDeviceInfo, data.LatestPiVersion, data.LatestFirmwareVersion)
	}
	return data, nil
}

func (h *Handler) handleAdminAccountDisable(w http.ResponseWriter, r *http.Request) {
	h.setAccountDisabled(w, r, true)
}

func (h *Handler) handleAdminAccountEnable(w http.ResponseWriter, r *http.Request) {
	h.setAccountDisabled(w, r, false)
}

// setAccountDisabled is the shared body of the disable and enable actions.
// It runs behind requireAdmin. An admin cannot disable their own account:
// that would delete the session making the request and could lock every
// admin out if the allowlist has one entry.
func (h *Handler) setAccountDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	ctx := r.Context()
	admin := auth.UserFromContext(ctx)
	targetID := r.PathValue("id")
	if disabled && admin != nil && admin.ID == targetID {
		http.Error(w, "cannot disable your own account", http.StatusBadRequest)
		return
	}
	if err := h.authStore.SetDisabled(ctx, targetID, disabled); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.ErrorContext(ctx, "admin: set account disabled failed", "target_user_id", targetID, "disabled", disabled, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	slog.InfoContext(ctx, "admin: account disabled state changed", "admin_user_id", admin.ID, "target_user_id", targetID, "disabled", disabled)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
