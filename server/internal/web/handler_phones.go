package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
	"github.com/justinlindh/digits/server/internal/version"
)

type pairSuccess struct {
	Name            string
	FirmwareVersion string
}

type linesData struct {
	Page                  string
	Version               string
	CallHistoryEnabled    bool
	HouseholdName         string
	Lines                 []lineRow
	Error                 string
	PairError             string
	PairSuccess           *pairSuccess
	User                  *auth.User
	LatestPiVersion       string
	LatestFirmwareVersion string
}

type lineRow struct {
	Line                line.Line
	Online              bool
	OnCall              bool
	OnCallPeerName      string
	OnCallElapsed       string // "mm:ss" for the Dashboard room-card callout
	OnCallID            int64  // 0 when not on a call; otherwise the active call id
	DeviceInfo          *signaling.DeviceInfoSnapshot
	FirmwareUpdateNotes []updates.Release
	PiUpdateNotes       []updates.Release
}

func (h *Handler) buildLinesData(r *http.Request, errMsg string) linesData {
	var user *auth.User
	if r != nil {
		user = auth.UserFromContext(r.Context())
	}

	var lines []line.Line

	// Scope to household if user has one and householdStore is available
	if user != nil && h.householdStore != nil {
		households, err := h.householdStore.GetForUser(r.Context(), user.ID)
		if err == nil && len(households) > 0 {
			lines, _ = h.lineStore.ListByHousehold(r.Context(), households[0].ID)
		}
	}

	// If household lookup failed, show empty list rather than leaking all lines
	if lines == nil {
		lines = []line.Line{}
	}

	online := h.hub.OnlineNumbers()
	onlineSet := make(map[string]bool, len(online))
	for _, n := range online {
		onlineSet[n] = true
	}

	var (
		idx                *updates.ReleaseIndex
		latestPi, latestFw string
	)
	if h.Releases != nil {
		idx = h.Releases.ReleaseIndex()
	}
	if idx != nil {
		latestPi = idx.Pi.Latest
		latestFw = idx.Firmware.Latest
	}

	rows := make([]lineRow, len(lines))
	for i, l := range lines {
		info := h.hub.DeviceInfo(l.Number)
		row := lineRow{Line: l, Online: onlineSet[l.Number], DeviceInfo: info}
		if idx != nil && info != nil {
			if latestFw != "" && info.FirmwareVersion != "" && updates.CompareSemver(info.FirmwareVersion, latestFw) < 0 {
				row.FirmwareUpdateNotes = idx.RangeReleases(updates.ComponentFirmware, info.FirmwareVersion, latestFw)
			}
			if latestPi != "" && info.PiVersion != "" && updates.CompareSemver(info.PiVersion, latestPi) < 0 {
				row.PiUpdateNotes = idx.RangeReleases(updates.ComponentPi, info.PiVersion, latestPi)
			}
		}
		rows[i] = row
	}
	return linesData{
		Page:                  "phones",
		Version:               version.Version,
		CallHistoryEnabled:    h.callHistoryEnabled(r),
		HouseholdName:         h.householdNameFromContext(r),
		Lines:                 rows,
		Error:                 errMsg,
		User:                  user,
		LatestPiVersion:       latestPi,
		LatestFirmwareVersion: latestFw,
	}
}

func (h *Handler) handlePhonesGet(w http.ResponseWriter, r *http.Request) {
	data := h.buildLinesData(r, "")
	if pairedName := r.URL.Query().Get("paired"); pairedName != "" {
		data.PairSuccess = &pairSuccess{
			Name:            pairedName,
			FirmwareVersion: r.URL.Query().Get("fw"),
		}
	}
	renderWith(w, h.tmplPhones, layoutFor(r), data)
}

func (h *Handler) handlePhonesPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	number := line.StripNumber(strings.TrimSpace(r.FormValue("number")))
	name := strings.TrimSpace(r.FormValue("name"))

	// Get user's household to associate the new line
	var householdID string
	if h.householdStore != nil {
		user := auth.UserFromContext(r.Context())
		if user != nil {
			households, err := h.householdStore.GetForUser(r.Context(), user.ID)
			if err == nil && len(households) > 0 {
				householdID = households[0].ID
			}
		}
	}

	if err := line.ValidateNumber(number); err != nil {
		data := h.buildLinesData(r, err.Error())
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	_, err := h.lineStore.Add(r.Context(), number, name, householdID)
	data := h.buildLinesData(r, "")
	if err != nil {
		data = h.buildLinesData(r, err.Error())
	}

	if isHTMX(r) {
		renderWith(w, h.tmplPhones, "phones-table", data)
		return
	}
	if err != nil {
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}
	http.Redirect(w, r, "/phones", http.StatusSeeOther)
}

