// Package config provides config file loading and saving for digitsd.
// The config file lives at /data/digits/config.json (writable data partition).
//
// Writes are crash-safe: write to .tmp, fsync, rename (atomic on Linux).
// A .bak copy is kept so corrupt configs can be recovered after power loss.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Voice style identifiers persisted in Config and on the wire.
const (
	VoiceStyleCopper = "copper"
	VoiceStyleModern = "modern"
)

// Voicemail constants that are no longer server-configurable.
const (
	VoicemailMaxStoredMessages = 50
)

// WiFiFallback configures the wifi auto-fallback supervisor.
type WiFiFallback struct {
	Enabled           bool          `json:"enabled"`
	GraceInitial      time.Duration `json:"grace_initial"`
	GraceMax          time.Duration `json:"grace_max"`
	APNoClientTimeout time.Duration `json:"ap_no_client_timeout"`
}

// Voicemail configures the answering-machine feature. Storage is phone-local;
// there is no server-side index. Retrieval is dial-in only, from the same
// phone the message was left at.
type Voicemail struct {
	// Enabled gates the entire feature. When false the FSM behaves as if
	// voicemail did not exist: no ring timeout, no retrieval code intercept,
	// no LED message-waiting indicator.
	Enabled bool `json:"enabled"`

	// RingTimeout is how long an inbound ring may go unanswered before
	// digitsd auto-answers and starts recording. Mirrors a 4-ring delay on
	// classic answering machines (~20s on 5Hz cadence).
	RingTimeout time.Duration `json:"ring_timeout"`
}

// Config holds digitsd runtime configuration loaded from a JSON file.
// CLI flags always override values loaded from the file.
type Config struct {
	// ServerURL is the WebSocket URL for the signaling server.
	// e.g. "wss://digits.family/ws"
	ServerURL string `json:"server_url,omitempty"`

	// PairingCode is a short alphanumeric code used for first-time device
	// pairing. Once pairing completes the server returns a DeviceToken and
	// this field is cleared.
	PairingCode string `json:"pairing_code,omitempty"`

	// PhoneNumber is the E.164-style number assigned to this device
	// (without country code, e.g. "3140001").
	PhoneNumber string `json:"phone_number,omitempty"`

	// DeviceToken is an opaque bearer token returned by the server after a
	// successful pairing exchange. If non-empty, pairing is complete.
	DeviceToken string `json:"device_token,omitempty"`

	// HookInverted inverts the hook switch sense on the Pico firmware.
	// Set to true for PCB carrier boards with on-board tactile switch
	// (LOW = off-hook). Leave false for protoboard builds with V-153-1C25
	// microswitch (HIGH = off-hook).
	HookInverted bool `json:"hook_inverted,omitempty"`

	// VoiceStyle is the per-line audio character ("copper" or "modern").
	// Set by the server on registration and on each web-UI change, cached
	// locally so the device can start up with the correct style offline.
	VoiceStyle string `json:"voice_style,omitempty"`

	// SilentMode is the per-line ringer-silence flag. When true, the digitsd
	// phone controller suppresses the bell on incoming rings but still blinks
	// the LED. Cached locally so the setting survives reboots while offline.
	SilentMode bool `json:"silent_mode,omitempty"`

	// AutoUpdate controls whether digitsd applies OTA updates automatically.
	// When true, the daemon downloads and applies available updates without
	// waiting for a manual update_trigger from the server. Cached locally so
	// the setting survives reboots while offline.
	AutoUpdate bool `json:"auto_update,omitempty"`

	// WiFiFallback configures the WiFi auto-fallback supervisor.
	WiFiFallback WiFiFallback `json:"wifi_fallback"`

	// Voicemail configures the answering-machine feature. Enabled by default.
	Voicemail Voicemail `json:"voicemail"`

	// path is the file the config was loaded from; used by Save.
	path string
}

// DefaultPath is the default config file location on a Pi.
const DefaultPath = "/data/digits/config.json"

// Default returns a Config populated with the daemon's built-in defaults. It
// is the single source of truth for those defaults: Load and loadBackup both
// start from Default() and overlay the on-disk JSON, so changing a default
// here changes it everywhere.
func Default() *Config {
	return &Config{
		WiFiFallback: WiFiFallback{
			Enabled:           true,
			GraceInitial:      5 * time.Minute,
			GraceMax:          30 * time.Minute,
			APNoClientTimeout: 10 * time.Minute,
		},
		Voicemail: Voicemail{
			Enabled:     true,
			RingTimeout: 20 * time.Second,
		},
	}
}

