package line

import (
	"regexp"
	"time"
)

// Voice style identifiers persisted in line.Settings and on the wire.
const (
	VoiceStyleCopper = "copper"
	VoiceStyleModern = "modern"
)

// quietHoursTimeRe matches an "HH:MM" 24-hour clock time, hours 00-23 and
// minutes 00-59. Used to validate the Start/End fields of QuietHours.
var quietHoursTimeRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// QuietHours is a per-line scheduled silent window. When Enabled and the
// current household-local time falls inside [Start, End) on a selected day,
// the line behaves as if SilentMode were on: incoming calls do not ring,
// though the light still flashes and the call can still be answered.
//
// Start and End are "HH:MM" 24-hour strings. When Start > End the window
// wraps past midnight (e.g. 22:00 to 07:00 silences overnight). Days is a
// fixed array indexed by time.Weekday (Sunday = 0); a true entry means the
// window applies on that weekday. The array form keeps QuietHours (and thus
// Settings) comparable, so the existing struct-equality round-trip tests hold.
type QuietHours struct {
	Enabled bool    `json:"enabled,omitempty"`
	Start   string  `json:"start,omitempty"`
	End     string  `json:"end,omitempty"`
	Days    [7]bool `json:"days"`
}

// AllDays returns a Days array with every weekday selected. Used as the
// default so a freshly enabled window applies every day until the household
// narrows it.
func AllDays() [7]bool {
	return [7]bool{true, true, true, true, true, true, true}
}

// anyDay reports whether at least one weekday is selected.
func (q QuietHours) anyDay() bool {
	for _, d := range q.Days {
		if d {
			return true
		}
	}
	return false
}

// validQuietTime reports whether s is a well-formed "HH:MM" 24-hour time.
func validQuietTime(s string) bool {
	return quietHoursTimeRe.MatchString(s)
}

// minutesOfDay parses a validated "HH:MM" string into minutes since midnight.
// Callers must have already passed s through validQuietTime.
func minutesOfDay(s string) int {
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	return h*60 + m
}

// ActiveAt reports whether the quiet-hours window is active at local time t.
// t must already be expressed in the household's timezone (callers convert
// via time.Time.In). The function is pure and side-effect free so it can be
// unit-tested across in-window, out-of-window, midnight-wrap, and day-filter
// cases without a clock dependency.
//
// Semantics:
//   - Disabled, malformed, or all-days-off windows are never active.
//   - Start == End is treated as an empty window (never active), avoiding a
//     24-hour "always silent" surprise.
//   - The window is half-open [Start, End): the End minute itself is not
//     silenced, so a 22:00-07:00 window releases exactly at 07:00.
//   - For a wrapping window, the selected weekday is the day the window
//     *opens*. A Monday 22:00-07:00 window stays active into Tuesday 06:59
//     even if Tuesday is not selected.
func (q QuietHours) ActiveAt(t time.Time) bool {
	if !q.Enabled || !q.anyDay() {
		return false
	}
	if !validQuietTime(q.Start) || !validQuietTime(q.End) {
		return false
	}
	start := minutesOfDay(q.Start)
	end := minutesOfDay(q.End)
	if start == end {
		return false
	}
	nowMin := t.Hour()*60 + t.Minute()
	today := t.Weekday()
	if start < end {
		// Same-day window: today must be selected and now in [start, end).
		return q.Days[today] && nowMin >= start && nowMin < end
	}
	// Wrapping window. Either we are in the evening portion [start, midnight)
	// on a selected day, or the morning portion [midnight, end) carried over
	// from the previous day's (selected) opening.
	if nowMin >= start {
		return q.Days[today]
	}
	if nowMin < end {
		yesterday := time.Weekday((int(today) + 6) % 7)
		return q.Days[yesterday]
	}
	return false
}

// Normalize coerces a QuietHours into a safe, storable shape. Invalid times
// or a window with no days selected disable the feature outright (Enabled is
// forced false) so a malformed payload can never silence a line. A newly
// enabled window with no days defaults to every day.
func (q QuietHours) Normalize() QuietHours {
	if !q.Enabled {
		return QuietHours{Days: q.Days}
	}
	if !validQuietTime(q.Start) || !validQuietTime(q.End) || minutesOfDay(q.Start) == minutesOfDay(q.End) {
		return QuietHours{Days: q.Days}
	}
	if !q.anyDay() {
		q.Days = AllDays()
	}
	return q
}

