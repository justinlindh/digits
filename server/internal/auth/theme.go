package auth

// Theme identifies which webapp visual theme a user has selected.
type Theme string

const (
	ThemeC   Theme = "c"   // Home intercom, direction C (default)
	ThemeAOL Theme = "aol" // Online service 1997
)

// Valid reports whether t is a recognized theme identifier.
func (t Theme) Valid() bool {
	return t == ThemeC || t == ThemeAOL
}
