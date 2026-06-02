package line

import (
	"encoding/json"
	"testing"
	"time"
)

// at builds a time on a known weekday at the given clock, in the given
// location. 2024-01-01 was a Monday, so adding dayOffset days lets a test
// name a weekday explicitly.
func at(t *testing.T, loc *time.Location, weekday time.Weekday, hour, min int) time.Time {
	t.Helper()
	// 2024-01-07 is a Sunday. Offset to the requested weekday.
	base := time.Date(2024, 1, 7, 0, 0, 0, 0, loc) // Sunday
	d := base.AddDate(0, 0, int(weekday))
	return time.Date(d.Year(), d.Month(), d.Day(), hour, min, 0, 0, loc)
}

func daysAll() [7]bool { return AllDays() }

func TestQuietHoursActiveAtInWindowSameDay(t *testing.T) {
	q := QuietHours{Enabled: true, Start: "09:00", End: "17:00", Days: daysAll()}
	if !q.ActiveAt(at(t, time.UTC, time.Wednesday, 12, 0)) {
		t.Errorf("noon should be inside 09:00-17:00")
	}
}

func TestQuietHoursOutOfWindowSameDay(t *testing.T) {
	q := QuietHours{Enabled: true, Start: "09:00", End: "17:00", Days: daysAll()}
	if q.ActiveAt(at(t, time.UTC, time.Wednesday, 8, 59)) {
		t.Errorf("08:59 should be before the window")
	}
	if q.ActiveAt(at(t, time.UTC, time.Wednesday, 17, 0)) {
		t.Errorf("17:00 is the half-open end and must be released")
	}
	if !q.ActiveAt(at(t, time.UTC, time.Wednesday, 16, 59)) {
		t.Errorf("16:59 should still be inside")
	}
	if !q.ActiveAt(at(t, time.UTC, time.Wednesday, 9, 0)) {
		t.Errorf("09:00 is the inclusive start")
	}
}

func TestQuietHoursMidnightWrap(t *testing.T) {
	// Active Monday 22:00 through Tuesday 07:00.
	q := QuietHours{Enabled: true, Start: "22:00", End: "07:00", Days: daysAll()}
	if !q.ActiveAt(at(t, time.UTC, time.Monday, 23, 30)) {
		t.Errorf("Monday 23:30 should be inside the wrap window")
	}
	if !q.ActiveAt(at(t, time.UTC, time.Tuesday, 6, 59)) {
		t.Errorf("Tuesday 06:59 should still be inside the carryover")
	}
	if q.ActiveAt(at(t, time.UTC, time.Tuesday, 7, 0)) {
		t.Errorf("Tuesday 07:00 should be released")
	}
	if q.ActiveAt(at(t, time.UTC, time.Monday, 21, 59)) {
		t.Errorf("Monday 21:59 should be before the window opens")
	}
}

func TestQuietHoursDayFilterSameDay(t *testing.T) {
	// Only weekdays (Mon-Fri).
	q := QuietHours{Enabled: true, Start: "09:00", End: "17:00"}
	q.Days[time.Monday] = true
	q.Days[time.Tuesday] = true
	q.Days[time.Wednesday] = true
	q.Days[time.Thursday] = true
	q.Days[time.Friday] = true
	if !q.ActiveAt(at(t, time.UTC, time.Wednesday, 12, 0)) {
		t.Errorf("Wednesday noon should be active")
	}
	if q.ActiveAt(at(t, time.UTC, time.Saturday, 12, 0)) {
		t.Errorf("Saturday is not selected, must be inactive")
	}
}

func TestQuietHoursWrapDayFilterUsesOpeningDay(t *testing.T) {
	// Monday-only 22:00-07:00 should carry into Tuesday morning, but a
	// Tuesday 22:00 opening must not fire because Tuesday is not selected.
	q := QuietHours{Enabled: true, Start: "22:00", End: "07:00"}
	q.Days[time.Monday] = true
	if !q.ActiveAt(at(t, time.UTC, time.Tuesday, 6, 0)) {
		t.Errorf("Tuesday 06:00 carries over from Monday opening")
	}
	if q.ActiveAt(at(t, time.UTC, time.Tuesday, 23, 0)) {
		t.Errorf("Tuesday 23:00 should not open: Tuesday not selected")
	}
	if !q.ActiveAt(at(t, time.UTC, time.Monday, 23, 0)) {
		t.Errorf("Monday 23:00 should open")
	}
}

func TestQuietHoursTimezoneAware(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	q := QuietHours{Enabled: true, Start: "22:00", End: "07:00", Days: daysAll()}
	// 11:00 UTC Wednesday is daytime (outside 22:00-07:00 in UTC terms) but
	// 06:00 EST Wednesday, which is still inside the overnight window in NY.
	utcMidday := time.Date(2024, 1, 10, 11, 0, 0, 0, time.UTC) // Wednesday UTC
	if q.ActiveAt(utcMidday) {
		t.Errorf("11:00 UTC is outside the window in UTC terms")
	}
	if !q.ActiveAt(utcMidday.In(ny)) {
		t.Errorf("converted to New York (06:00 Wed) the window should be active")
	}
}

func TestQuietHoursDisabledNeverActive(t *testing.T) {
	q := QuietHours{Enabled: false, Start: "00:00", End: "23:59", Days: daysAll()}
	if q.ActiveAt(at(t, time.UTC, time.Monday, 12, 0)) {
		t.Errorf("disabled window must never be active")
	}
}

