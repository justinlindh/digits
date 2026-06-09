package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
)

const maxLineNameRunes = 50

func oldestVersions(infos []signaling.DeviceInfoSnapshot) (fw, pi string) {
	for _, info := range infos {
		if info.FirmwareVersion != "" && (fw == "" || updates.CompareSemver(info.FirmwareVersion, fw) < 0) {
			fw = info.FirmwareVersion
		}
		if info.PiVersion != "" && (pi == "" || updates.CompareSemver(info.PiVersion, pi) < 0) {
			pi = info.PiVersion
		}
	}
	return
}

// updateNotes returns the Pi and firmware release notes that span from the
// oldest connected device's reported version up to latestPi / latestFw. Empty
// when idx is nil or when nothing on this line is behind its component's
// latest release.
func updateNotes(idx *updates.ReleaseIndex, infos []signaling.DeviceInfoSnapshot, latestPi, latestFw string) (pi, fw []updates.Release) {
	if idx == nil {
		return nil, nil
	}
	oldestFw, oldestPi := oldestVersions(infos)
	if latestPi != "" && oldestPi != "" && updates.CompareSemver(oldestPi, latestPi) < 0 {
		pi = idx.RangeReleases(updates.ComponentPi, oldestPi, latestPi)
	}
	if latestFw != "" && oldestFw != "" && updates.CompareSemver(oldestFw, latestFw) < 0 {
		fw = idx.RangeReleases(updates.ComponentFirmware, oldestFw, latestFw)
	}
	return pi, fw
}

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
	Lines     []lineRow
	AllSilent bool
	PairError string
}

type lineRow struct {
	Line                line.Line
	Online              bool
	OnCall              bool
	OnCallPeerName      string
	OnCallElapsed       string // "mm:ss" for the Dashboard room-card callout
	OnCallID            int64  // 0 when not on a call; otherwise the active call id
	DeviceInfo          *signaling.DeviceInfoSnapshot
	Devices             []device.Device
	OnlineDeviceCount   int
	FirmwareUpdateNotes []updates.Release
	PiUpdateNotes       []updates.Release
	// VoicemailUnheard is the line-level unheard-voicemail count summed
	// across handsets last reported by digitsd. Surfaced as a badge on the
	// line's row in the phones list; the badge only renders when voicemail
	// is enabled AND this count is greater than zero.
	VoicemailUnheard int
}

// buildLinesData assembles the household's line roster with per-line status
// (online state, devices, update notes, voicemail counts). It feeds the
// Overview line cards, the pairing page's extension dropdown, the dashboard
// SSE status, and the status API. hh may be nil; when nil or lookup fails
// the caller gets an empty list rather than every line on the server.
func (h *Handler) buildLinesData(r *http.Request, hh *household.Household) linesData {
	var lines []line.Line
	if hh != nil && h.lineStore != nil {
		var err error
		lines, err = h.lineStore.ListByHousehold(r.Context(), hh.ID)
		if err != nil {
			slog.ErrorContext(r.Context(), "list lines by household failed", "household_id", hh.ID, "err", err)
		}
	}
	// On error or nil household, show empty list rather than leaking all lines.
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
	if h.releases != nil {
		idx = h.releases.ReleaseIndex()
	}
	if idx != nil {
		latestPi = idx.Pi.Latest
		latestFw = idx.Firmware.Latest
	}

	rows := make([]lineRow, len(lines))
	for i, l := range lines {
		infos := h.hub.AllDeviceInfo(l.Number)
		var info *signaling.DeviceInfoSnapshot
		if len(infos) > 0 {
			info = &infos[0]
		}
		row := lineRow{Line: l, Online: onlineSet[l.Number], DeviceInfo: info}
		row.OnlineDeviceCount = h.hub.ConnectionCount(l.Number)
		row.VoicemailUnheard = h.hub.LineVoicemailUnheard(l.Number)

		if h.deviceStore != nil {
			devs, err := h.deviceStore.ListByLine(r.Context(), l.ID)
			if err != nil {
				slog.ErrorContext(r.Context(), "list devices for line", "line_id", l.ID, "err", err)
			} else {
				row.Devices = devs
			}
		}

		row.PiUpdateNotes, row.FirmwareUpdateNotes = updateNotes(idx, infos, latestPi, latestFw)
		rows[i] = row
	}
	allSilent := len(rows) > 0
	for _, row := range rows {
		if !row.Line.Settings.SilentMode {
			allSilent = false
			break
		}
	}
	cd := h.newChromeDataWithHouseholds(r, "phones")
	cd.allSilent = allSilent
	return linesData{
		chromeData: cd,
		Lines:      rows,
		AllSilent:  allSilent,
	}
}

