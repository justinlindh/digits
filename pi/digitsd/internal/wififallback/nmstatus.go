package wififallback

import (
	"strings"
	"time"
)

// NMStatusChecker reports whether NetworkManager currently has global
// connectivity. Callers should treat a non-nil error as equivalent to
// "no connectivity" for escalation decisions -- a wedged nmcli is
// indistinguishable from an offline network from the supervisor's
// point of view.
type NMStatusChecker interface {
	HasConnectivity() (bool, error)
}

type nmcliChecker struct {
	run func(args ...string) ([]byte, error)
}

// NewNMCLIChecker returns an NMStatusChecker backed by the nmcli binary.
func NewNMCLIChecker() NMStatusChecker {
	return &nmcliChecker{run: func(args ...string) ([]byte, error) {
		return runCmd(5*time.Second, "nmcli", args...)
	}}
}

func (c *nmcliChecker) HasConnectivity() (bool, error) {
	raw, err := c.run("-t", "-f", "CONNECTIVITY", "general")
	if err != nil {
		return false, err
	}
	state := strings.ToLower(strings.TrimSpace(string(raw)))
	return state == "full", nil
}
