// Package updater checks for and applies Pi+Pico software updates.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// dialTimeout bounds TCP connection establishment.
	dialTimeout = 15 * time.Second
	// responseHeaderTimeout bounds how long we wait for the server to send
	// response headers after the request is written. This catches a server
	// that accepts the connection then stalls before replying, the classic
	// half-open hang, without capping the time spent streaming a large body.
	responseHeaderTimeout = 30 * time.Second
	// downloadTimeout is the absolute deadline for a single download attempt,
	// finite so a connection that goes silent mid-body cannot latch update
	// state forever. An attempt that dies mid-body keeps its partial .tmp and
	// the next attempt resumes it with a Range request, so a link too slow to
	// finish inside one deadline still converges across attempts.
	downloadTimeout = 10 * time.Minute
)

// newHTTPClient builds the shared client. It sets connection-setup and
// response-header timeouts on the transport rather than a single
// Client.Timeout, so legitimate large downloads are not killed by an absolute
// cap while half-open connections still fail fast. Per-request deadlines are
// supplied via context (see CheckVersion and Download).
func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   dialTimeout,
			ResponseHeaderTimeout: responseHeaderTimeout,
			ExpectContinueTimeout: 1 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

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
	// downloadTimeout bounds a single Download. Defaults to the package
	// downloadTimeout const; overridable in tests.
	downloadTimeout time.Duration
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
			cfg.BinaryPath = "/usr/local/bin/digitsd"
		} else {
			cfg.BinaryPath = exe
		}
	}
	slog.Info("updater: configured", "binary_path", cfg.BinaryPath)
	if cfg.FirmwarePath == "" {
		cfg.FirmwarePath = "/data/digits/firmware.elf"
	}
	return &Updater{
		cfg:             cfg,
		client:          newHTTPClient(),
		downloadTimeout: downloadTimeout,
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.cfg.ServerBaseURL+"/api/updates/releases", nil)
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch releases: status %d", resp.StatusCode)
	}

	var idx ReleaseIndex
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}

	result := &CheckResult{}

	piRel, err := resolveTargetRelease("pi", targetPi, u.cfg.CurrentPiVersion, idx.Pi)
	if err != nil {
		return nil, err
	}
	if piRel != nil {
		result.PiAvailable = true
		result.PiVersion = piRel.Version
		result.PiSHA256 = piRel.SHA256
		result.PiURL = piRel.URL
	}

	fwRel, err := resolveTargetRelease("firmware", targetFW, u.cfg.CurrentFWVersion, idx.Firmware)
	if err != nil {
		return nil, err
	}
	if fwRel != nil {
		result.FWAvailable = true
		result.FWVersion = fwRel.Version
		result.FWSHA256 = fwRel.SHA256
		result.FWURL = fwRel.URL
	}

	return result, nil
}

// resolveTargetRelease picks the release a component should move to. An empty
// target means "use the component's Latest". Returns (nil, nil) when no update
// is needed (no target known, or the resolved target equals current). Returns
// an error only when an explicit target is set but missing from the index.
func resolveTargetRelease(label, target, current string, comp ComponentIndex) (*Release, error) {
	if target == "" {
		target = comp.Latest
	}
	if target == "" || target == current {
		return nil, nil
	}
	rel, ok := comp.Releases[target]
	if !ok {
		return nil, fmt.Errorf("%s version %s not found in release index", label, target)
	}
	return rel, nil
}

// Download downloads an artifact from a URL, verifies SHA256, and writes it
// atomically to the staging directory.
func (u *Updater) Download(url, localName, expectedSHA string) (string, error) {
	if err := os.MkdirAll(u.cfg.StagingDir, 0755); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}
	destPath := filepath.Join(u.cfg.StagingDir, localName)
	tmpPath := destPath + ".tmp"
	// The marker records which artifact the partial belongs to, so a new
	// release under the same localName restarts from zero instead of gluing
	// bytes from two different binaries together.
	markerPath := tmpPath + ".sha"

	// Resume a partial from a prior attempt when it provably belongs to the
	// same artifact. Seed the hash from the bytes already on disk so the final
	// digest covers the whole file.
	var offset int64
	h := sha256.New()
	if expectedSHA != "" {
		if marker, err := os.ReadFile(markerPath); err == nil && string(marker) == expectedSHA {
			if prev, err := os.Open(tmpPath); err == nil {
				n, cerr := io.Copy(h, prev)
				_ = prev.Close()
				if cerr == nil {
					offset = n
				}
			}
		}
	}
	if offset == 0 {
		h = sha256.New()
		_ = os.Remove(tmpPath)
		_ = os.Remove(markerPath)
	}

	// Bound the attempt so a connection that stalls mid-body cannot block
	// io.Copy forever and latch update state. The deadline covers the body
	// stream too: cancelling the context unblocks an in-flight Read.
	ctx, cancel := context.WithTimeout(context.Background(), u.downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case offset > 0 && resp.StatusCode == http.StatusPartialContent:
		// Server honored the resume; append below.
	case resp.StatusCode == http.StatusOK:
		// Fresh download, or the server ignored the Range header and sent the
		// full body. Either way start over from byte zero.
		offset = 0
		h = sha256.New()
	default:
		if offset > 0 {
			// Most likely 416 from a partial the server can no longer satisfy
			// (e.g. a complete-but-unrenamed leftover). Drop it so the next
			// attempt starts clean instead of erroring forever.
			_ = os.Remove(tmpPath)
			_ = os.Remove(markerPath)
		}
		return "", fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}

	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if offset > 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(tmpPath, flags, 0644)
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	if expectedSHA != "" && offset == 0 {
		if err := os.WriteFile(markerPath, []byte(expectedSHA), 0644); err != nil {
			_ = f.Close()
			return "", fmt.Errorf("write resume marker: %w", err)
		}
	}

	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		// Keep the partial and its marker: the next attempt resumes here.
		_ = f.Close()
		return "", fmt.Errorf("download write: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("flush download: %w", err)
	}

	gotSHA := hex.EncodeToString(h.Sum(nil))
	if expectedSHA != "" && gotSHA != expectedSHA {
		_ = os.Remove(tmpPath)
		_ = os.Remove(markerPath)
		return "", fmt.Errorf("sha256 mismatch: got %s, want %s", gotSHA, expectedSHA)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}
	_ = os.Remove(markerPath)

	slog.Info("updater: downloaded", "file", localName, "url", url, "sha256", gotSHA)
	return destPath, nil
}

