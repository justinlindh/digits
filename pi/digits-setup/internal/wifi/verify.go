package wifi

import (
	"fmt"
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
	// Stop AP services to release wlan0.
	_, _ = cmd.run("systemctl", "stop", "hostapd")
	_, _ = cmd.run("systemctl", "stop", "dnsmasq")

	// Start NetworkManager so it picks up the backup config.
	_, _ = cmd.run("systemctl", "start", "NetworkManager")

	var connected bool
	for i := 0; i < cfg.maxAttempts; i++ {
		time.Sleep(cfg.pollInterval)

		out, err := cmd.run("nmcli", "general", "status")
		if err != nil {
			continue
		}
		lower := strings.ToLower(out)
		if strings.Contains(lower, "connected") && strings.Contains(lower, "full") {
			connected = true
			break
		}
	}

	// Always restore AP services.
	_, _ = cmd.run("systemctl", "stop", "NetworkManager")
	_, _ = cmd.run("systemctl", "start", "hostapd")
	_, _ = cmd.run("systemctl", "start", "dnsmasq")

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