func (h *Handler) handlePhonesPairPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(r.FormValue("code"))
	number := line.StripNumber(strings.TrimSpace(r.FormValue("number")))
	name := strings.TrimSpace(r.FormValue("name"))

	if err := line.ValidateNumber(number); err != nil {
		data := h.buildLinesData(r, "")
		data.PairError = "invalid phone number: " + err.Error()
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	if h.pairingStore == nil {
		data := h.buildLinesData(r, "")
		data.PairError = "pairing is not enabled"
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	// Get user's household
	var householdID string
	if h.householdStore != nil {
		user := auth.UserFromContext(r.Context())
		if user != nil {
			households, err := h.householdStore.GetForUser(r.Context(), user.ID)
			if err == nil && len(households) > 0 {
				householdID = households[0].ID
			}
		}
	}
	if householdID == "" {
		data := h.buildLinesData(r, "")
		data.PairError = "no household found — please complete onboarding first"
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	token, hwID, err := h.pairingStore.ClaimDevice(r.Context(), code, number, name, householdID)
	if err != nil {
		data := h.buildLinesData(r, "")
		data.PairError = err.Error()
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	if hwID != "" {
		if err := h.hub.SendToHardware(hwID, &signaling.Message{
			Type:        signaling.TypePaired,
			DeviceToken: token,
			Number:      number,
		}); err != nil {
			slog.Warn("could not notify device of pairing", "hardware_id", hwID, "err", err)
		}
	}

	v := url.Values{}
	v.Set("paired", name)
	http.Redirect(w, r, "/phones?"+v.Encode(), http.StatusSeeOther)
}

type lineDetailData struct {
	Page                  string
	Version               string
	CallHistoryEnabled    bool
	HouseholdName         string
	Line                  line.Line
	Online                bool
	Devices               []device.Device
	DeviceInfo            *signaling.DeviceInfoSnapshot
	LastSeenAt            *time.Time
	LatestPiVersion       string
	LatestFirmwareVersion string
	PiReleases            []updates.Release
	FWReleases            []updates.Release
	PiUpdateNotes         []updates.Release
	FirmwareUpdateNotes   []updates.Release
	User                  *auth.User
}

func (h *Handler) handlePhoneDetail(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	online := h.hub.Get(number) != nil

	var devices []device.Device
	if h.deviceStore != nil {
		var err error
		devices, err = h.deviceStore.ListByLine(r.Context(), ln.ID)
		if err != nil {
			slog.Error("failed to list devices by line", "err", err, "line_id", ln.ID)
		}
	}

	// For online devices, use the real-time in-memory timestamp from the Hub.
	// For offline devices, fall back to the last disconnect time from the DB.
	var lastSeenAt *time.Time
	if online {
		lastSeenAt = h.hub.LastSeenAt(number)
	} else {
		for _, d := range devices {
			if d.LastSeenAt != nil && (lastSeenAt == nil || d.LastSeenAt.After(*lastSeenAt)) {
				lastSeenAt = d.LastSeenAt
			}
		}
	}

	var latestPi, latestFw string
	var piReleases, fwReleases []updates.Release
	var idx *updates.ReleaseIndex
	if h.Releases != nil {
		idx = h.Releases.ReleaseIndex()
	}
	if idx != nil {
		latestPi = idx.Pi.Latest
		latestFw = idx.Firmware.Latest
		piReleases = idx.SortedReleases(updates.ComponentPi)
		fwReleases = idx.SortedReleases(updates.ComponentFirmware)
	}

	hhName, callHistory, loc := h.householdContext(r)

	if lastSeenAt != nil {
		t := lastSeenAt.In(loc)
		lastSeenAt = &t
	}

	devInfo := h.hub.DeviceInfo(number)

	var piUpdateNotes, firmwareUpdateNotes []updates.Release
	if idx != nil && devInfo != nil {
		if latestPi != "" && devInfo.PiVersion != "" && updates.CompareSemver(devInfo.PiVersion, latestPi) < 0 {
			piUpdateNotes = idx.RangeReleases(updates.ComponentPi, devInfo.PiVersion, latestPi)
		}
		if latestFw != "" && devInfo.FirmwareVersion != "" && updates.CompareSemver(devInfo.FirmwareVersion, latestFw) < 0 {
			firmwareUpdateNotes = idx.RangeReleases(updates.ComponentFirmware, devInfo.FirmwareVersion, latestFw)
		}
	}

	user := auth.UserFromContext(r.Context())

	renderWith(w, h.tmplPhoneDetail, layoutFor(r), lineDetailData{
		Page:                  "phones",
		Version:               version.Version,
		CallHistoryEnabled:    callHistory,
		HouseholdName:         hhName,
		Line:                  *ln,
		Online:                online,
		Devices:               devices,
		DeviceInfo:            devInfo,
		LastSeenAt:            lastSeenAt,
		LatestPiVersion:       latestPi,
		LatestFirmwareVersion: latestFw,
		PiReleases:            piReleases,
		FWReleases:            fwReleases,
		PiUpdateNotes:         piUpdateNotes,
		FirmwareUpdateNotes:   firmwareUpdateNotes,
		User:                  user,
	})
}

func (h *Handler) handlePhoneOnline(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if h.requireLineOwnership(w, r, number) == nil {
		return
	}
	online := h.hub.Get(number) != nil
	if isHTMX(r) {
		renderWith(w, h.tmplPhoneDetail, "phone-status", struct{ Online bool }{online})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]bool{"online": online}); err != nil {
		slog.Error("encode online status failed", "err", err)
	}
}

func (h *Handler) handlePhoneEditGet(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	online := h.hub.Get(number) != nil
	renderWith(w, h.tmplPhones, "phone-edit-row", lineRow{Line: *ln, Online: online})
}

func (h *Handler) handlePhoneEditPost(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))

	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}

	if err := h.lineStore.Update(r.Context(), ln.ID, number, name); err != nil {
		slog.Error("line update failed", "err", err, "line_id", ln.ID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	data := h.buildLinesData(r, "")
	if isHTMX(r) {
		renderWith(w, h.tmplPhones, "phones-table", data)
		return
	}
	http.Redirect(w, r, "/phones", http.StatusSeeOther)
}

func (h *Handler) handlePhoneVoiceStylePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	raw := strings.TrimSpace(r.FormValue("voice_style"))
	if raw == "" {
		http.Error(w, "missing voice_style", http.StatusBadRequest)
		return
	}
	h.updateLineSetting(w, r, "voice-style-section", func(s *line.Settings) {
		s.VoiceStyle = raw
	})
}

