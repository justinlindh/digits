package line

// Voice style identifiers persisted in line.Settings and on the wire.
const (
	VoiceStyleCopper = "copper"
	VoiceStyleModern = "modern"
)

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
	VoiceStyle string    `json:"voice_style,omitempty"`
	SilentMode bool      `json:"silent_mode,omitempty"`
	AutoUpdate bool      `json:"auto_update,omitempty"`
	Voicemail  Voicemail `json:"voicemail"`
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
	return s
}
