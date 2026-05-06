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
	// Tear down AP and hand wlan0 to NetworkManager. Mirrors do_ap_down()
	// in digits-ap-check but without stopping digits-setup (our process).
	slog.Info("wifi verify: tearing down AP")
	cmd.run("systemctl", "stop", "digits-dnsmasq-ap")
	cmd.run("systemctl", "stop", "digits-ap")
	cmd.run("ip", "addr", "flush", "dev", "wlan0")
	cmd.run("ip", "link", "set", "wlan0", "down")

	slog.Info("wifi verify: starting NetworkManager")
	if out, err := cmd.run("systemctl", "start", "NetworkManager"); err != nil {
		slog.Warn("wifi verify: start NetworkManager failed", "error", err, "output", out)
	}

	// Poll for connectivity.
	var connected bool
	var lastScanOutput string
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

	// Capture scan results while NM is still running (before we kill it).
	if !connected {
		lastScanOutput, _ = cmd.run("nmcli", "device", "wifi", "list")
	}

	// Restore AP mode. Mirrors do_ap_up() in digits-ap-check but without
	// the service stops that would kill our own process.
	slog.Info("wifi verify: restoring AP", "connected", connected)

	cmd.run("systemctl", "stop", "NetworkManager")
	cmd.run("systemctl", "stop", "wpa_supplicant@wlan0.service")
	cmd.run("systemctl", "stop", "wpa_supplicant.service")
	cmd.run("rfkill", "unblock", "wifi")

	// Flush wlan0 state left by NM so hostapd can reclaim it.
	cmd.run("ip", "addr", "flush", "dev", "wlan0")
	cmd.run("ip", "link", "set", "wlan0", "down")
	time.Sleep(time.Second)

	// Reconfigure wlan0 static IP for AP mode.
	if out, err := cmd.run("/usr/local/bin/digits-ap-setup"); err != nil {
		slog.Warn("wifi verify: digits-ap-setup failed", "error", err, "output", out)
	}

	// Start hostapd. The brcmfmac driver can reset wlan0 when switching to
	// AP mode, which drops the static IP. Wait for it to settle, then verify
	// the address survived before starting dnsmasq.
	if out, err := cmd.run("systemctl", "start", "digits-ap"); err != nil {
		slog.Warn("wifi verify: start digits-ap failed", "error", err, "output", out)
	}
	time.Sleep(2 * time.Second)

	addrOut, _ := cmd.run("ip", "addr", "show", "dev", "wlan0")
	if !strings.Contains(addrOut, "192.168.4.1") {
		slog.Warn("wifi verify: AP IP lost after hostapd start, re-applying")
		cmd.run("/usr/local/bin/digits-ap-setup")
		time.Sleep(500 * time.Millisecond)
	}

	if out, err := cmd.run("systemctl", "start", "digits-dnsmasq-ap"); err != nil {
		slog.Warn("wifi verify: start digits-dnsmasq-ap failed", "error", err, "output", out)
	}
	slog.Info("wifi verify: AP restored")

	// Delete the backup credentials on failure so NM doesn't retry them.
	if !connected {
		cmd.run("rm", "-f", backupPath)
	}

	if connected {
		return VerifyResult{Connected: true}
	}

	// Determine failure reason using scan data captured while NM was running.
	reason := fmt.Sprintf("Could not connect to %s. Check the password and try again.", ssid)
	if lastScanOutput != "" && !strings.Contains(lastScanOutput, ssid) {
		reason = fmt.Sprintf("Could not find %s. It may be out of range.", ssid)
	}

	return VerifyResult{Error: reason}
}