func (h *Handler) handlePhoneSilentModePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	silent := strings.TrimSpace(r.FormValue("silent_mode")) == "on"
	h.updateLineSetting(w, r, "silent-mode-section", func(s *line.Settings) {
		s.SilentMode = silent
	})
}

// updateLineSetting applies a mutation to the Settings of the line identified
// by the {number} path value, persists and pushes it if anything changed, and
// then renders the named template partial (or redirects to the phone detail
// page for non-htmx callers). Handlers are expected to have already called
// ParseForm and extracted the field they need before invoking this helper.
func (h *Handler) updateLineSetting(w http.ResponseWriter, r *http.Request, partial string, mutate func(*line.Settings)) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	next := ln.Settings
	mutate(&next)
	next = next.Normalize()
	if next != ln.Settings {
		if err := h.lineStore.UpdateSettings(r.Context(), ln.ID, next); err != nil {
			slog.Error("update line settings failed", "err", err, "line_id", ln.ID)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if err := h.pushLineSettings(number, next); err != nil {
			slog.Warn("push line settings failed", "number", number, "err", err)
		}
		ln.Settings = next
	}
	if isHTMX(r) {
		renderWith(w, h.tmplPhoneDetail, partial, struct {
			Line line.Line
		}{Line: *ln})
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

// pushLineSettings sends the updated settings to the device currently
// registered as the given number, if any. A missing device is not an error;
// the next time that device reconnects it will receive the latest settings
// via the registration push in relay.OnRegistered.
func (h *Handler) pushLineSettings(number string, settings line.Settings) error {
	err := h.hub.SendTo(number, &signaling.Message{
		Type: signaling.TypeLineSettings,
		To:   number,
		LineSettings: &signaling.LineSettings{
			VoiceStyle: settings.VoiceStyle,
			SilentMode: settings.SilentMode,
		},
	})
	if errors.Is(err, signaling.ErrNotConnected) {
		return nil
	}
	return err
}

// effectiveSilent returns whether the device should treat the line as silent
// at ring time. The household-wide DND flag and the per-line silent flag are
// combined with OR: silence if either is set.
func effectiveSilent(s line.Settings, householdDND bool) bool {
	return householdDND || s.SilentMode
}

func (h *Handler) handlePhoneUpdate(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if h.requireLineOwnership(w, r, number) == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	targetPi := strings.TrimSpace(r.FormValue("target_pi_version"))
	targetFW := strings.TrimSpace(r.FormValue("target_fw_version"))

	// Clear any stale status before sending new trigger
	h.hub.ClearUpdateStatus(number)

	msg := &signaling.Message{
		Type:            signaling.TypeUpdateTrigger,
		TargetPiVersion: targetPi,
		TargetFWVersion: targetFW,
	}

	var sendErr string
	if err := h.hub.SendTo(number, msg); err != nil {
		slog.Warn("update trigger failed", "number", number, "err", err)
		sendErr = err.Error()
	} else {
		slog.Info("update trigger sent", "number", number, "target_pi", targetPi, "target_fw", targetFW)
	}

	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		if sendErr != "" {
			w.WriteHeader(http.StatusBadGateway)
			if err := json.NewEncoder(w).Encode(map[string]string{"error": sendErr}); err != nil {
				slog.Error("phone update: json encode failed", "err", err)
			}
		} else {
			if err := json.NewEncoder(w).Encode(map[string]string{"status": "triggered"}); err != nil {
				slog.Error("phone update: json encode failed", "err", err)
			}
		}
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

func (h *Handler) handlePhoneUpdateStatus(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if h.requireLineOwnership(w, r, number) == nil {
		return
	}
	status := h.hub.GetUpdateStatus(number)
	w.Header().Set("Content-Type", "application/json")
	if status == nil {
		if err := json.NewEncoder(w).Encode(map[string]string{"status": ""}); err != nil {
			slog.Error("update status: json encode failed", "err", err)
		}
		return
	}
	if err := json.NewEncoder(w).Encode(status); err != nil {
		slog.Error("update status: json encode failed", "err", err)
	}
}

func (h *Handler) handlePhoneFactoryReset(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if h.requireLineOwnership(w, r, number) == nil {
		return
	}

	h.hub.ClearUpdateStatus(number)

	msg := &signaling.Message{
		Type: signaling.TypeFactoryReset,
	}

	var sendErr string
	if err := h.hub.SendTo(number, msg); err != nil {
		slog.Warn("factory reset trigger failed", "number", number, "err", err)
		sendErr = err.Error()
	} else {
		slog.Info("factory reset triggered", "number", number)
	}

	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		if sendErr != "" {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": sendErr})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "triggered"})
		}
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

func (h *Handler) handlePhoneRestart(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if h.requireLineOwnership(w, r, number) == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	mode := strings.TrimSpace(r.FormValue("mode"))

	if mode != "service" && mode != "reboot" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "mode must be 'service' or 'reboot'"})
		return
	}

	msg := &signaling.Message{
		Type:        signaling.TypeRestart,
		RestartMode: mode,
	}

	var sendErr string
	if err := h.hub.SendTo(number, msg); err != nil {
		slog.Warn("restart command failed", "number", number, "mode", mode, "err", err)
		sendErr = err.Error()
	} else {
		slog.Info("restart command sent", "number", number, "mode", mode)
	}

	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		if sendErr != "" {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": sendErr})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "triggered"})
		}
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

func (h *Handler) handlePhoneDelete(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	if err := h.lineStore.Delete(r.Context(), ln.ID); err != nil {
		slog.Error("delete line failed", "line_id", ln.ID, "err", err)
		http.Error(w, "failed to delete line", http.StatusInternalServerError)
		return
	}
	data := h.buildLinesData(r, "")
	if isHTMX(r) {
		renderWith(w, h.tmplPhones, "phones-table", data)
		return
	}
	http.Redirect(w, r, "/phones", http.StatusSeeOther)
}

