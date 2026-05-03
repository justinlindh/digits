package line

// Voice style identifiers persisted in line.Settings and on the wire.
const (
	VoiceStyleCopper = "copper"
	VoiceStyleModern = "modern"
)

// Settings holds per-line configuration that a household owner can change in
// the web UI. Stored as a single JSONB column on the lines table so new
// fields can be added without schema changes.
type Settings struct {
	VoiceStyle string `json:"voice_style,omitempty"`
	SilentMode bool   `json:"silent_mode,omitempty"`
	AutoUpdate bool   `json:"auto_update,omitempty"`
}

// DefaultSettings returns the settings a newly created line starts with.
// Copper is the default voice style because Digits' physical form (vintage
// handsets) pairs better with POTS-colored audio than raw HD.
func DefaultSettings() Settings {
	return Settings{VoiceStyle: VoiceStyleCopper}
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
	return s
}

// EffectiveSilent returns whether the device should treat the line as silent
// at ring time. The household-wide DND flag and the per-line silent flag are
// combined with OR: silence if either is set.
func EffectiveSilent(s Settings, householdDND bool) bool {
	return householdDND || s.SilentMode
}
