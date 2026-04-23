package auth

// Appearance selects the day/night palette variant within the intercom theme.
// Only meaningful when the user's Theme is ThemeIntercom; ignored otherwise.
type Appearance string

const (
	AppearanceDay   Appearance = "day"   // warm paper, walnut, brass (default)
	AppearanceNight Appearance = "night" // intercom at night: walnut charcoal, backlit brass
)

// Valid reports whether a is a recognized appearance identifier.
func (a Appearance) Valid() bool {
	return a == AppearanceDay || a == AppearanceNight
}