// Merge layers a quiet-hours patch on top of q. Enabled and Days are taken
// from the patch unconditionally (a bool and a fixed array have no "unset"
// sentinel); Start/End are taken only when the patch sets them, so an empty
// DB payload keeps the receiver's values.
func (q QuietHours) Merge(patch QuietHours) QuietHours {
	q.Enabled = patch.Enabled
	q.Days = patch.Days
	if patch.Start != "" {
		q.Start = patch.Start
	}
	if patch.End != "" {
		q.End = patch.End
	}
	return q
}

// Voicemail ring timeout bounds.
const (
	VoicemailRingTimeoutMin = 5
	VoicemailRingTimeoutMax = 60

	DefaultVoicemailRingTimeoutSeconds = 20
)

// Voicemail is the per-line voicemail (answering-machine) configuration.
// Only Enabled and RingTimeoutSeconds are user-configurable; max stored
// messages and retrieval code are constants in digitsd.
type Voicemail struct {
	Enabled            bool `json:"enabled"`
	RingTimeoutSeconds int  `json:"ring_timeout_seconds"`
}

// Settings holds per-line configuration that a household owner can change in
// the web UI. Stored as a single JSONB column on the lines table so new
// fields can be added without schema changes.
type Settings struct {
	VoiceStyle string     `json:"voice_style,omitempty"`
	SilentMode bool       `json:"silent_mode,omitempty"`
	AutoUpdate bool       `json:"auto_update,omitempty"`
	Voicemail  Voicemail  `json:"voicemail"`
	QuietHours QuietHours `json:"quiet_hours"`
}

// DefaultVoicemail returns the voicemail configuration a newly created line
// starts with: enabled, with a 20-second ring timeout so a new line records
// messages out of the box without the household having to opt in.
func DefaultVoicemail() Voicemail {
	return Voicemail{
		Enabled:            true,
		RingTimeoutSeconds: DefaultVoicemailRingTimeoutSeconds,
	}
}

// DefaultSettings returns the settings a newly created line starts with.
// Copper is the default voice style because Digits' physical form (vintage
// handsets) pairs better with POTS-colored audio than raw HD.
func DefaultSettings() Settings {
	return Settings{
		VoiceStyle: VoiceStyleCopper,
		Voicemail:  DefaultVoicemail(),
	}
}

// Merge layers a patch on top of v, overwriting only the fields the patch
// actually sets. Used to apply a DB-loaded voicemail JSON on top of the
// defaults: zero/empty fields keep their default value, except Enabled
// which is overwritten unconditionally (a bool has no "unset" sentinel).
func (v Voicemail) Merge(patch Voicemail) Voicemail {
	v.Enabled = patch.Enabled
	if patch.RingTimeoutSeconds != 0 {
		v.RingTimeoutSeconds = patch.RingTimeoutSeconds
	}
	return v
}

// Normalize substitutes the default value for any field that is zero or out
// of the allowed range.
func (v Voicemail) Normalize() Voicemail {
	if v.RingTimeoutSeconds < VoicemailRingTimeoutMin || v.RingTimeoutSeconds > VoicemailRingTimeoutMax {
		v.RingTimeoutSeconds = DefaultVoicemailRingTimeoutSeconds
	}
	return v
}

// Merge layers a patch on top of s, overwriting only the fields the patch
// actually sets.
func (s Settings) Merge(patch Settings) Settings {
	if patch.VoiceStyle != "" {
		s.VoiceStyle = patch.VoiceStyle
	}
	s.SilentMode = patch.SilentMode
	s.AutoUpdate = patch.AutoUpdate
	s.Voicemail = s.Voicemail.Merge(patch.Voicemail)
	s.QuietHours = s.QuietHours.Merge(patch.QuietHours)
	return s
}

// Normalize rewrites any fields that hold an unknown value to their default.
func (s Settings) Normalize() Settings {
	switch s.VoiceStyle {
	case VoiceStyleCopper, VoiceStyleModern:
		// ok
	default:
		s.VoiceStyle = VoiceStyleCopper
	}
	s.Voicemail = s.Voicemail.Normalize()
	s.QuietHours = s.QuietHours.Normalize()
	return s
}

// SilentNow reports the effective silent state for this line at local time t.
// It is the OR of the explicit SilentMode toggle and an active quiet-hours
// window, so a device treats either as "do not ring". t must already be in
// the household's timezone.
func (s Settings) SilentNow(t time.Time) bool {
	return s.SilentMode || s.QuietHours.ActiveAt(t)
}
