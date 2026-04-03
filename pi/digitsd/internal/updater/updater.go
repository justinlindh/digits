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
	"time"
)

type Manifest struct {
	PiVersion       string `json:"pi_version"`
	PiCommit        string `json:"pi_commit"`
	PiSHA256        string `json:"pi_sha256"`
	FirmwareVersion string `json:"firmware_version"`
	FirmwareCommit  string `json:"firmware_commit"`
	FirmwareSHA256  string `json:"firmware_sha256"`
}

type CheckResult struct {
	PiUpdateAvailable bool
	FWUpdateAvailable bool
	Manifest          *Manifest
	TargetPiURL       string // set by CheckVersion for targeted downloads
	TargetFWURL       string // set by CheckVersion for targeted downloads
}

type Config struct {
	ServerBaseURL    string
	CurrentPiVersion string
	CurrentFWVersion string
	StagingDir       string // default: /data/digits/staging
	FlashScript      string // default: /usr/local/bin/flash-pico.sh
	BinaryPath       string // default: /data/digits/digitsd/digitsd
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
		cfg.BinaryPath = "/data/digits/digitsd/digitsd"
	}
	if cfg.FirmwarePath == "" {
		cfg.FirmwarePath = "/data/digits/firmware.elf"
	}
	return &Updater{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Check queries the server for the latest version manifest.
func (u *Updater) Check() (*CheckResult, error) {
	resp, err := u.client.Get(u.cfg.ServerBaseURL + "/api/updates/latest")
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("check update: status %d", resp.StatusCode)
	}

	var m Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	return &CheckResult{
		PiUpdateAvailable: m.PiVersion != u.cfg.CurrentPiVersion,
		FWUpdateAvailable: m.FirmwareVersion != u.cfg.CurrentFWVersion,
		Manifest:          &m,
	}, nil
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

// CheckVersion queries the release index and builds a result for specific target versions.
// Empty target means "no update for that component".
func (u *Updater) CheckVersion(targetPi, targetFW string) (*CheckResult, error) {
	if targetPi == "" && targetFW == "" {
		return u.Check() // fall back to latest
	}

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

	result := &CheckResult{Manifest: &Manifest{}}

	if targetPi != "" && targetPi != u.cfg.CurrentPiVersion {
		rel, ok := idx.Pi.Releases[targetPi]
		if !ok {
			return nil, fmt.Errorf("pi version %s not found in release index", targetPi)
		}
		result.PiUpdateAvailable = true
		result.Manifest.PiVersion = rel.Version
		result.Manifest.PiCommit = rel.Commit
		result.Manifest.PiSHA256 = rel.SHA256
		result.TargetPiURL = rel.URL
	}

	if targetFW != "" && targetFW != u.cfg.CurrentFWVersion {
		rel, ok := idx.Firmware.Releases[targetFW]
		if !ok {
			return nil, fmt.Errorf("firmware version %s not found in release index", targetFW)
		}
		result.FWUpdateAvailable = true
		result.Manifest.FirmwareVersion = rel.Version
		result.Manifest.FirmwareCommit = rel.Commit
		result.Manifest.FirmwareSHA256 = rel.SHA256
		result.TargetFWURL = rel.URL
	}

	return result, nil
}

// DownloadFromURL downloads an artifact from a specific URL, verifies SHA256.
func (u *Updater) DownloadFromURL(url, localName, expectedSHA string) (string, error) {
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
	f.Close()

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

// DownloadArtifact downloads a named artifact to staging dir, verifies SHA256.
func (u *Updater) DownloadArtifact(name, expectedSHA string) (string, error) {
	if err := os.MkdirAll(u.cfg.StagingDir, 0755); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}
	destPath := filepath.Join(u.cfg.StagingDir, name)

	resp, err := u.client.Get(u.cfg.ServerBaseURL + "/api/updates/download/" + name)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: status %d", name, resp.StatusCode)
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
	f.Close()

	gotSHA := hex.EncodeToString(h.Sum(nil))
	if expectedSHA != "" && gotSHA != expectedSHA {
		os.Remove(destPath + ".tmp")
		return "", fmt.Errorf("sha256 mismatch: got %s, want %s", gotSHA, expectedSHA)
	}

	if err := os.Rename(destPath+".tmp", destPath); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}

	log.Printf("updater: downloaded %s (sha256=%s)", name, gotSHA)
	return destPath, nil
}

// ApplyPiUpdate replaces the digitsd binary and exits (systemd restarts).
// Staging dir and binary path are on the same filesystem (/data), so rename is atomic.
func (u *Updater) ApplyPiUpdate(stagedBinary string) error {
	if err := os.Chmod(stagedBinary, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(stagedBinary, u.cfg.BinaryPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	log.Println("updater: Pi binary updated — exiting for restart")
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
