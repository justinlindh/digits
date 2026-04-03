package wifi

import (
	"fmt"
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

	hiddenLine := ""
	if req.Hidden {
		hiddenLine = "hidden=true\n"
	}

	// Write NetworkManager connection file (NM manages wlan0 on the working Pi)
	nmConn := fmt.Sprintf(`[connection]
id=digits-wifi
uuid=a1b2c3d4-e5f6-7890-abcd-ef1234567890
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
`, hiddenLine, req.SSID, req.Password)

	nmPath := filepath.Join(wpaDir, "digits-wifi.nmconnection")
	if err := fs.WriteFile(nmPath, []byte(nmConn), 0600); err != nil {
		return fmt.Errorf("write nmconnection: %w", err)
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
