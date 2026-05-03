package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFile(t *testing.T) {
	c, err := Load("/tmp/digits-test-nonexistent-config-99999.json")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil config")
	}
	if c.ServerURL != "" || c.PhoneNumber != "" || c.DeviceToken != "" || c.PairingCode != "" {
		t.Errorf("expected zero-value config for missing file, got: %+v", c)
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data := `{
		"server_url": "wss://digits.family/ws",
		"pairing_code": "A7X9",
		"phone_number": "3140001",
		"device_token": "tok-abc123"
	}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.ServerURL != "wss://digits.family/ws" {
		t.Errorf("ServerURL = %q, want %q", c.ServerURL, "wss://digits.family/ws")
	}
	if c.PairingCode != "A7X9" {
		t.Errorf("PairingCode = %q, want %q", c.PairingCode, "A7X9")
	}
	if c.PhoneNumber != "3140001" {
		t.Errorf("PhoneNumber = %q, want %q", c.PhoneNumber, "3140001")
	}
	if c.DeviceToken != "tok-abc123" {
		t.Errorf("DeviceToken = %q, want %q", c.DeviceToken, "tok-abc123")
	}
}

func TestLoadBadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte("{bad json"), 0644); err != nil {
		t.Fatal(err)
	}

	// Bad JSON now falls back to backup/defaults instead of erroring
	c, err := Load(path)
	if err != nil {
		t.Fatalf("should not error (falls back to defaults): %v", err)
	}
	if c.ServerURL != "" {
		t.Error("should return zero-value config on corrupt primary with no backup")
	}
}

func TestLoadCorruptFallsBackToBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	bakPath := path + ".bak"

	// Write corrupt primary (null bytes, simulating power loss)
	if err := os.WriteFile(path, make([]byte, 100), 0644); err != nil {
		t.Fatal(err)
	}

	// Write valid backup
	backup := `{"server_url": "wss://test/ws", "phone_number": "1234", "device_token": "tok-saved"}`
	if err := os.WriteFile(bakPath, []byte(backup), 0644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("should recover from backup: %v", err)
	}
	if c.DeviceToken != "tok-saved" {
		t.Errorf("expected token from backup, got %q", c.DeviceToken)
	}
	if c.PhoneNumber != "1234" {
		t.Errorf("expected phone from backup, got %q", c.PhoneNumber)
	}

	// Primary should be restored from backup
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("primary should be restored: %v", err)
	}
	var restored Config
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("restored primary should be valid JSON: %v", err)
	}
	if restored.DeviceToken != "tok-saved" {
		t.Errorf("restored primary token = %q, want tok-saved", restored.DeviceToken)
	}
}

func TestSaveCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	bakPath := path + ".bak"

	// Write initial config
	c := &Config{
		ServerURL:   "wss://test/ws",
		PhoneNumber: "1000",
		DeviceToken: "tok-v1",
		path:        path,
	}
	if err := c.Save(); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Save again with updated token
	c.DeviceToken = "tok-v2"
	if err := c.Save(); err != nil {
		t.Fatalf("second save: %v", err)
	}

	// Backup should have the v1 token
	data, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup should exist: %v", err)
	}
	var bak Config
	if err := json.Unmarshal(data, &bak); err != nil {
		t.Fatalf("backup should be valid JSON: %v", err)
	}
	if bak.DeviceToken != "tok-v1" {
		t.Errorf("backup token = %q, want tok-v1", bak.DeviceToken)
	}

	// Primary should have v2
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var primary Config
	if err := json.Unmarshal(data, &primary); err != nil {
		t.Fatal(err)
	}
	if primary.DeviceToken != "tok-v2" {
		t.Errorf("primary token = %q, want tok-v2", primary.DeviceToken)
	}
}

func TestIsCorrupt(t *testing.T) {
	if isCorrupt([]byte("{}")) {
		t.Error("valid JSON should not be corrupt")
	}
	if !isCorrupt(make([]byte, 4)) {
		t.Error("all nulls should be corrupt")
	}
	nullInJSON := []byte{'{', 0, '}'}
	if !isCorrupt(nullInJSON) {
		t.Error("embedded null should be corrupt")
	}
	if isCorrupt([]byte{}) {
		t.Error("empty should not be corrupt")
	}
}

func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "config.json")

	c := &Config{
		ServerURL:   "wss://test.local/ws",
		PhoneNumber: "5550001",
		PairingCode: "TEST",
		path:        path,
	}

	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c2, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if c2.ServerURL != c.ServerURL {
		t.Errorf("ServerURL: got %q, want %q", c2.ServerURL, c.ServerURL)
	}
	if c2.PairingCode != c.PairingCode {
		t.Errorf("PairingCode: got %q, want %q", c2.PairingCode, c.PairingCode)
	}
}

func TestHookInvertedDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Existing config without hook_inverted field defaults to false
	data := `{"server_url": "wss://digits.family/ws", "device_token": "tok-abc"}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.HookInverted {
		t.Error("HookInverted should default to false when not in config")
	}
}

func TestHookInvertedTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data := `{"server_url": "wss://digits.family/ws", "device_token": "tok-abc", "hook_inverted": true}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c.HookInverted {
		t.Error("HookInverted should be true when set in config")
	}
}

func TestCLIOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data := `{"server_url": "wss://config.example.com/ws", "phone_number": "3140001"}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	cliServerURL := "wss://override.example.com/ws"
	cliNumber := "9990001"

	effectiveURL := c.ServerURL
	effectiveNumber := c.PhoneNumber

	if cliServerURL != "" {
		effectiveURL = cliServerURL
	}
	if cliNumber != "" {
		effectiveNumber = cliNumber
	}

	if effectiveURL != "wss://override.example.com/ws" {
		t.Errorf("CLI override for server_url failed: got %q", effectiveURL)
	}
	if effectiveNumber != "9990001" {
		t.Errorf("CLI override for phone_number failed: got %q", effectiveNumber)
	}
}

func TestConfigVoiceStyleDefaultCopper(t *testing.T) {
	c := &Config{}
	if got := c.VoiceStyleOrDefault(); got != "copper" {
		t.Errorf("empty config: got %q, want %q", got, "copper")
	}
}

func TestConfigVoiceStyleCustomPreserved(t *testing.T) {
	c := &Config{VoiceStyle: "modern"}
	if got := c.VoiceStyleOrDefault(); got != "modern" {
		t.Errorf("custom value: got %q, want %q", got, "modern")
	}
}

func TestConfigLoadPreservesVoiceStyle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"voice_style":"modern"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.VoiceStyle != "modern" {
		t.Errorf("loaded voice_style: got %q, want %q", c.VoiceStyle, "modern")
	}
}

func TestWiFiFallbackDefaults(t *testing.T) {
	c := Default()
	if !c.WiFiFallback.Enabled {
		t.Error("WiFiFallback.Enabled should default to true")
	}
	if c.WiFiFallback.GraceInitial != 5*time.Minute {
		t.Errorf("GraceInitial = %v, want 5m", c.WiFiFallback.GraceInitial)
	}
	if c.WiFiFallback.GraceMax != 30*time.Minute {
		t.Errorf("GraceMax = %v, want 30m", c.WiFiFallback.GraceMax)
	}
	if c.WiFiFallback.APNoClientTimeout != 10*time.Minute {
		t.Errorf("APNoClientTimeout = %v, want 10m", c.WiFiFallback.APNoClientTimeout)
	}
}

func TestLoadPreservesExplicitEnabledFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := []byte(`{"wifi_fallback": {"enabled": false, "grace_initial": 120000000000}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.WiFiFallback.Enabled {
		t.Error("explicit enabled:false was not preserved")
	}
	if c.WiFiFallback.GraceInitial != 2*time.Minute {
		t.Errorf("GraceInitial = %v, want 2m", c.WiFiFallback.GraceInitial)
	}
	// Fields omitted from JSON should have default values.
	if c.WiFiFallback.GraceMax != 30*time.Minute {
		t.Errorf("GraceMax = %v, want 30m (default)", c.WiFiFallback.GraceMax)
	}
	if c.WiFiFallback.APNoClientTimeout != 10*time.Minute {
		t.Errorf("APNoClientTimeout = %v, want 10m (default)", c.WiFiFallback.APNoClientTimeout)
	}
}

func TestLoadWithoutWiFiFallbackSectionUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// Minimal config with no wifi_fallback section at all.
	data := []byte(`{}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !c.WiFiFallback.Enabled {
		t.Error("Enabled should default to true when wifi_fallback section is absent")
	}
	if c.WiFiFallback.GraceInitial != 5*time.Minute {
		t.Errorf("GraceInitial = %v, want 5m", c.WiFiFallback.GraceInitial)
	}
	if c.WiFiFallback.GraceMax != 30*time.Minute {
		t.Errorf("GraceMax = %v, want 30m", c.WiFiFallback.GraceMax)
	}
	if c.WiFiFallback.APNoClientTimeout != 10*time.Minute {
		t.Errorf("APNoClientTimeout = %v, want 10m", c.WiFiFallback.APNoClientTimeout)
	}
}

func TestConfigDefaultSilentModeFalse(t *testing.T) {
	if Default().SilentMode {
		t.Errorf("Default().SilentMode: got true, want false")
	}
}

func TestConfigLoadPreservesSilentMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"silent_mode":true}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !c.SilentMode {
		t.Errorf("load did not preserve silent_mode=true")
	}
}

func TestConfigSaveRoundTripSilentMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := &Config{path: path, SilentMode: true, VoiceStyle: VoiceStyleCopper}
	if err := c.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !loaded.SilentMode {
		t.Errorf("round trip did not preserve silent_mode")
	}
}

func TestAutoUpdateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data := `{"auto_update": true}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.AutoUpdate {
		t.Error("AutoUpdate: got false after load, want true")
	}

	if err := loaded.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !reloaded.AutoUpdate {
		t.Error("AutoUpdate: got false after save round-trip, want true")
	}
}
