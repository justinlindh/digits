package wififallback

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// NMStatusChecker reports whether NetworkManager currently has global connectivity.
type NMStatusChecker interface {
	HasConnectivity() (bool, error)
}

// nmcliChecker queries nmcli -t -f CONNECTIVITY general and treats "full" as connectivity.
type nmcliChecker struct {
	run func(args ...string) ([]byte, error)
}

// NewNMCLIChecker returns an NMStatusChecker backed by the nmcli binary.
func NewNMCLIChecker() NMStatusChecker {
	return &nmcliChecker{run: execNmcli}
}

func execNmcli(args ...string) ([]byte, error) {
	cmd := exec.Command("nmcli", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("nmcli %v: %w", args, err)
	}
	return out.Bytes(), nil
}

func (c *nmcliChecker) HasConnectivity() (bool, error) {
	raw, err := c.run("-t", "-f", "CONNECTIVITY", "general")
	if err != nil {
		return false, err
	}
	state := strings.ToLower(strings.TrimSpace(string(raw)))
	return state == "full", nil
}
