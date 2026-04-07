// Package config provides config file loading and saving for digitsd.
// The config file lives at /data/digits/config.json (writable data partition).
//
// Writes are crash-safe: write to .tmp, fsync, rename (atomic on Linux).
// A .bak copy is kept so corrupt configs can be recovered after power loss.
package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

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

	// path is the file the config was loaded from; used by Save.
	path string
}

// DefaultPath is the default config file location on a Pi.
const DefaultPath = "/data/digits/config.json"

// Load reads the config file at path. If the file does not exist a zero-value
// Config is returned with no error (caller applies CLI-flag defaults).
//
// If the primary file is corrupt (e.g. null bytes from a power cut), Load
// automatically falls back to the .bak file.
func Load(path string) (*Config, error) {
	c := &Config{path: path}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("config: %s not found, using defaults", path)
			return c, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	// Try parsing the primary config
	if err := json.Unmarshal(data, c); err != nil {
		log.Printf("config: %s is corrupt (%v), trying backup", path, err)
		return loadBackup(path)
	}

	// Sanity check: a zero-length or all-null file parses as empty JSON
	// but is likely corrupt. Check if file had actual content.
	if isCorrupt(data) {
		log.Printf("config: %s contains null bytes (power loss?), trying backup", path)
		return loadBackup(path)
	}

	log.Printf("config: loaded from %s (server_url=%q, phone_number=%q, has_token=%v, has_pairing_code=%v)",
		path, c.ServerURL, c.PhoneNumber, c.DeviceToken != "", c.PairingCode != "")
	return c, nil
}

// loadBackup tries to load from the .bak file. If that also fails, returns
// a zero-value config so the daemon can still boot (in pairing mode).
func loadBackup(path string) (*Config, error) {
	bakPath := path + ".bak"
	c := &Config{path: path}

	data, err := os.ReadFile(bakPath)
	if err != nil {
		log.Printf("config: backup %s also unavailable: %v — using defaults", bakPath, err)
		return c, nil
	}

	if isCorrupt(data) {
		log.Printf("config: backup %s also corrupt — using defaults", bakPath)
		return c, nil
	}

	if err := json.Unmarshal(data, c); err != nil {
		log.Printf("config: backup %s also unparseable: %v — using defaults", bakPath, err)
		return c, nil
	}

	log.Printf("config: RECOVERED from backup %s (server_url=%q, phone_number=%q, has_token=%v)",
		bakPath, c.ServerURL, c.PhoneNumber, c.DeviceToken != "")

	// Restore the primary file from the good backup
	if err := atomicWrite(path, data, 0600); err != nil {
		log.Printf("config: failed to restore primary from backup: %v", err)
	} else {
		log.Printf("config: restored primary %s from backup", path)
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
			log.Printf("config: backup write failed: %v", err)
		}
	}

	// Atomic write: tmp → fsync → rename
	if err := atomicWrite(c.path, data, 0600); err != nil {
		return fmt.Errorf("config: atomic write %s: %w", c.path, err)
	}

	log.Printf("config: saved to %s", c.path)
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
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}

	// fsync to flush to storage before rename
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync temp: %w", err)
	}

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
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
		d.Sync()
		d.Close()
	}

	return nil
}

// SetDeviceToken records a device token returned after pairing and clears the
// pairing code, then saves the config to disk.
func (c *Config) SetDeviceToken(token string) error {
	c.DeviceToken = token
	c.PairingCode = ""
	return c.Save()
}

// NeedsPairing returns true when a pairing code is present but no device
// token has been issued yet.
func (c *Config) NeedsPairing() bool {
	return c.PairingCode != "" && c.DeviceToken == ""
}

// IsConfigured returns true when the minimum required fields (server URL and
// either a device token or a pairing code) are present.
func (c *Config) IsConfigured() bool {
	return c.ServerURL != "" && (c.DeviceToken != "" || c.PairingCode != "")
}

// Path returns the file path this config was loaded from.
func (c *Config) Path() string {
	return c.path
}
