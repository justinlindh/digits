package wifi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// ConfigRequest is the JSON body for POST /api/configure.
type ConfigRequest struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Hidden   bool   `json:"hidden"`
}

// Configurator writes Wi-Fi config.
type Configurator interface {
	Configure(req ConfigRequest) error
}

// FileSystem abstracts file operations for testing.
type FileSystem interface {
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	Remove(name string) error
	Rename(oldpath, newpath string) error
	Stat(name string) (os.FileInfo, error)
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

func (OSFileSystem) Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

func (OSFileSystem) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

// Mounter abstracts remounting the root filesystem.
type Mounter interface {
	RemountRW() error
	RemountRO() error
}

// SystemMounter calls `mount -o remount,{rw,ro} /`.
type SystemMounter struct{}

func (SystemMounter) RemountRW() error {
	out, err := exec.Command("mount", "-o", "remount,rw", "/").CombinedOutput()
	if err != nil {
		return fmt.Errorf("remount rw: %w: %s", err, out)
	}
	return nil
}

func (SystemMounter) RemountRO() error {
	out, err := exec.Command("mount", "-o", "remount,ro", "/").CombinedOutput()
	if err != nil {
		return fmt.Errorf("remount ro: %w: %s", err, out)
	}
	return nil
}

// APController abstracts tearing down the captive-portal AP.
type APController interface {
	Down() error
}

// SystemAPController calls `digits-ap-check down`.
type SystemAPController struct{}

func (SystemAPController) Down() error {
	out, err := exec.Command("/usr/local/bin/digits-ap-check", "down").CombinedOutput()
	if err != nil {
		return fmt.Errorf("digits-ap-check down: %w: %s", err, out)
	}
	return nil
}

// SystemConfigurator is the production configurator.
type SystemConfigurator struct{}

func (c *SystemConfigurator) Configure(req ConfigRequest) error {
	return ConfigureWithDeps(req, OSFileSystem{}, SystemMounter{})
}

const (
	// backupDir is the persistent Wi-Fi config store on /data. Survives rootfs
	// replacement on OTA image upgrades.
	backupDir = "/data/wifi"

	// operationalDir is where NetworkManager reads connection profiles at
	// runtime. Lives on the read-only rootfs and requires a remount,rw to
	// write.
	operationalDir = "/etc/NetworkManager/system-connections"

	// wifiConfiguredFlag marks the system as provisioned; digits-ap-check
	// reads this on boot to choose station vs. AP mode.
	wifiConfiguredFlag = "/data/wifi-configured"

	// legacyConnFilename is the pre-multi-network single filename we clean up
	// in both stores on a successful configure.
	legacyConnFilename = "digits-wifi.nmconnection"
)

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
func ConfigureWithDeps(req ConfigRequest, fs FileSystem, mounter Mounter) error {
	if req.SSID == "" {
		return fmt.Errorf("ssid is required")
	}

	safeName := SanitizeSSID(req.SSID)
	uuid := uuidForSSID(req.SSID)
	connID := "digits-wifi-" + safeName + "-" + uuid[:6]
	filename := connID + ".nmconnection"

	hiddenLine := ""
	if req.Hidden {
		hiddenLine = "hidden=true\n"
	}

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

	data := []byte(nmConn)

	// 1. Persistent backup write (always writable).
	backupPath := filepath.Join(backupDir, filename)
	if err := writeAtomic(fs, backupPath, data, 0600); err != nil {
		return fmt.Errorf("backup write: %w", err)
	}
	if err := verifyFile(fs, backupPath, data); err != nil {
		return fmt.Errorf("backup verify: %w", err)
	}

	// 2. Operational write behind a remount.
	if err := mounter.RemountRW(); err != nil {
		return fmt.Errorf("remount rw: %w", err)
	}
	opPath := filepath.Join(operationalDir, filename)
	writeErr := writeAtomic(fs, opPath, data, 0600)
	verifyErr := error(nil)
	if writeErr == nil {
		verifyErr = verifyFile(fs, opPath, data)
	}

	// 3. Legacy cleanup in both stores. Best-effort only; never fails the
	// configure.
	legacyPaths := []string{
		filepath.Join(backupDir, legacyConnFilename),
		filepath.Join(operationalDir, legacyConnFilename),
	}
	for _, p := range legacyPaths {
		if err := fs.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("configure: legacy cleanup of %s failed: %v", p, err)
		}
	}

	// 4. Remount ro regardless of write outcome, then return any earlier error.
	if err := mounter.RemountRO(); err != nil {
		log.Printf("configure: remount ro failed: %v", err)
	}
	if writeErr != nil {
		return fmt.Errorf("operational write: %w", writeErr)
	}
	if verifyErr != nil {
		return fmt.Errorf("operational verify: %w", verifyErr)
	}

	// 5. Set the configured flag last so a partial failure above does not
	// promote the device to station mode on next boot.
	if err := fs.WriteFile(wifiConfiguredFlag, []byte("1\n"), 0644); err != nil {
		return fmt.Errorf("write flag: %w", err)
	}

	return nil
}

// verifyFile reads back a file and compares it to the expected bytes.
func verifyFile(fs FileSystem, path string, want []byte) error {
	got, err := fs.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read-back %s: %w", path, err)
	}
	if string(got) != string(want) {
		return fmt.Errorf("read-back mismatch for %s (wrote %d bytes, read %d bytes)", path, len(want), len(got))
	}
	return nil
}

// writeAtomic writes data to path via a sibling temp file and atomic rename.
// mkdir is called for the parent dir first. On rename failure the temp file
// is best-effort removed.
func writeAtomic(fs FileSystem, path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := fs.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := fs.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write temp %s: %w", tmp, err)
	}
	if err := fs.Rename(tmp, path); err != nil {
		_ = fs.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}
