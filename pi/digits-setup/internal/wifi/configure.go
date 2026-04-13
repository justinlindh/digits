package wifi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ConfigRequest is the JSON body for POST /api/configure.
type ConfigRequest struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Hidden   bool   `json:"hidden"`
}

// Configurator writes Wi-Fi config and triggers reboot.
type Configurator interface {
	Configure(req ConfigRequest) error
}

// FileSystem abstracts file operations for testing.
type FileSystem interface {
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	Remove(name string) error
}

// Rebooter abstracts the system reboot.
type Rebooter interface {
	ScheduleReboot(delay time.Duration)
}

// OSFileSystem is the real filesystem.
type OSFileSystem struct{}

func (OSFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (OSFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func (OSFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (OSFileSystem) Remove(name string) error {
	return os.Remove(name)
}

// SystemRebooter calls `systemctl reboot`.
type SystemRebooter struct{}

func (SystemRebooter) ScheduleReboot(delay time.Duration) {
	go func() {
		time.Sleep(delay)
		out, err := exec.Command("systemctl", "reboot").CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "reboot failed: %v: %s\n", err, out)
		}
	}()
}

// SystemConfigurator is the production configurator.
type SystemConfigurator struct{}

func (c *SystemConfigurator) Configure(req ConfigRequest) error {
	return ConfigureWithDeps(req, OSFileSystem{}, SystemRebooter{})
}

// uuidForSSID returns a stable UUID-shaped string derived from the SSID.
// Format: 8-4-4-4-12 hex chars. Not a real UUID, but NetworkManager accepts
// anything UUID-shaped in this field. Deterministic so reconfiguring the
// same SSID overwrites the previous file rather than creating a duplicate.
func uuidForSSID(ssid string) string {
	h := sha256.Sum256([]byte(ssid))
	s := hex.EncodeToString(h[:16])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// ConfigureWithDeps performs configuration with injectable dependencies.
func ConfigureWithDeps(req ConfigRequest, fs FileSystem, rebooter Rebooter) error {
	if req.SSID == "" {
		return fmt.Errorf("ssid is required")
	}
	// Write wpa_supplicant.conf
	wpaDir := "/data/wifi"
	if err := fs.MkdirAll(wpaDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", wpaDir, err)
	}

	safeName := SanitizeSSID(req.SSID)
	uuid := uuidForSSID(req.SSID)
	connID := "digits-wifi-" + safeName + "-" + uuid[:6]

	hiddenLine := ""
	if req.Hidden {
		hiddenLine = "hidden=true\n"
	}

	// Write NetworkManager connection file (NM manages wlan0 on the working Pi)
	nmConn := fmt.Sprintf(`[connection]
id=%s
uuid=%s
type=wifi

[wifi]
%smode=infrastructure
ssid=%s

[wifi-security]
key-mgmt=wpa-psk
psk=%s

[ipv4]
method=auto

[ipv6]
addr-gen-mode=default
method=auto

[proxy]
`, connID, uuid, hiddenLine, req.SSID, req.Password)

	nmPath := filepath.Join(wpaDir, "digits-wifi-"+safeName+"-"+uuid[:6]+".nmconnection")
	if err := fs.WriteFile(nmPath, []byte(nmConn), 0600); err != nil {
		return fmt.Errorf("write nmconnection: %w", err)
	}

	// Read back and verify the file was written correctly
	readBack, err := fs.ReadFile(nmPath)
	if err != nil {
		return fmt.Errorf("verify nmconnection: read-back failed: %w", err)
	}
	if string(readBack) != nmConn {
		return fmt.Errorf("verify nmconnection: read-back mismatch (wrote %d bytes, read %d bytes)", len(nmConn), len(readBack))
	}

	// Best-effort cleanup: remove the legacy single-file nmconnection left over
	// from pre-multi-network devices so NetworkManager doesn't load a stale
	// profile alongside the new per-SSID file.
	legacyPath := filepath.Join(wpaDir, "digits-wifi.nmconnection")
	if err := fs.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		// Log but do not fail the configure; a failure here should not block
		// the user's provisioning attempt.
		log.Printf("configure: legacy cleanup of %s failed: %v", legacyPath, err)
	}

	// Set wifi-configured flag
	flagPath := "/data/wifi-configured"
	if err := fs.WriteFile(flagPath, []byte("1\n"), 0644); err != nil {
		return fmt.Errorf("write flag: %w", err)
	}

	// Schedule reboot
	rebooter.ScheduleReboot(5 * time.Second)

	return nil
}
