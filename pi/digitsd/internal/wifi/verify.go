package wifi

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// VerifyResult reports whether Wi-Fi verification succeeded.
type VerifyResult struct {
	Connected bool
	Error     string
}

// cmdRunner abstracts command execution for testing.
type cmdRunner interface {
	run(name string, args ...string) (string, error)
}

// systemCmdRunner executes real system commands.
type systemCmdRunner struct{}

func (systemCmdRunner) run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// verifyConfig holds tunable parameters for the verification polling loop.
type verifyConfig struct {
	pollInterval time.Duration
	maxAttempts  int
}

var defaultVerifyConfig = verifyConfig{
	pollInterval: 2 * time.Second,
	maxAttempts:  10,
}

// Verify stops the captive-portal AP, starts NetworkManager, polls for
// connectivity, then restores the AP regardless of outcome.
func Verify(ssid, backupPath string) VerifyResult {
	return verifyWithConfig(ssid, backupPath, systemCmdRunner{}, defaultVerifyConfig)
}

func verifyWithConfig(ssid, backupPath string, cmd cmdRunner, cfg verifyConfig) VerifyResult {
	slog.Info("wifi verify: stopping AP services")
	if out, err := cmd.run("systemctl", "stop", "digits-dnsmasq-ap"); err != nil {
		slog.Warn("wifi verify: stop digits-dnsmasq-ap failed", "error", err, "output", out)
	}
	if out, err := cmd.run("systemctl", "stop", "digits-ap"); err != nil {
		slog.Warn("wifi verify: stop digits-ap failed", "error", err, "output", out)
	}

	slog.Info("wifi verify: starting NetworkManager")
	if out, err := cmd.run("systemctl", "start", "NetworkManager"); err != nil {
		slog.Warn("wifi verify: start NetworkManager failed", "error", err, "output", out)
	}

	var connected bool
	for i := 0; i < cfg.maxAttempts; i++ {
		time.Sleep(cfg.pollInterval)

		out, err := cmd.run("nmcli", "general", "status")
		if err != nil {
			slog.Info("wifi verify: nmcli poll failed", "attempt", i+1, "error", err)
			continue
		}
		lower := strings.ToLower(out)
		slog.Info("wifi verify: nmcli poll", "attempt", i+1, "status", strings.TrimSpace(out))
		if strings.Contains(lower, "connected") && strings.Contains(lower, "full") {
			connected = true
			break
		}
	}

	slog.Info("wifi verify: restoring AP services", "connected", connected)
	if out, err := cmd.run("systemctl", "stop", "NetworkManager"); err != nil {
		slog.Warn("wifi verify: stop NetworkManager failed", "error", err, "output", out)
	}
	if out, err := cmd.run("systemctl", "start", "digits-ap"); err != nil {
		slog.Warn("wifi verify: start digits-ap failed", "error", err, "output", out)
	}
	time.Sleep(500 * time.Millisecond)
	if out, err := cmd.run("systemctl", "start", "digits-dnsmasq-ap"); err != nil {
		slog.Warn("wifi verify: start digits-dnsmasq-ap failed", "error", err, "output", out)
	}
	slog.Info("wifi verify: AP services restored")

	if connected {
		return VerifyResult{Connected: true}
	}

	// Determine failure reason.
	reason := fmt.Sprintf("Could not connect to %s. Check the password and try again.", ssid)

	// Check if the SSID is visible from the last NM scan.
	out, err := cmd.run("nmcli", "device", "wifi", "list")
	if err == nil && !strings.Contains(out, ssid) {
		reason = fmt.Sprintf("Could not find %s. It may be out of range.", ssid)
	}

	return VerifyResult{Error: reason}
}
