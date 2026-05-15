package wifi

import (
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
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
	pollInterval    time.Duration
	maxAttempts     int
	postFlushDelay  time.Duration
	postHostapdDelay time.Duration
	postReapplyDelay time.Duration
}

var defaultVerifyConfig = verifyConfig{
	pollInterval:    2 * time.Second,
	maxAttempts:     10,
	postFlushDelay:  time.Second,
	postHostapdDelay: 2 * time.Second,
	postReapplyDelay: 500 * time.Millisecond,
}

// Verify stops the captive-portal AP, starts NetworkManager, polls for
// connectivity, then restores the AP regardless of outcome.
func Verify(ssid, backupPath string, hidden bool) VerifyResult {
	return verifyWithConfig(ssid, backupPath, hidden, systemCmdRunner{}, defaultVerifyConfig)
}

func verifyWithConfig(ssid, backupPath string, hidden bool, cmd cmdRunner, cfg verifyConfig) VerifyResult {
	// Tear down AP and hand wlan0 to NetworkManager. Mirrors do_ap_down()
	// in digits-ap-check but without stopping digits-setup (our process).
	slog.Info("wifi verify: tearing down AP")
	_, _ = cmd.run("systemctl", "stop", "digits-dnsmasq-ap")
	_, _ = cmd.run("systemctl", "stop", "digits-ap")
	_, _ = cmd.run("ip", "addr", "flush", "dev", "wlan0")
	_, _ = cmd.run("ip", "link", "set", "wlan0", "down")

	// Copy the backup .nmconnection to NM's operational directory so NM
	// knows about the network. Rootfs is read-only, so remount rw first.
	slog.Info("wifi verify: installing connection for NM", "path", backupPath)
	_, _ = cmd.run("mount", "-o", "remount,rw", "/")
	defer func() { _, _ = cmd.run("mount", "-o", "remount,ro", "/") }()
	filename := filepath.Base(backupPath)
	opPath := filepath.Join("/etc/NetworkManager/system-connections", filename)
	if out, err := cmd.run("cp", backupPath, opPath); err != nil {
		slog.Warn("wifi verify: copy to NM dir failed", "error", err, "output", out)
	}
	_, _ = cmd.run("chmod", "600", opPath)

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

	_, _ = cmd.run("systemctl", "stop", "NetworkManager")
	_, _ = cmd.run("systemctl", "stop", "wpa_supplicant@wlan0.service")
	_, _ = cmd.run("systemctl", "stop", "wpa_supplicant.service")
	_, _ = cmd.run("rfkill", "unblock", "wifi")

	// Flush wlan0 state left by NM so hostapd can reclaim it.
	_, _ = cmd.run("ip", "addr", "flush", "dev", "wlan0")
	_, _ = cmd.run("ip", "link", "set", "wlan0", "down")
	time.Sleep(cfg.postFlushDelay)

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
	time.Sleep(cfg.postHostapdDelay)

	addrOut, _ := cmd.run("ip", "addr", "show", "dev", "wlan0")
	if !strings.Contains(addrOut, "192.168.4.1") {
		slog.Warn("wifi verify: AP IP lost after hostapd start, re-applying")
		_, _ = cmd.run("/usr/local/bin/digits-ap-setup")
		time.Sleep(cfg.postReapplyDelay)
	}

	if out, err := cmd.run("systemctl", "start", "digits-dnsmasq-ap"); err != nil {
		slog.Warn("wifi verify: start digits-dnsmasq-ap failed", "error", err, "output", out)
	}
	slog.Info("wifi verify: AP restored")

	// Delete credentials on failure so NM doesn't retry them.
	if !connected {
		_, _ = cmd.run("rm", "-f", backupPath)
		_, _ = cmd.run("rm", "-f", opPath)
	}

	if connected {
		return VerifyResult{Connected: true}
	}

	// Determine failure reason using scan data captured while NM was running.
	// Hidden networks won't appear in scans, so skip the visibility check.
	reason := fmt.Sprintf("Could not connect to %s. Check the password and try again.", ssid)
	if !hidden && lastScanOutput != "" && !strings.Contains(lastScanOutput, ssid) {
		reason = fmt.Sprintf("Could not find %s. It may be out of range.", ssid)
	}

	return VerifyResult{Error: reason}
}
