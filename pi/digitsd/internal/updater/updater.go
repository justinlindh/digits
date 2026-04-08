// Package updater checks for and applies Pi+Pico software updates.
package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type CheckResult struct {
	PiAvailable bool
	PiVersion   string
	PiSHA256    string
	PiURL       string
	FWAvailable bool
	FWVersion   string
	FWSHA256    string
	FWURL       string
}

type Config struct {
	ServerBaseURL    string
	CurrentPiVersion string
	CurrentFWVersion string
	StagingDir       string // default: /data/digits/staging
	FlashScript      string // default: /usr/local/bin/flash-pico.sh
	BinaryPath       string // default: os.Executable() result
	FirmwarePath     string // default: /data/digits/firmware.elf
}

type Updater struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) *Updater {
	if cfg.StagingDir == "" {
		cfg.StagingDir = "/data/digits/staging"
	}
	if cfg.FlashScript == "" {
		cfg.FlashScript = "/usr/local/bin/flash-pico.sh"
	}
	if cfg.BinaryPath == "" {
		exe, err := os.Executable()
		if err != nil {
			log.Printf("updater: WARNING: os.Executable() failed, falling back to /usr/local/bin/digitsd: %v", err)
			cfg.BinaryPath = "/usr/local/bin/digitsd"
		} else {
			cfg.BinaryPath = exe
		}
	}
	log.Printf("updater: BinaryPath=%s", cfg.BinaryPath)
	if cfg.FirmwarePath == "" {
		cfg.FirmwarePath = "/data/digits/firmware.elf"
	}
	return &Updater{
		cfg:    cfg,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// ReleaseIndex mirrors the server's release index structure.
type ReleaseIndex struct {
	Pi       ComponentIndex `json:"pi"`
	Firmware ComponentIndex `json:"firmware"`
}

// ComponentIndex holds the latest version and full history for one component.
type ComponentIndex struct {
	Latest   string              `json:"latest"`
	Releases map[string]*Release `json:"releases"`
}

// Release describes a single versioned artifact.
type Release struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	URL     string `json:"url"`
	Date    string `json:"date,omitempty"`
}

// CheckVersion queries the release index from /api/updates/releases and
// compares available versions against the currently running versions.
// If targetPi/targetFW are empty, the latest version is used.
// If the resolved version matches the current version, that component is skipped.
func (u *Updater) CheckVersion(targetPi, targetFW string) (*CheckResult, error) {
	resp, err := u.client.Get(u.cfg.ServerBaseURL + "/api/updates/releases")
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch releases: status %d", resp.StatusCode)
	}

	var idx ReleaseIndex
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}

	result := &CheckResult{}

	// Resolve Pi target
	piTarget := targetPi
	if piTarget == "" {
		piTarget = idx.Pi.Latest
	}
	if piTarget != "" && piTarget != u.cfg.CurrentPiVersion {
		rel, ok := idx.Pi.Releases[piTarget]
		if !ok {
			return nil, fmt.Errorf("pi version %s not found in release index", piTarget)
		}
		result.PiAvailable = true
		result.PiVersion = rel.Version
		result.PiSHA256 = rel.SHA256
		result.PiURL = rel.URL
	}

	// Resolve FW target
	fwTarget := targetFW
	if fwTarget == "" {
		fwTarget = idx.Firmware.Latest
	}
	if fwTarget != "" && fwTarget != u.cfg.CurrentFWVersion {
		rel, ok := idx.Firmware.Releases[fwTarget]
		if !ok {
			return nil, fmt.Errorf("firmware version %s not found in release index", fwTarget)
		}
		result.FWAvailable = true
		result.FWVersion = rel.Version
		result.FWSHA256 = rel.SHA256
		result.FWURL = rel.URL
	}

	return result, nil
}

