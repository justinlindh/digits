package auth

// CRTMode identifies where the dial-up theme renders the CRT monitor bezel.
// Only meaningful when the user's Theme is ThemeDialup; ignored otherwise.
type CRTMode string

const (
	CRTModeOff        CRTMode = "off"        // no bezel anywhere
	CRTModeConnecting CRTMode = "connecting" // bezel only on /connecting (default)
	CRTModeAll        CRTMode = "all"        // bezel wraps every dialup page
)

// Valid reports whether m is a recognized CRT mode identifier.
func (m CRTMode) Valid() bool {
	return m == CRTModeOff || m == CRTModeConnecting || m == CRTModeAll
}