// renderPairError renders the full phones page with a pairing-specific error.
func (h *Handler) renderPairError(w http.ResponseWriter, r *http.Request, hh *household.Household, msg string) {
	data := h.buildLinesData(r, hh)
	data.PairError = msg
	renderWith(r.Context(), w, h.tmplPhones, layoutFor(r), data)
}

func (h *Handler) handlePhonesGet(w http.ResponseWriter, r *http.Request) {
	data := h.buildLinesData(r, h.activeHousehold(r))
	renderWith(r.Context(), w, h.tmplPhones, layoutFor(r), data)
}

func (h *Handler) handlePhonesPairPost(w http.ResponseWriter, r *http.Request) {
	_, hh, ok := h.requireHouseholdAdmin(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	code := strings.TrimSpace(r.FormValue("code"))
	deviceName := strings.TrimSpace(r.FormValue("name"))
	pairMode := strings.TrimSpace(r.FormValue("pair_mode"))
	existingLineID := strings.TrimSpace(r.FormValue("existing_line_id"))

	if h.pairingStore == nil {
		h.renderPairError(w, r, hh, "pairing is not enabled")
		return
	}

	householdID := hh.ID

	var (
		token  string
		hwID   string
		number string
		err    error
	)

	if pairMode == "existing" && existingLineID != "" {
		// Add device to an existing line (POTS extension)
		lineID, parseErr := strconv.ParseInt(existingLineID, 10, 64)
		if parseErr != nil {
			h.renderPairError(w, r, hh, "invalid line selection")
			return
		}
		token, hwID, err = h.pairingStore.ClaimDeviceToLine(r.Context(), code, lineID, deviceName, householdID)
		if err == nil {
			ln, lnErr := h.lineStore.GetByID(r.Context(), lineID)
			if lnErr == nil {
				number = ln.Number
			}
		}
	} else {
		// Create a new line and pair the device
		number = line.StripNumber(strings.TrimSpace(r.FormValue("number")))
		if verr := line.ValidateNumber(number); verr != nil {
			h.renderPairError(w, r, hh, "invalid phone number: "+verr.Error())
			return
		}
		lineName, verr := validateLineName(deviceName)
		if verr != nil {
			h.renderPairError(w, r, hh, "handset name: "+verr.Error())
			return
		}
		token, hwID, err = h.pairingStore.ClaimDevice(r.Context(), code, number, lineName, deviceName, householdID)
	}

	if err != nil {
		h.renderPairError(w, r, hh, err.Error())
		return
	}

	if hwID != "" && number != "" {
		if err := h.hub.SendToHardware(hwID, &signaling.Message{
			Type:        signaling.TypePaired,
			DeviceToken: token,
			Number:      number,
		}); err != nil {
			slog.WarnContext(r.Context(), "could not notify device of pairing", "hardware_id", hwID, "err", err)
		}
	}

	v := url.Values{}
	v.Set("paired", deviceName)
	http.Redirect(w, r, "/?"+v.Encode(), http.StatusSeeOther)
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
	OtherLines            []line.Line
	NumberError           string
}

func (h *Handler) handlePhoneDetail(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln, hh := h.requireLineOwnershipWithHousehold(w, r, number)
	if ln == nil {
		return
	}
	online := h.hub.IsOnline(number)

	var devices []device.Device
	if h.deviceStore != nil {
		var err error
		devices, err = h.deviceStore.ListByLine(r.Context(), ln.ID)
		if err != nil {
			slog.ErrorContext(r.Context(), "failed to list devices by line", "err", err, "line_id", ln.ID)
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
	if h.releases != nil {
		idx = h.releases.ReleaseIndex()
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

	allInfos := h.hub.AllDeviceInfo(number)
	var devInfo *signaling.DeviceInfoSnapshot
	if len(allInfos) > 0 {
		devInfo = &allInfos[0]
	}

	piUpdateNotes, firmwareUpdateNotes := updateNotes(idx, allInfos, latestPi, latestFw)

	var otherLines []line.Line
	allLines, err := h.lineStore.ListByHousehold(r.Context(), hh.ID)
	if err == nil {
		for _, ol := range allLines {
			if ol.ID != ln.ID {
				otherLines = append(otherLines, ol)
			}
		}
	}

	renderWith(r.Context(), w, h.tmplPhoneDetail, layoutFor(r), lineDetailData{
		chromeData:            h.newChromeDataWithHouseholds(r, "phones"),
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
		OtherLines:            otherLines,
		NumberError:           r.URL.Query().Get("number_error"),
	})
}

func (h *Handler) handlePhoneOnline(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if h.requireLineOwnership(w, r, number) == nil {
		return
	}
	online := h.hub.IsOnline(number)
	if isHTMX(r) {
		renderWith(r.Context(), w, h.tmplPhoneDetail, partialFor(r, "phone-status", "am-phone-status"), struct {
			Online bool
		}{online})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]bool{"online": online}); err != nil {
		slog.ErrorContext(r.Context(), "encode online status failed", "err", err)
	}
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
	renderWith(r.Context(), w, h.tmplPhoneDetail, partialFor(r, "name-section", "am-name-section"), nameSectionData{Line: *ln})
}

func (h *Handler) handlePhoneNameEditGet(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	renderWith(r.Context(), w, h.tmplPhoneDetail, partialFor(r, "name-section-edit", "am-name-section-edit"), nameSectionData{Line: *ln, Value: ln.Name})
}

func (h *Handler) handlePhoneNamePost(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !parseForm(w, r) {
		return
	}
	raw := r.FormValue("name")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	name, verr := validateLineName(raw)
	if verr != nil {
		renderWithStatus(r.Context(), w, h.tmplPhoneDetail, partialFor(r, "name-section-edit", "am-name-section-edit"), nameSectionData{Line: *ln, Value: raw, Error: verr.Error()}, http.StatusBadRequest)
		return
	}
	if name != ln.Name {
		if err := h.lineStore.Update(r.Context(), ln.ID, number, name); err != nil {
			slog.ErrorContext(r.Context(), "line update failed", "err", err, "line_id", ln.ID)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		ln.Name = name
	}
	if isHTMX(r) {
		renderWith(r.Context(), w, h.tmplPhoneDetail, partialFor(r, "name-section", "am-name-section"), nameSectionData{Line: *ln})
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

func (h *Handler) handlePhoneNumberPost(w http.ResponseWriter, r *http.Request) {
	oldNumber := r.PathValue("number")
	if !parseForm(w, r) {
		return
	}
	newNumber := line.StripNumber(r.FormValue("number"))

	ln, _ := h.requireLineOwnershipAdmin(w, r, oldNumber)
	if ln == nil {
		return
	}

	if h.tracker != nil && h.tracker.Busy(r.Context(), oldNumber) {
		http.Redirect(w, r, "/phones/"+oldNumber+"?number_error="+url.QueryEscape("cannot change number while on an active call"), http.StatusSeeOther)
		return
	}

	if err := line.ValidateNumber(newNumber); err != nil {
		http.Redirect(w, r, "/phones/"+oldNumber+"?number_error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}

	if newNumber == oldNumber {
		http.Redirect(w, r, "/phones/"+oldNumber, http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	taken, err := h.lineStore.NumberExistsExcluding(ctx, newNumber, ln.ID)
	if err != nil {
		slog.ErrorContext(ctx, "number uniqueness check failed", "err", err, "line_id", ln.ID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if taken {
		http.Redirect(w, r, "/phones/"+oldNumber+"?number_error="+url.QueryEscape("that number is already in use"), http.StatusSeeOther)
		return
	}

	if err := h.lineStore.Update(ctx, ln.ID, newNumber, ln.Name); err != nil {
		slog.ErrorContext(ctx, "line number update failed", "err", err, "line_id", ln.ID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if h.tracker != nil {
		if err := h.tracker.RenameNumber(ctx, oldNumber, newNumber); err != nil {
			slog.ErrorContext(ctx, "call history rename failed", "old", oldNumber, "new", newNumber, "err", err)
		}
	}

	h.hub.RekeyNumber(oldNumber, newNumber)

	if err := h.pushLineSettings(newNumber, ln.Settings); err != nil {
		slog.WarnContext(ctx, "push line settings after number change failed", "number", newNumber, "err", err)
	}

	http.Redirect(w, r, "/phones/"+newNumber, http.StatusSeeOther)
}

func (h *Handler) handlePhoneVoiceStylePost(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
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
	if !parseForm(w, r) {
		return
	}
	silent := strings.TrimSpace(r.FormValue("silent_mode")) == "on"
	h.updateLineSetting(w, r, "silent-mode-section", "am-silent-mode-section", func(s *line.Settings) {
		s.SilentMode = silent
	})
}

func (h *Handler) handlePhoneAutoUpdatePost(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	autoUpdate := strings.TrimSpace(r.FormValue("auto_update")) == "on"
	h.updateLineSetting(w, r, "auto-update-section", "am-auto-update-section", func(s *line.Settings) {
		s.AutoUpdate = autoUpdate
	})
}

// handlePhoneQuietHoursPost accepts the full quiet-hours window for a line:
// an enable checkbox, start/end "HH:MM" times, and day_0..day_6 checkboxes
// (index = time.Weekday, Sunday = 0). The times are validated through
// Settings.Normalize, which disables a malformed or zero-length window rather
// than rejecting the request, so a fat-fingered time can never wedge the
// form. On success the new settings persist and push, then the quiet-hours
// section partial is swapped (htmx) or the detail page reloads.
func (h *Handler) handlePhoneQuietHoursPost(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}

	var days [7]bool
	for i := range days {
		if strings.TrimSpace(r.FormValue("day_"+strconv.Itoa(i))) == "on" {
			days[i] = true
		}
	}

	next := ln.Settings
	next.QuietHours = line.QuietHours{
		Enabled: strings.TrimSpace(r.FormValue("enabled")) == "on",
		Start:   strings.TrimSpace(r.FormValue("start")),
		End:     strings.TrimSpace(r.FormValue("end")),
		Days:    days,
	}
	next = next.Normalize()
	if !h.applyLineSettings(w, r, ln, next) {
		return
	}

	if isHTMX(r) {
		renderWith(r.Context(), w, h.tmplPhoneDetail,
			partialFor(r, "quiet-hours-section", "am-quiet-hours-section"),
			struct {
				Line line.Line
			}{Line: *ln})
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

// handlePhoneVoicemailPost accepts a form submission with the full voicemail
// configuration for the line. Every field is validated server-side before
// any DB write: out-of-range ints and malformed retrieval codes
// return 400 with a friendly message so the form can surface it. On success
// the new settings are persisted and pushed to the device (if connected),
// then either the voicemail-section partial is swapped (htmx) or the user
// is redirected back to the phone detail page (regular form post).
func (h *Handler) handlePhoneVoicemailPost(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}

	enabled := strings.TrimSpace(r.FormValue("enabled")) == "on"
	ring, ok := parseClampedInt(w, r, "ring_timeout_seconds",
		line.VoicemailRingTimeoutMin, line.VoicemailRingTimeoutMax)
	if !ok {
		return
	}

	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}

	next := ln.Settings
	next.Voicemail = line.Voicemail{
		Enabled:            enabled,
		RingTimeoutSeconds: ring,
	}
	next = next.Normalize()
	if !h.applyLineSettings(w, r, ln, next) {
		return
	}

	if isHTMX(r) {
		renderWith(r.Context(), w, h.tmplPhoneDetail,
			partialFor(r, "voicemail-section", "am-voicemail-section"),
			struct {
				Line line.Line
			}{Line: *ln})
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

// handlePhoneVoicemailTogglePost flips Voicemail.Enabled for a line and
// swaps the detail-page voicemail section. The other voicemail fields
// are preserved through Normalize, which backfills defaults when the row
// was created before voicemail existed. Path is intentionally separate
// from the full-form POST so a checkbox-only round-trip does not have to
// resubmit the timing/code fields.
func (h *Handler) handlePhoneVoicemailTogglePost(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}

	next := ln.Settings
	next.Voicemail.Enabled = !next.Voicemail.Enabled
	next = next.Normalize()
	if !h.applyLineSettings(w, r, ln, next) {
		return
	}

	if isHTMX(r) {
		renderWith(r.Context(), w, h.tmplPhoneDetail,
			partialFor(r, "voicemail-section", "am-voicemail-section"),
			struct {
				Line line.Line
			}{Line: *ln})
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

// parseClampedInt reads form field `name`, parses it as an integer, and
// requires it to fall in [min, max]. On any failure it writes a 400 with a
// friendly message naming the field and the allowed range, then returns
// (0, false). Helper for handlePhoneVoicemailPost so the three numeric
// validations don't repeat the same boilerplate.
func parseClampedInt(w http.ResponseWriter, r *http.Request, name string, min, max int) (int, bool) {
	raw := strings.TrimSpace(r.FormValue(name))
	v, err := strconv.Atoi(raw)
	if err != nil || v < min || v > max {
		http.Error(w,
			name+" must be an integer between "+strconv.Itoa(min)+" and "+strconv.Itoa(max),
			http.StatusBadRequest)
		return 0, false
	}
	return v, true
}

// updateLineSetting applies a mutation to the Settings of the line identified
// by the {number} path value, persists and pushes it if anything changed, and
// then renders the theme-appropriate partial (or redirects to the phone detail
// page for non-htmx callers). Handlers are expected to have already called
// ParseForm and extracted the field they need before invoking this helper.
func (h *Handler) updateLineSetting(w http.ResponseWriter, r *http.Request, intercom, am string, mutate func(*line.Settings)) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	next := ln.Settings
	mutate(&next)
	next = next.Normalize()
	if !h.applyLineSettings(w, r, ln, next) {
		return
	}
	if isHTMX(r) {
		renderWith(r.Context(), w, h.tmplPhoneDetail, partialFor(r, intercom, am), struct {
			Line line.Line
		}{Line: *ln})
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

// applyLineSettings persists `next` and pushes it to the connected device if
// it differs from ln.Settings. On persistence failure it writes a 500 and
// returns false so the caller can abort; push failures are logged but do not
// fail the request (the next OnRegistered reconciles). On success (including
// the no-op case where next == ln.Settings) it returns true and mutates
// ln.Settings in place so callers rendering ln see the latest values.
//
// The push uses effective settings (quiet-hours-aware SilentMode) so a device
// is not un-silenced by an unrelated setting change during an active window.
func (h *Handler) applyLineSettings(w http.ResponseWriter, r *http.Request, ln *line.Line, next line.Settings) bool {
	if next == ln.Settings {
		return true
	}
	if err := h.lineStore.UpdateSettings(r.Context(), ln.ID, next); err != nil {
		slog.ErrorContext(r.Context(), "update line settings failed", "err", err, "line_id", ln.ID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return false
	}
	effective, err := h.lineStore.EffectiveSettingsByNumber(r.Context(), ln.Number)
	if err != nil {
		slog.WarnContext(r.Context(), "fetch effective settings failed, pushing raw settings", "number", ln.Number, "err", err)
		effective = next
	}
	if err := h.pushLineSettings(ln.Number, effective); err != nil {
		slog.WarnContext(r.Context(), "push line settings failed", "number", ln.Number, "err", err)
	}
	ln.Settings = next
	return true
}

// pushLineSettings sends the updated settings to the device currently
// registered as the given number, if any. A missing device is not an error;
// the next time that device reconnects it will receive the latest effective
// settings via the registration push in relay.OnRegistered.
func (h *Handler) pushLineSettings(number string, settings line.Settings) error {
	err := h.hub.SendTo(number, &signaling.Message{
		Type: signaling.TypeLineSettings,
		To:   number,
		LineSettings: &signaling.LineSettings{
			VoiceStyle: settings.VoiceStyle,
			SilentMode: settings.SilentMode,
			AutoUpdate: settings.AutoUpdate,
			Voicemail:  signaling.VoicemailFromLine(settings.Voicemail),
		},
	})
	if errors.Is(err, signaling.ErrNotConnected) {
		return nil
	}
	return err
}

func (h *Handler) handlePhoneUpdate(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if ln, _ := h.requireLineOwnershipAdmin(w, r, number); ln == nil {
		return
	}
	if !parseForm(w, r) {
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
	h.sendPhoneCommandAndRespond(w, r, number, msg, "update trigger", "target_pi", targetPi, "target_fw", targetFW)
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
			slog.ErrorContext(r.Context(), "update status: json encode failed", "err", err)
		}
		return
	}
	if err := json.NewEncoder(w).Encode(status); err != nil {
		slog.ErrorContext(r.Context(), "update status: json encode failed", "err", err)
	}
}

func (h *Handler) handlePhoneRingTest(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if h.requireLineOwnership(w, r, number) == nil {
		return
	}

	msg := &signaling.Message{
		Type: signaling.TypeRingTest,
	}
	h.sendPhoneCommandAndRespond(w, r, number, msg, "ring test")
}

func (h *Handler) handlePhoneFactoryReset(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if ln, _ := h.requireLineOwnershipAdmin(w, r, number); ln == nil {
		return
	}

	h.hub.ClearUpdateStatus(number)

	msg := &signaling.Message{
		Type: signaling.TypeFactoryReset,
	}
	h.sendPhoneCommandAndRespond(w, r, number, msg, "factory reset")
}

func (h *Handler) handlePhoneRestart(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if ln, _ := h.requireLineOwnershipAdmin(w, r, number); ln == nil {
		return
	}
	if !parseForm(w, r) {
		return
	}
	mode := strings.TrimSpace(r.FormValue("mode"))

	if mode != "service" && mode != "reboot" {
		jsonError(r.Context(), w, "mode must be 'service' or 'reboot'", http.StatusBadRequest)
		return
	}

	msg := &signaling.Message{
		Type:        signaling.TypeRestart,
		RestartMode: mode,
	}
	h.sendPhoneCommandAndRespond(w, r, number, msg, "restart command", "mode", mode)
}

// sendPhoneCommandAndRespond pushes msg to the device, logs the outcome
// (warn on hub send failure, info on success), and writes the standard
// phone-command response. opName names the operation for the log
// messages; extraInfo is forwarded to both the warn and info logs as
// command-specific context (restart mode, update targets, etc.).
func (h *Handler) sendPhoneCommandAndRespond(w http.ResponseWriter, r *http.Request, number string, msg *signaling.Message, opName string, extraInfo ...any) {
	var sendErr string
	if err := h.hub.SendTo(number, msg); err != nil {
		slog.WarnContext(r.Context(), opName+" failed", append([]any{"number", number, "err", err}, extraInfo...)...)
		sendErr = err.Error()
	} else {
		slog.InfoContext(r.Context(), opName+" sent", append([]any{"number", number}, extraInfo...)...)
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
			slog.ErrorContext(r.Context(), "phone command response: json encode failed", "number", number, "err", err)
		}
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

func (h *Handler) handlePhoneDelete(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln, _ := h.requireLineOwnershipAdmin(w, r, number)
	if ln == nil {
		return
	}
	if err := h.lineStore.Delete(r.Context(), ln.ID); err != nil {
		slog.ErrorContext(r.Context(), "delete line failed", "line_id", ln.ID, "err", err)
		http.Error(w, "failed to delete line", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) handlePhoneConvert(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !parseForm(w, r) {
		return
	}

	targetLineIDStr := strings.TrimSpace(r.FormValue("target_line_id"))
	if targetLineIDStr == "" {
		http.Error(w, "target line required", http.StatusBadRequest)
		return
	}
	targetLineID, err := strconv.ParseInt(targetLineIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid target line", http.StatusBadRequest)
		return
	}

	srcLn, _ := h.requireLineOwnershipAdmin(w, r, number)
	if srcLn == nil {
		return
	}
	if srcLn.ID == targetLineID {
		http.Error(w, "cannot move to the same line", http.StatusBadRequest)
		return
	}

	// Verify target line belongs to same household.
	tgtLn, err := h.lineStore.GetByID(r.Context(), targetLineID)
	if err != nil {
		http.Error(w, "target line not found", http.StatusNotFound)
		return
	}
	if tgtLn.HouseholdID != srcLn.HouseholdID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	devices, listErr := h.deviceStore.ListByLine(r.Context(), srcLn.ID)
	if listErr != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	deviceIDStr := strings.TrimSpace(r.FormValue("device_id"))
	var deviceID int64
	if deviceIDStr != "" {
		deviceID, err = strconv.ParseInt(deviceIDStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid device", http.StatusBadRequest)
			return
		}
		owned := false
		for _, d := range devices {
			if d.ID == deviceID {
				owned = true
				break
			}
		}
		if !owned {
			http.Error(w, "device does not belong to this line", http.StatusBadRequest)
			return
		}
	} else {
		if len(devices) != 1 {
			http.Error(w, "device_id required for multi-device lines", http.StatusBadRequest)
			return
		}
		deviceID = devices[0].ID
	}

	// Move the device.
	if err := h.deviceStore.Reassign(r.Context(), deviceID, targetLineID); err != nil {
		slog.ErrorContext(r.Context(), "move device failed", "device_id", deviceID, "target", targetLineID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	remaining, err := h.deviceStore.ListByLine(r.Context(), srcLn.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "list remaining devices failed", "line_id", srcLn.ID, "err", err)
		http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
		return
	}
	if len(remaining) == 0 {
		if err := h.lineStore.Delete(r.Context(), srcLn.ID); err != nil {
			slog.ErrorContext(r.Context(), "delete empty line failed", "line_id", srcLn.ID, "err", err)
		}
		http.Redirect(w, r, "/phones", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}
