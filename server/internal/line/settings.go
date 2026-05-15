package line

import (
	"regexp"
	"strings"
)

// Voice style identifiers persisted in line.Settings and on the wire.
const (
	VoiceStyleCopper = "copper"
	VoiceStyleModern = "modern"
)

// Voicemail field clamp bounds and default retrieval code. Kept as package
// constants so the handler validation, Normalize(), and tests reference the
// same numbers.
const (
	VoicemailRingTimeoutMin = 5
	VoicemailRingTimeoutMax = 60
	VoicemailMaxMessageMin  = 15
	VoicemailMaxMessageMax  = 180
	VoicemailMaxStoredMin   = 5
	VoicemailMaxStoredMax   = 200

	DefaultVoicemailRingTimeoutSeconds = 20
	DefaultVoicemailMaxMessageSeconds  = 90
	DefaultVoicemailMaxStoredMessages  = 50
	DefaultVoicemailRetrievalCode      = "*98"
)

// retrievalCodeFormatRegex pins the charset and length of a voicemail
// retrieval code. The trailing semantic check (must contain * or #) is
// done with strings.ContainsAny so a plain numeric prefix like "1234"
// can't shadow a 7-digit dial; that check lives in IsValidRetrievalCode.
var retrievalCodeFormatRegex = regexp.MustCompile(`^[0-9*#]{2,6}$`)

// IsValidRetrievalCode reports whether code passes both the format regex
// and the must-contain-star-or-hash rule. Exported so the HTTP handler and
// Normalize() can share one definition.
func IsValidRetrievalCode(code string) bool {
	return retrievalCodeFormatRegex.MatchString(code) && strings.ContainsAny(code, "*#")
}

// Voicemail is the per-line voicemail (answering-machine) configuration.
// Wire field names match what the digitsd receiver expects. The integer
// time fields use seconds on the wire; the daemon converts to time.Duration
// when applying to its internal config.
//
// Inner fields deliberately omit omitempty to match the wire contract: a
// stored row carries every field literally so a future read can tell
// "Enabled was explicitly false" apart from "field was absent". Empty /
// out-of-range values are still healed by Normalize on read.
type Voicemail struct {
	Enabled            bool   `json:"enabled"`
	RingTimeoutSeconds int    `json:"ring_timeout_seconds"`
	MaxMessageSeconds  int    `json:"max_message_seconds"`
	MaxStoredMessages  int    `json:"max_stored_messages"`
	RetrievalCode      string `json:"retrieval_code"`
}

// Settings holds per-line configuration that a household owner can change in
// the web UI. Stored as a single JSONB column on the lines table so new
// fields can be added without schema changes.
//
// Voicemail intentionally has no omitempty: encoding/json's omitempty has
// no effect on value-typed structs anyway, and dropping it documents the
// "always carry the full block" contract.
type Settings struct {
	VoiceStyle string    `json:"voice_style,omitempty"`
	SilentMode bool      `json:"silent_mode,omitempty"`
	AutoUpdate bool      `json:"auto_update,omitempty"`
	Voicemail  Voicemail `json:"voicemail"`
}

// DefaultVoicemail returns the voicemail configuration a newly created line
// starts with: disabled, with classic answering-machine defaults so that the
// first time a household toggles it on, the behavior matches expectations.
func DefaultVoicemail() Voicemail {
	return Voicemail{
		Enabled:            false,
		RingTimeoutSeconds: DefaultVoicemailRingTimeoutSeconds,
		MaxMessageSeconds:  DefaultVoicemailMaxMessageSeconds,
		MaxStoredMessages:  DefaultVoicemailMaxStoredMessages,
		RetrievalCode:      DefaultVoicemailRetrievalCode,
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
	if patch.MaxMessageSeconds != 0 {
		v.MaxMessageSeconds = patch.MaxMessageSeconds
	}
	if patch.MaxStoredMessages != 0 {
		v.MaxStoredMessages = patch.MaxStoredMessages
	}
	if patch.RetrievalCode != "" {
		v.RetrievalCode = patch.RetrievalCode
	}
	return v
}

// Normalize substitutes the default value for any field that is zero or out
// of the allowed range. Defense in depth so corrupt on-disk data can never
// propagate to the daemon (which would, for instance, refuse to record a
// message if MaxMessageSeconds is zero).
func (v Voicemail) Normalize() Voicemail {
	if v.RingTimeoutSeconds < VoicemailRingTimeoutMin || v.RingTimeoutSeconds > VoicemailRingTimeoutMax {
		v.RingTimeoutSeconds = DefaultVoicemailRingTimeoutSeconds
	}
	if v.MaxMessageSeconds < VoicemailMaxMessageMin || v.MaxMessageSeconds > VoicemailMaxMessageMax {
		v.MaxMessageSeconds = DefaultVoicemailMaxMessageSeconds
	}
	if v.MaxStoredMessages < VoicemailMaxStoredMin || v.MaxStoredMessages > VoicemailMaxStoredMax {
		v.MaxStoredMessages = DefaultVoicemailMaxStoredMessages
	}
	if !IsValidRetrievalCode(v.RetrievalCode) {
		v.RetrievalCode = DefaultVoicemailRetrievalCode
	}
	return v
}

// Merge layers a patch on top of s, overwriting only the fields the patch
// actually sets. This is how we apply a DB-loaded settings JSON on top of
// the defaults: missing fields keep their default value.
func (s Settings) Merge(patch Settings) Settings {
	if patch.VoiceStyle != "" {
		s.VoiceStyle = patch.VoiceStyle
	}
	s.SilentMode = patch.SilentMode
	s.AutoUpdate = patch.AutoUpdate
	s.Voicemail = s.Voicemail.Merge(patch.Voicemail)
	return s
}

// Normalize rewrites any fields that hold an unknown value to their default.
// Use this after unmarshaling untrusted input to avoid propagating garbage.
func (s Settings) Normalize() Settings {
	switch s.VoiceStyle {
	case VoiceStyleCopper, VoiceStyleModern:
		// ok
	default:
		s.VoiceStyle = VoiceStyleCopper
	}
	s.Voicemail = s.Voicemail.Normalize()
	return s
}
