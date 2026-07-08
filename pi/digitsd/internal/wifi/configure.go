package wifi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// ErrInvalidRequest is returned by SaveToBackup for user-facing validation
// failures (missing SSID, etc.) so handlers can return 400 vs. 500.
var ErrInvalidRequest = errors.New("invalid configure request")

// ConfigRequest is the JSON body for POST /api/configure.
type ConfigRequest struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Hidden   bool   `json:"hidden"`
}

// fileSystem abstracts file operations for testing.
type fileSystem interface {
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	Remove(name string) error
	Rename(oldpath, newpath string) error
}

// osFileSystem is the real filesystem.
type osFileSystem struct{}

func (osFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (osFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func (osFileSystem) Remove(name string) error {
	return os.Remove(name)
}

func (osFileSystem) Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

// mounter abstracts remounting the root filesystem.
type mounter interface {
	RemountRW() error
	RemountRO() error
}

// systemMounter calls `mount -o remount,{rw,ro} /`.
type systemMounter struct{}

func (systemMounter) RemountRW() error {
	out, err := exec.Command("mount", "-o", "remount,rw", "/").CombinedOutput()
	if err != nil {
		return fmt.Errorf("remount rw: %w: %s", err, out)
	}
	return nil
}

func (systemMounter) RemountRO() error {
	out, err := exec.Command("mount", "-o", "remount,ro", "/").CombinedOutput()
	if err != nil {
		return fmt.Errorf("remount ro: %w: %s", err, out)
	}
	return nil
}

const (
	backupDir          = "/data/wifi"
	operationalDir     = "/etc/NetworkManager/system-connections"
	legacyConnFilename = "digits-wifi.nmconnection"
)

// ConfiguredFlagPath is the sentinel file that indicates Wi-Fi has been
// provisioned. CommitToOperational writes it; its absence triggers AP mode on
// next boot, and the *#SETUP# service code removes it to re-trigger setup.
const ConfiguredFlagPath = "/data/wifi-configured"

// uuidForSSID returns a UUID-shaped string derived from the SSID so
// reconfiguring the same SSID overwrites the previous file rather than
// creating a duplicate. It is a stable identifier, not an RFC 4122 UUID.
func uuidForSSID(ssid string) string {
	h := sha256.Sum256([]byte(ssid))
	s := hex.EncodeToString(h[:16])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// SaveToBackup writes Wi-Fi config to the backup directory only (not
// operational, no flag). Returns the backup file path so the caller can pass
// it to Verify and CommitToOperational.
func SaveToBackup(req ConfigRequest) (string, error) {
	return saveToBackupWithDeps(req, osFileSystem{})
}

func saveToBackupWithDeps(req ConfigRequest, fs fileSystem) (string, error) {
	if req.SSID == "" {
		return "", fmt.Errorf("%w: ssid is required", ErrInvalidRequest)
	}

	safeName := SanitizeSSID(req.SSID)
	uuid := uuidForSSID(req.SSID)
	connID := "digits-wifi-" + safeName + "-" + uuid[:6]
	filename := connID + ".nmconnection"

	hiddenLine := ""
	if req.Hidden {
		hiddenLine = "hidden=true\n"
	}

	data := []byte(fmt.Sprintf(`[connection]
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
`, connID, uuid, hiddenLine, req.SSID, req.Password))

	backupPath := filepath.Join(backupDir, filename)
	if err := writeAtomic(fs, backupPath, data, 0600); err != nil {
		return "", fmt.Errorf("backup write: %w", err)
	}
	return backupPath, nil
}

// CommitToOperational copies the backup config to the operational directory,
// does legacy cleanup, and writes the wifi-configured flag.
func CommitToOperational(backupPath string) error {
	return commitWithDeps(backupPath, osFileSystem{}, systemMounter{})
}

func commitWithDeps(backupPath string, fs fileSystem, m mounter) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}

	filename := filepath.Base(backupPath)

	if err := m.RemountRW(); err != nil {
		return fmt.Errorf("remount rw: %w", err)
	}
	defer func() {
		if err := m.RemountRO(); err != nil {
			log.Printf("commit: remount ro failed: %v", err)
		}
	}()

	opPath := filepath.Join(operationalDir, filename)
	if err := writeAtomic(fs, opPath, data, 0600); err != nil {
		return fmt.Errorf("operational write: %w", err)
	}

	// The pre-PR-170 scheme produced exactly one file with this hardcoded
	// name; removing it by name (not by pattern) is deliberate so we don't
	// accidentally stomp on current digits-wifi-*.nmconnection entries.
	for _, p := range []string{
		filepath.Join(backupDir, legacyConnFilename),
		filepath.Join(operationalDir, legacyConnFilename),
	} {
		if err := fs.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("commit: legacy cleanup of %s failed: %v", p, err)
		}
	}

	if err := fs.WriteFile(ConfiguredFlagPath, []byte("1\n"), 0600); err != nil {
		return fmt.Errorf("write flag: %w", err)
	}

	return nil
}

// writeAtomic writes data to path via a sibling temp file and atomic rename.
func writeAtomic(fs fileSystem, path string, data []byte, perm os.FileMode) error {
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