// ApplyPiUpdate replaces the digitsd binary on the read-only rootfs and exits
// (systemd restarts). Temporarily remounts / as rw for the copy, then restores ro.
func (u *Updater) ApplyPiUpdate(stagedBinary, expectedVersion string) error {
	if err := os.Chmod(stagedBinary, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// Remount rootfs read-write so we can replace the binary.
	if err := exec.Command("sudo", "mount", "-o", "remount,rw", "/").Run(); err != nil {
		return fmt.Errorf("remount rw: %w", err)
	}

	tmpDst := u.cfg.BinaryPath + ".tmp"

	// remountRO restores the read-only rootfs on the error paths below; a
	// failure to remount is logged, never fatal. Every early return past the
	// remount,rw must call this so the rootfs is not left writable.
	remountRO := func(when string) {
		if err := exec.Command("sudo", "mount", "-o", "remount,ro", "/").Run(); err != nil {
			slog.Warn("updater: failed to remount ro "+when, "error", err)
		}
	}
	// removeTmp discards a partial tmp binary; failures are logged, not fatal.
	removeTmp := func(when string) {
		if err := exec.Command("sudo", "rm", "-f", tmpDst).Run(); err != nil {
			slog.Warn("updater: failed to remove tmp binary "+when, "error", err)
		}
	}

	// Copy staged binary via sudo using tmp+mv to avoid "text file busy" on the
	// running executable. Direct cp fails because the kernel won't let you
	// overwrite an open binary.
	if err := exec.Command("sudo", "cp", stagedBinary, tmpDst).Run(); err != nil {
		remountRO("after copy failure")
		return fmt.Errorf("copy binary: %w", err)
	}
	if err := exec.Command("sudo", "chmod", "0755", tmpDst).Run(); err != nil {
		removeTmp("after chmod failure")
		remountRO("after chmod failure")
		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := exec.Command("sudo", "mv", tmpDst, u.cfg.BinaryPath).Run(); err != nil {
		removeTmp("after rename failure")
		remountRO("after rename failure")
		return fmt.Errorf("rename binary: %w", err)
	}
	_ = os.Remove(stagedBinary)

	// Verify the installed binary reports the expected version.
	if expectedVersion != "" {
		out, err := exec.Command(u.cfg.BinaryPath, "-version").CombinedOutput()
		if err != nil {
			slog.Warn("updater: version check failed", "error", err)
		} else if !strings.Contains(string(out), expectedVersion) {
			slog.Warn("updater: installed binary version mismatch", "got", strings.TrimSpace(string(out)), "expected", expectedVersion)
		} else {
			slog.Info("updater: verified installed version", "version", strings.TrimSpace(string(out)))
		}
	}

	// Restore read-only rootfs. This remount may fail: the running process has
	// the old binary's inode mmap'd (link count 0 after the mv), and the
	// kernel rejects remount,ro while a deleted inode is still open. That is
	// expected here -- os.Exit(0) follows immediately, which releases the
	// mmap. Systemd then starts the new binary, whose Extract() call will
	// remount ro once it is safely running from the replacement inode.
	if err := exec.Command("sudo", "mount", "-o", "remount,ro", "/").Run(); err != nil {
		slog.Warn("updater: remount ro failed (expected when replacing running binary; next startup will fix)", "error", err)
	}

	slog.Info("updater: Pi binary updated -- exiting for restart")
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

	slog.Info("updater: firmware update applied successfully")
	return nil
}
