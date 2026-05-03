package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
)

const maxLineNameRunes = 50

func validateLineName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("name is required")
	}
	if utf8.RuneCountInString(name) > maxLineNameRunes {
		return "", errors.New("name too long")
	}
	return name, nil
}

type pairSuccess struct {
	Name            string
	FirmwareVersion string
}

type linesData struct {
	chromeData
	Lines                 []lineRow
	Error                 string
	PairError             string
	PairSuccess           *pairSuccess
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

// buildLinesData assembles the line-list page payload. hh may be nil; when
// nil or lookup fails the handler shows an empty list rather than leaking
// every line on the server.
func (h *Handler) buildLinesData(r *http.Request, hh *household.Household, errMsg string) linesData {
	var user *auth.User
	if r != nil {
		user = auth.UserFromContext(r.Context())
	}

	var lines []line.Line
	if hh != nil && h.lineStore != nil {
		lines, _ = h.lineStore.ListByHousehold(r.Context(), hh.ID)
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
		chromeData:            newChromeData("phones", user, hh),
		Lines:                 rows,
		Error:                 errMsg,
		LatestPiVersion:       latestPi,
		LatestFirmwareVersion: latestFw,
	}
}

func (h *Handler) handlePhonesGet(w http.ResponseWriter, r *http.Request) {
	data := h.buildLinesData(r, h.primaryHousehold(r), "")
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

	hh := h.primaryHousehold(r)
	var householdID string
	if hh != nil {
		householdID = hh.ID
	}

	if err := line.ValidateNumber(number); err != nil {
		data := h.buildLinesData(r, hh, err.Error())
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	_, err := h.lineStore.Add(r.Context(), number, name, householdID)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	data := h.buildLinesData(r, hh, msg)

	if isHTMX(r) {
		renderWith(w, h.tmplPhones, partialFor(r, "phones-table", "am-phones-table"), data)
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

	hh := h.primaryHousehold(r)

	if err := line.ValidateNumber(number); err != nil {
		data := h.buildLinesData(r, hh, "")
		data.PairError = "invalid phone number: " + err.Error()
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	if h.pairingStore == nil {
		data := h.buildLinesData(r, hh, "")
		data.PairError = "pairing is not enabled"
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	var householdID string
	if hh != nil {
		householdID = hh.ID
	}
	if householdID == "" {
		data := h.buildLinesData(r, hh, "")
		data.PairError = "no household found: please complete onboarding first"
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	token, hwID, err := h.pairingStore.ClaimDevice(r.Context(), code, number, name, householdID)
	if err != nil {
		data := h.buildLinesData(r, hh, "")
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
	chromeData
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
}

func (h *Handler) handlePhoneDetail(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln, hh := h.requireLineOwnershipWithHousehold(w, r, number)
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

	loc := hh.Location()

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
		chromeData:            newChromeData("phones", user, hh),
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
	})
}

func (h *Handler) handlePhoneOnline(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if h.requireLineOwnership(w, r, number) == nil {
		return
	}
	online := h.hub.Get(number) != nil
	if isHTMX(r) {
		renderWith(w, h.tmplPhoneDetail, partialFor(r, "phone-status", "am-phone-status"), struct {
			Online bool
		}{online})
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
	renderWith(w, h.tmplPhones, partialFor(r, "phone-edit-row", "am-phone-edit-row"), lineRow{Line: *ln, Online: online})
}

func (h *Handler) handlePhoneEditPost(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name, err := validateLineName(r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ln, hh := h.requireLineOwnershipWithHousehold(w, r, number)
	if ln == nil {
		return
	}

	if name != ln.Name {
		if err := h.lineStore.Update(r.Context(), ln.ID, number, name); err != nil {
			slog.Error("line update failed", "err", err, "line_id", ln.ID)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	data := h.buildLinesData(r, hh, "")
	if isHTMX(r) {
		renderWith(w, h.tmplPhones, partialFor(r, "phones-table", "am-phones-table"), data)
		return
	}
	http.Redirect(w, r, "/phones", http.StatusSeeOther)
}

// nameSectionData carries the prefilled input value and any validation error
// when re-rendering the edit partial after a failed POST, so the user's draft
// and the reason for rejection survive the round-trip.
type nameSectionData struct {
	Line  line.Line
	Error string
	Value string
}

func (h *Handler) handlePhoneNameGet(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	renderWith(w, h.tmplPhoneDetail, partialFor(r, "name-section", "am-name-section"), nameSectionData{Line: *ln})
}

func (h *Handler) handlePhoneNameEditGet(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	renderWith(w, h.tmplPhoneDetail, partialFor(r, "name-section-edit", "am-name-section-edit"), nameSectionData{Line: *ln, Value: ln.Name})
}

func (h *Handler) handlePhoneNamePost(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	raw := r.FormValue("name")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	name, verr := validateLineName(raw)
	if verr != nil {
		renderWithStatus(w, h.tmplPhoneDetail, partialFor(r, "name-section-edit", "am-name-section-edit"), nameSectionData{Line: *ln, Value: raw, Error: verr.Error()}, http.StatusBadRequest)
		return
	}
	if name != ln.Name {
		if err := h.lineStore.Update(r.Context(), ln.ID, number, name); err != nil {
			slog.Error("line update failed", "err", err, "line_id", ln.ID)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		ln.Name = name
	}
	if isHTMX(r) {
		renderWith(w, h.tmplPhoneDetail, partialFor(r, "name-section", "am-name-section"), nameSectionData{Line: *ln})
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
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
	h.updateLineSetting(w, r, "voice-style-section", "am-voice-style-section", func(s *line.Settings) {
		s.VoiceStyle = raw
	})
}

func (h *Handler) handlePhoneSilentModePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	silent := strings.TrimSpace(r.FormValue("silent_mode")) == "on"
	h.updateLineSetting(w, r, "silent-mode-section", "am-silent-mode-section", func(s *line.Settings) {
		s.SilentMode = silent
	})
}

func (h *Handler) handlePhoneAutoUpdatePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	autoUpdate := strings.TrimSpace(r.FormValue("auto_update")) == "on"
	h.updateLineSetting(w, r, "auto-update-section", "am-auto-update-section", func(s *line.Settings) {
		s.AutoUpdate = autoUpdate
	})
}

// updateLineSetting applies a mutation to the Settings of the line identified
// by the {number} path value, persists and pushes it if anything changed, and
// then renders the theme-appropriate partial (or redirects to the phone detail
// page for non-htmx callers). Handlers are expected to have already called
// ParseForm and extracted the field they need before invoking this helper.
func (h *Handler) updateLineSetting(w http.ResponseWriter, r *http.Request, intercom, am string, mutate func(*line.Settings)) {
	number := r.PathValue("number")
	ln, hh := h.requireLineOwnershipWithHousehold(w, r, number)
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
		dnd := false
		if hh != nil {
			dnd = hh.DoNotDisturb
		}
		if err := h.pushLineSettings(number, next, dnd); err != nil {
			slog.Warn("push line settings failed", "number", number, "err", err)
		}
		ln.Settings = next
	}
	if isHTMX(r) {
		renderWith(w, h.tmplPhoneDetail, partialFor(r, intercom, am), struct {
			Line line.Line
		}{Line: *ln})
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

// pushLineSettings sends the updated effective settings to the device
// currently registered as the given number, if any. The household-DND flag
// is OR'd into SilentMode before sending so the device sees one
// authoritative bool. A missing device is not an error; the next time that
// device reconnects it will receive the latest effective settings via the
// registration push in relay.OnRegistered.
func (h *Handler) pushLineSettings(number string, settings line.Settings, householdDND bool) error {
	err := h.hub.SendTo(number, &signaling.Message{
		Type: signaling.TypeLineSettings,
		To:   number,
		LineSettings: &signaling.LineSettings{
			VoiceStyle: settings.VoiceStyle,
			SilentMode: line.EffectiveSilent(settings, householdDND),
			AutoUpdate: settings.AutoUpdate,
		},
	})
	if errors.Is(err, signaling.ErrNotConnected) {
		return nil
	}
	return err
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

	h.respondPhoneCommandResult(w, r, number, sendErr)
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

	h.respondPhoneCommandResult(w, r, number, sendErr)
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

	h.respondPhoneCommandResult(w, r, number, sendErr)
}

// respondPhoneCommandResult writes the response for a phone-command handler
// (update, factory reset, restart). JSON-accept callers get a structured
// status/error body; everyone else is redirected to the phone detail page.
// sendErr is the empty string on success or the hub send error string.
func (h *Handler) respondPhoneCommandResult(w http.ResponseWriter, r *http.Request, number, sendErr string) {
	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]string{"status": "triggered"}
		if sendErr != "" {
			w.WriteHeader(http.StatusBadGateway)
			payload = map[string]string{"error": sendErr}
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			slog.Error("phone command response: json encode failed", "number", number, "err", err)
		}
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

func (h *Handler) handlePhoneDelete(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln, hh := h.requireLineOwnershipWithHousehold(w, r, number)
	if ln == nil {
		return
	}
	if err := h.lineStore.Delete(r.Context(), ln.ID); err != nil {
		slog.Error("delete line failed", "line_id", ln.ID, "err", err)
		http.Error(w, "failed to delete line", http.StatusInternalServerError)
		return
	}
	data := h.buildLinesData(r, hh, "")
	if isHTMX(r) {
		renderWith(w, h.tmplPhones, partialFor(r, "phones-table", "am-phones-table"), data)
		return
	}
	http.Redirect(w, r, "/phones", http.StatusSeeOther)
}