// Load reads the config file at path. If the file does not exist a Config with
// built-in defaults is returned with no error (caller applies CLI-flag
// defaults).
//
// If the primary file is corrupt (e.g. null bytes from a power cut), Load
// automatically falls back to the .bak file.
func Load(path string) (*Config, error) {
	c := Default()
	c.path = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("config: file not found, using defaults", "path", path)
			return c, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	// Try parsing the primary config
	if err := json.Unmarshal(data, c); err != nil {
		slog.Warn("config: file is corrupt, trying backup", "path", path, "error", err)
		return loadBackup(path)
	}

	// Sanity check: a zero-length or all-null file parses as empty JSON
	// but is likely corrupt. Check if file had actual content.
	if isCorrupt(data) {
		slog.Warn("config: file contains null bytes (power loss?), trying backup", "path", path)
		return loadBackup(path)
	}

	slog.Info("config: loaded", "path", path, "server_url", c.ServerURL, "phone_number", c.PhoneNumber, "has_token", c.DeviceToken != "", "has_pairing_code", c.PairingCode != "")
	return c, nil
}

// loadBackup tries to load from the .bak file. If that also fails, returns
// a Config with built-in defaults so the daemon can still boot (in pairing
// mode).
func loadBackup(path string) (*Config, error) {
	bakPath := path + ".bak"
	c := Default()
	c.path = path

	data, err := os.ReadFile(bakPath)
	if err != nil {
		slog.Warn("config: backup also unavailable, using defaults", "path", bakPath, "error", err)
		return c, nil
	}

	if isCorrupt(data) {
		slog.Warn("config: backup also corrupt, using defaults", "path", bakPath)
		return c, nil
	}

	if err := json.Unmarshal(data, c); err != nil {
		slog.Warn("config: backup also unparseable, using defaults", "path", bakPath, "error", err)
		return c, nil
	}

	slog.Info("config: recovered from backup", "path", bakPath, "server_url", c.ServerURL, "phone_number", c.PhoneNumber, "has_token", c.DeviceToken != "")

	// Restore the primary file from the good backup
	if err := atomicWrite(path, data, 0600); err != nil {
		slog.Warn("config: failed to restore primary from backup", "error", err)
	} else {
		slog.Info("config: restored primary from backup", "path", path)
	}

	return c, nil
}

// isCorrupt checks if file data is likely corrupt (all zeros, or contains
// null bytes which should never appear in valid JSON).
func isCorrupt(data []byte) bool {
	if len(data) == 0 {
		return false // empty file is valid (no config)
	}
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

// VoiceStyleOrDefault returns the configured voice style, falling back to
// VoiceStyleCopper when the field is empty. Kept separate from the raw field
// so the empty-string case (never set, or explicitly cleared) doesn't have
// to be special-cased at every call site.
func (c *Config) VoiceStyleOrDefault() string {
	if c == nil || c.VoiceStyle == "" {
		return VoiceStyleCopper
	}
	return c.VoiceStyle
}

// Save writes the config back to the file it was loaded from.
// Uses atomic write (tmp + fsync + rename) and keeps a .bak copy.
func (c *Config) Save() error {
	if c.path == "" {
		return fmt.Errorf("config: no path set, cannot save")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0755); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", filepath.Dir(c.path), err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	// Copy current file to .bak before overwriting (best-effort)
	if existing, err := os.ReadFile(c.path); err == nil && !isCorrupt(existing) {
		bakPath := c.path + ".bak"
		if err := atomicWrite(bakPath, existing, 0600); err != nil {
			slog.Error("config: backup write failed", "error", err)
		}
	}

	// Atomic write: tmp → fsync → rename
	if err := atomicWrite(c.path, data, 0600); err != nil {
		return fmt.Errorf("config: atomic write %s: %w", c.path, err)
	}

	slog.Info("config: saved", "path", c.path)
	return nil
}

// atomicWrite writes data to path using a temp file + fsync + rename pattern.
// This ensures the file is either fully written or not changed at all.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	// Clean up temp file on any error
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("write temp: %w", err)
	}

	// fsync to flush to storage before rename
	if err := tmp.Sync(); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("fsync temp: %w", err)
	}

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("chmod temp: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	tmpPath = "" // prevent deferred cleanup

	// fsync the directory to ensure the rename is durable
	d, err := os.Open(dir)
	if err == nil {
		if syncErr := d.Sync(); syncErr != nil {
			slog.Warn("config: dir fsync", "dir", dir, "error", syncErr)
		}
		_ = d.Close()
	}

	return nil
}

// Path returns the file path this config was loaded from.
func (c *Config) Path() string {
	return c.path
}