// Download downloads an artifact from a URL, verifies SHA256, and writes it
// atomically to the staging directory.
func (u *Updater) Download(url, localName, expectedSHA string) (string, error) {
	if err := os.MkdirAll(u.cfg.StagingDir, 0755); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}
	destPath := filepath.Join(u.cfg.StagingDir, localName)

	resp, err := u.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}

	f, err := os.Create(destPath + ".tmp")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		os.Remove(destPath + ".tmp")
		return "", fmt.Errorf("download write: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(destPath + ".tmp")
		return "", fmt.Errorf("flush download: %w", err)
	}

	gotSHA := hex.EncodeToString(h.Sum(nil))
	if expectedSHA != "" && gotSHA != expectedSHA {
		os.Remove(destPath + ".tmp")
		return "", fmt.Errorf("sha256 mismatch: got %s, want %s", gotSHA, expectedSHA)
	}

	if err := os.Rename(destPath+".tmp", destPath); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}

	log.Printf("updater: downloaded %s from %s (sha256=%s)", localName, url, gotSHA)
	return destPath, nil
}

// ApplyPiUpdate replaces the digitsd binary on the read-only rootfs and exits
// (systemd restarts). Temporarily remounts / as rw for the copy, then restores ro.
// expectedVersion is checked against the installed binary's -version output before exiting.
func (u *Updater) ApplyPiUpdate(stagedBinary, expectedVersion string) error {
	if err := os.Chmod(stagedBinary, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// Remount rootfs read-write so we can replace the binary.
	if err := exec.Command("sudo", "mount", "-o", "remount,rw", "/").Run(); err != nil {
		return fmt.Errorf("remount rw: %w", err)
	}

	// Copy staged binary via sudo using tmp+mv to avoid "text file busy" on the
	// running executable. Direct cp fails because the kernel won't let you
	// overwrite an open binary.
	tmpDst := u.cfg.BinaryPath + ".tmp"
	if err := exec.Command("sudo", "cp", stagedBinary, tmpDst).Run(); err != nil {
		exec.Command("sudo", "mount", "-o", "remount,ro", "/").Run()
		return fmt.Errorf("copy binary: %w", err)
	}
	if err := exec.Command("sudo", "chmod", "0755", tmpDst).Run(); err != nil {
		exec.Command("sudo", "rm", "-f", tmpDst).Run()
		exec.Command("sudo", "mount", "-o", "remount,ro", "/").Run()
		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := exec.Command("sudo", "mv", tmpDst, u.cfg.BinaryPath).Run(); err != nil {
		exec.Command("sudo", "rm", "-f", tmpDst).Run()
		exec.Command("sudo", "mount", "-o", "remount,ro", "/").Run()
		return fmt.Errorf("rename binary: %w", err)
	}
	os.Remove(stagedBinary)

	// Verify the installed binary reports the expected version.
	verOut, err := exec.Command(u.cfg.BinaryPath, "-version").Output()
	if err != nil {
		exec.Command("sudo", "mount", "-o", "remount,ro", "/").Run()
		return fmt.Errorf("version check failed: %w", err)
	}
	gotVersion := strings.TrimSpace(string(verOut))
	if !strings.Contains(gotVersion, expectedVersion) {
		exec.Command("sudo", "mount", "-o", "remount,ro", "/").Run()
		return fmt.Errorf("version mismatch: installed binary reports %q, expected %q", gotVersion, expectedVersion)
	}

	// Restore read-only rootfs.
	if err := exec.Command("sudo", "mount", "-o", "remount,ro", "/").Run(); err != nil {
		log.Printf("updater: WARNING: failed to remount ro: %v", err)
	}

	log.Println("updater: Pi binary updated -- exiting for restart")
	os.Exit(0)
	return nil // unreachable
}

// ApplyFirmwareUpdate moves the ELF to the flash path and runs the flash script.
// Staging dir and firmware path are on the same filesystem (/data), so rename is atomic.
func (u *Updater) ApplyFirmwareUpdate(stagedELF string) error {
	if err := os.Rename(stagedELF, u.cfg.FirmwarePath); err != nil {
		return fmt.Errorf("rename staged firmware: %w", err)
	}

	// Run flash script in a new session so it survives digitsd being stopped.
	// SKIP_SERVICE_CONTROL=1 tells the script not to stop/start digitsd.
	cmd := exec.Command("setsid", "bash", u.cfg.FlashScript, u.cfg.FirmwarePath)
	cmd.Env = append(os.Environ(), "SKIP_SERVICE_CONTROL=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("flash script: %w", err)
	}

	log.Println("updater: firmware update applied successfully")
	return nil
}
