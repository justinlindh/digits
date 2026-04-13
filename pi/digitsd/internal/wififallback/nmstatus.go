package wififallback

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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
	return &nmcliChecker{run: execNmcli}
}

// execNmcli runs nmcli with the given args, applying a 5 second timeout
// and capturing stderr so caller errors are actionable.
func execNmcli(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nmcli", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("nmcli %v: %w: %s", args, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (c *nmcliChecker) HasConnectivity() (bool, error) {
	raw, err := c.run("-t", "-f", "CONNECTIVITY", "general")
	if err != nil {
		return false, err
	}
	state := strings.ToLower(strings.TrimSpace(string(raw)))
	return state == "full", nil
}
