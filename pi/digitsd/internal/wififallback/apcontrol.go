package wififallback

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// APController brings the setup-mode access point up and down and reports
// whether a client is associated with it.
type APController interface {
	Up() error
	Down() error
	HasClient() (bool, error)
}

type scriptAPController struct {
	run    func(args ...string) error
	runOut func(args ...string) ([]byte, error)
}

// NewScriptAPController returns an APController backed by
// /usr/local/bin/digits-ap-check for Up/Down and `iw dev wlan0 station dump`
// for HasClient.
func NewScriptAPController() APController {
	return &scriptAPController{
		run:    runCombinedScript,
		runOut: runStdoutQuick,
	}
}

// runCombinedScript runs a command with 30s timeout (for digits-ap-check which
// can take 10-20s to stop NetworkManager and bring up hostapd/dnsmasq/portal).
func runCombinedScript(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v failed: %w: %s", args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runStdoutQuick runs a command with 5s timeout (for iw which is quick).
func runStdoutQuick(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v failed: %w: %s", args, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (s *scriptAPController) Up() error {
	return s.run("/usr/local/bin/digits-ap-check", "up")
}

func (s *scriptAPController) Down() error {
	return s.run("/usr/local/bin/digits-ap-check", "down")
}

func (s *scriptAPController) HasClient() (bool, error) {
	out, err := s.runOut("iw", "dev", "wlan0", "station", "dump")
	if err != nil {
		return false, err
	}
	return strings.Contains(string(out), "Station "), nil
}