func TestQuietHoursNoDaysNeverActive(t *testing.T) {
	q := QuietHours{Enabled: true, Start: "09:00", End: "17:00"} // zero Days
	if q.ActiveAt(at(t, time.UTC, time.Monday, 12, 0)) {
		t.Errorf("no selected days means never active")
	}
}

func TestQuietHoursEqualStartEndNeverActive(t *testing.T) {
	q := QuietHours{Enabled: true, Start: "09:00", End: "09:00", Days: daysAll()}
	if q.ActiveAt(at(t, time.UTC, time.Monday, 9, 0)) {
		t.Errorf("equal start/end is an empty window, never active")
	}
}

func TestQuietHoursMalformedTimesNeverActive(t *testing.T) {
	for _, tc := range []QuietHours{
		{Enabled: true, Start: "25:00", End: "07:00", Days: daysAll()},
		{Enabled: true, Start: "09:00", End: "9:00", Days: daysAll()},
		{Enabled: true, Start: "", End: "07:00", Days: daysAll()},
	} {
		if tc.ActiveAt(at(t, time.UTC, time.Monday, 12, 0)) {
			t.Errorf("malformed %+v should never be active", tc)
		}
	}
}

func TestQuietHoursNormalizeDisablesMalformed(t *testing.T) {
	q := QuietHours{Enabled: true, Start: "99:99", End: "07:00", Days: daysAll()}
	got := q.Normalize()
	if got.Enabled {
		t.Errorf("malformed window should be disabled by Normalize, got %+v", got)
	}
}

func TestQuietHoursNormalizeEnabledNoDaysDefaultsAll(t *testing.T) {
	q := QuietHours{Enabled: true, Start: "22:00", End: "07:00"} // zero Days
	got := q.Normalize()
	if !got.Enabled {
		t.Fatalf("valid window should stay enabled")
	}
	if got.Days != AllDays() {
		t.Errorf("enabled window with no days should default to all 7, got %+v", got.Days)
	}
}

func TestQuietHoursNormalizeEqualStartEndDisabled(t *testing.T) {
	q := QuietHours{Enabled: true, Start: "08:00", End: "08:00", Days: daysAll()}
	if q.Normalize().Enabled {
		t.Errorf("equal start/end should be disabled by Normalize")
	}
}

func TestQuietHoursMergeTakesEnabledAndDays(t *testing.T) {
	base := QuietHours{Enabled: false, Start: "01:00", End: "02:00"}
	patch := QuietHours{Enabled: true, Days: daysAll()}
	got := base.Merge(patch)
	if !got.Enabled {
		t.Errorf("Merge should take Enabled from patch")
	}
	if got.Days != AllDays() {
		t.Errorf("Merge should take Days from patch")
	}
	if got.Start != "01:00" || got.End != "02:00" {
		t.Errorf("Merge should keep Start/End when patch leaves them empty, got %+v", got)
	}
}

func TestQuietHoursMergeOverwritesTimesWhenSet(t *testing.T) {
	base := QuietHours{Start: "01:00", End: "02:00"}
	patch := QuietHours{Start: "22:00", End: "07:00"}
	got := base.Merge(patch)
	if got.Start != "22:00" || got.End != "07:00" {
		t.Errorf("Merge should overwrite times when patch sets them, got %+v", got)
	}
}

func TestSettingsJSONRoundTripQuietHours(t *testing.T) {
	in := Settings{
		VoiceStyle: VoiceStyleCopper,
		Voicemail:  Voicemail{Enabled: true, RingTimeoutSeconds: 20},
		QuietHours: QuietHours{Enabled: true, Start: "22:00", End: "07:00", Days: daysAll()},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Settings
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round trip: got %+v, want %+v", out, in)
	}
}

func TestSettingsScanLayersQuietHours(t *testing.T) {
	raw := []byte(`{
		"voice_style": "modern",
		"quiet_hours": {
			"enabled": true,
			"start": "23:00",
			"end": "06:30",
			"days": [false, true, true, true, true, true, false]
		}
	}`)
	var patch Settings
	if err := json.Unmarshal(raw, &patch); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	merged := DefaultSettings().Merge(patch).Normalize()
	if !merged.QuietHours.Enabled {
		t.Fatalf("quiet hours should be enabled after merge")
	}
	if merged.QuietHours.Start != "23:00" || merged.QuietHours.End != "06:30" {
		t.Errorf("times not preserved: %+v", merged.QuietHours)
	}
	want := [7]bool{false, true, true, true, true, true, false}
	if merged.QuietHours.Days != want {
		t.Errorf("days not preserved: got %+v want %+v", merged.QuietHours.Days, want)
	}
}

func TestSettingsSilentNowOrsQuietHours(t *testing.T) {
	s := Settings{
		QuietHours: QuietHours{Enabled: true, Start: "22:00", End: "07:00", Days: daysAll()},
	}
	if !s.SilentNow(at(t, time.UTC, time.Monday, 23, 0)) {
		t.Errorf("SilentNow should be true inside the quiet window")
	}
	if s.SilentNow(at(t, time.UTC, time.Monday, 12, 0)) {
		t.Errorf("SilentNow should be false outside the window with SilentMode off")
	}
	s.SilentMode = true
	if !s.SilentNow(at(t, time.UTC, time.Monday, 12, 0)) {
		t.Errorf("SilentNow should be true when SilentMode is on regardless of window")
	}
}
