package wifi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ErrInvalidRequest is returned by ConfigureWithDeps for user-facing validation
// failures (missing SSID, etc.) so handlers can return 400 vs. 500.
var ErrInvalidRequest = errors.New("invalid configure request")

// ConfigRequest is the JSON body for POST /api/configure.
type ConfigRequest struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Hidden   bool   `json:"hidden"`
}

// FileSystem abstracts file operations for testing.
type FileSystem interface {
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	Remove(name string) error
	Rename(oldpath, newpath string) error
}

// OSFileSystem is the real filesystem.
type OSFileSystem struct{}

func (OSFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (OSFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func (OSFileSystem) Remove(name string) error {
	return os.Remove(name)
}

func (OSFileSystem) Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
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

const apCheckBinary = "/usr/local/bin/digits-ap-check"

// SystemAPController calls `digits-ap-check down` as a detached transient
// systemd unit via systemd-run --no-block. We cannot invoke digits-ap-check
// directly as a child process because its do_ap_down routine stops
// digits-setup.service, which -- under systemd's default
// KillMode=control-group -- terminates every process in the service's
// cgroup including a child digits-ap-check. By spawning digits-ap-check in
// its own transient cgroup, the teardown script survives the death of its
// caller and runs to completion.
type SystemAPController struct{}

func (SystemAPController) Down() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(
		ctx,
		"systemd-run",
		"--no-block",
		"--collect",
		"--unit=digits-ap-teardown",
		apCheckBinary, "down",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("digits-ap-check down (systemd-run): %w: %s", err, out)
	}
	return nil
}

// Configure writes Wi-Fi config using production filesystem and mount
// implementations. Tests should call ConfigureWithDeps directly with mocks.
func Configure(req ConfigRequest) error {
	return ConfigureWithDeps(req, OSFileSystem{}, SystemMounter{})
}

const (
	backupDir          = "/data/wifi"
	operationalDir     = "/etc/NetworkManager/system-connections"
	wifiConfiguredFlag = "/data/wifi-configured"
	legacyConnFilename = "digits-wifi.nmconnection"
)

// uuidForSSID returns a UUID-shaped string derived from the SSID so
// reconfiguring the same SSID overwrites the previous file rather than
// creating a duplicate. It is a stable identifier, not an RFC 4122 UUID.
func uuidForSSID(ssid string) string {
	h := sha256.Sum256([]byte(ssid))
	s := hex.EncodeToString(h[:16])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// ConfigureWithDeps performs configuration with injectable dependencies.
//
// The flag at wifiConfiguredFlag is written last so a partial failure during
// the operational write does not promote the device to station mode on next
// boot with a half-written config.
func ConfigureWithDeps(req ConfigRequest, fs FileSystem, mounter Mounter) error {
	if req.SSID == "" {
		return fmt.Errorf("%w: ssid is required", ErrInvalidRequest)
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
		return fmt.Errorf("backup write: %w", err)
	}

	if err := mounter.RemountRW(); err != nil {
		return fmt.Errorf("remount rw: %w", err)
	}
	defer func() {
		if err := mounter.RemountRO(); err != nil {
			log.Printf("configure: remount ro failed: %v", err)
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
			log.Printf("configure: legacy cleanup of %s failed: %v", p, err)
		}
	}

	if err := fs.WriteFile(wifiConfiguredFlag, []byte("1\n"), 0600); err != nil {
		return fmt.Errorf("write flag: %w", err)
	}

	return nil
}

// writeAtomic writes data to path via a sibling temp file and atomic rename.
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
