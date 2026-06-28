package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justinlindh/digits/pi/digitsd/internal/devmode"
)

// newTestListener returns a real, closeable loopback listener for manager tests
// so they never bind the fixed :8080 dev-UI port.
func newTestListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func TestDevModeManager_EnablePassesPasswordAndStartsListener(t *testing.T) {
	var gotEnable bool
	var gotPassword string
	startCalls := 0
	m := &devModeManager{
		cfg: &devModeConfig{},
		apply: func(enable bool, password string) error {
			gotEnable = enable
			gotPassword = password
			return nil
		},
		start: func(*devModeConfig) (net.Listener, error) {
			startCalls++
			return newTestListener(t), nil
		},
	}

	if err := m.Enable("hunter2pw"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !gotEnable {
		t.Error("apply called with enable=false, want true")
	}
	if gotPassword != "hunter2pw" {
		t.Errorf("password = %q, want hunter2pw", gotPassword)
	}
	if startCalls != 1 {
		t.Errorf("start calls = %d, want 1", startCalls)
	}

	// Re-enabling does not start a second listener.
	if err := m.Enable("hunter2pw"); err != nil {
		t.Fatalf("Enable (2nd): %v", err)
	}
	if startCalls != 1 {
		t.Errorf("start calls after re-enable = %d, want 1", startCalls)
	}
	m.Close()
}

func TestDevModeManager_DisableStopsListener(t *testing.T) {
	var gotEnable = true
	m := &devModeManager{
		cfg:   &devModeConfig{},
		apply: func(enable bool, _ string) error { gotEnable = enable; return nil },
		start: func(*devModeConfig) (net.Listener, error) { return newTestListener(t), nil },
	}
	if err := m.EnsureListener(); err != nil {
		t.Fatalf("EnsureListener: %v", err)
	}
	if m.ln == nil {
		t.Fatal("listener not started by EnsureListener")
	}
	if err := m.Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if gotEnable {
		t.Error("apply called with enable=true on Disable, want false")
	}
	if m.ln != nil {
		t.Error("listener not stopped by Disable")
	}
}

func TestDevModeManager_ApplyErrorLeavesListenerUnchanged(t *testing.T) {
	startCalls := 0
	m := &devModeManager{
		cfg:   &devModeConfig{},
		apply: func(bool, string) error { return errors.New("helper boom") },
		start: func(*devModeConfig) (net.Listener, error) { startCalls++; return newTestListener(t), nil },
	}
	if err := m.Enable("pw12345678"); err == nil {
		t.Fatal("Enable: want error, got nil")
	}
	if startCalls != 0 {
		t.Errorf("start calls = %d, want 0 (apply failed)", startCalls)
	}
	if m.ln != nil {
		t.Error("listener started despite apply failure")
	}
}

func TestDevModeStatusHandler(t *testing.T) {
	dir := t.TempDir()
	cfg := &devModeConfig{
		FlagPath:           filepath.Join(dir, "dev-mode"),
		SkipFWReflashPath:  filepath.Join(dir, "skip-fw-reflash"),
		SkipAutoUpdatePath: filepath.Join(dir, "skip-auto-update"),
		StatusFunc: func() devModeStatus {
			return devModeStatus{
				DigitsdVersion:   "1.2.3",
				FirmwareVersion:  "1.5.0",
				FirmwareCommit:   "abc1234",
				Phase:            "0x01",
				Online:           true,
				PhoneNumber:      "3140001",
				ConfigAutoUpdate: true,
			}
		},
	}
	_ = devmode.Set(cfg.FlagPath, true)

	h := devModeStatusHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	h(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["digitsd_version"] != "1.2.3" {
		t.Errorf("digitsd_version = %v, want 1.2.3", body["digitsd_version"])
	}
	if body["dev_mode"] != true {
		t.Errorf("dev_mode = %v, want true", body["dev_mode"])
	}
	if body["firmware_version"] != "1.5.0" {
		t.Errorf("firmware_version = %v, want 1.5.0", body["firmware_version"])
	}
	if body["online"] != true {
		t.Errorf("online = %v, want true", body["online"])
	}
}

func TestDevModeStatusHandler_NilStatusFunc(t *testing.T) {
	dir := t.TempDir()
	cfg := &devModeConfig{
		FlagPath:           filepath.Join(dir, "dev-mode"),
		SkipFWReflashPath:  filepath.Join(dir, "skip-fw-reflash"),
		SkipAutoUpdatePath: filepath.Join(dir, "skip-auto-update"),
	}

	h := devModeStatusHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	h(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
}

func TestDevModeToggleHandler_DevMode(t *testing.T) {
	dir := t.TempDir()
	cfg := &devModeConfig{
		FlagPath:           filepath.Join(dir, "dev-mode"),
		SkipFWReflashPath:  filepath.Join(dir, "skip-fw-reflash"),
		SkipAutoUpdatePath: filepath.Join(dir, "skip-auto-update"),
	}

	h := devModeToggleHandler(cfg)

	// Enable dev mode.
	body := `{"name":"devmode","enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/toggle", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("enable: status code = %d, want 200", w.Code)
	}
	if !devmode.IsSet(cfg.FlagPath) {
		t.Error("dev-mode flag not created")
	}

	// Disable dev mode.
	body = `{"name":"devmode","enabled":false}`
	req = httptest.NewRequest(http.MethodPost, "/api/toggle", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("disable: status code = %d, want 200", w.Code)
	}
	if devmode.IsSet(cfg.FlagPath) {
		t.Error("dev-mode flag not removed")
	}
}

func TestDevModeToggleHandler_FWReflash(t *testing.T) {
	dir := t.TempDir()
	cfg := &devModeConfig{
		FlagPath:           filepath.Join(dir, "dev-mode"),
		SkipFWReflashPath:  filepath.Join(dir, "skip-fw-reflash"),
		SkipAutoUpdatePath: filepath.Join(dir, "skip-auto-update"),
	}

	h := devModeToggleHandler(cfg)
	body := `{"name":"fw_reflash","enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/toggle", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	if !devmode.IsSet(cfg.SkipFWReflashPath) {
		t.Error("skip-fw-reflash flag not created")
	}
}

func TestDevModeToggleHandler_AutoUpdate(t *testing.T) {
	dir := t.TempDir()
	cfg := &devModeConfig{
		FlagPath:           filepath.Join(dir, "dev-mode"),
		SkipFWReflashPath:  filepath.Join(dir, "skip-fw-reflash"),
		SkipAutoUpdatePath: filepath.Join(dir, "skip-auto-update"),
	}

	h := devModeToggleHandler(cfg)
	body := `{"name":"auto_update","enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/toggle", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	if !devmode.IsSet(cfg.SkipAutoUpdatePath) {
		t.Error("skip-auto-update flag not created")
	}
}

func TestDevModeToggleHandler_BadMethod(t *testing.T) {
	cfg := &devModeConfig{}
	h := devModeToggleHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/toggle", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want 405", w.Code)
	}
}

func TestDevModeToggleHandler_UnknownToggle(t *testing.T) {
	dir := t.TempDir()
	cfg := &devModeConfig{
		FlagPath:           filepath.Join(dir, "dev-mode"),
		SkipFWReflashPath:  filepath.Join(dir, "skip-fw-reflash"),
		SkipAutoUpdatePath: filepath.Join(dir, "skip-auto-update"),
	}
	h := devModeToggleHandler(cfg)
	body := `{"name":"bogus","enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/toggle", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want 400", w.Code)
	}
}

func TestDevModeFlashHandler_NoFlashFunc(t *testing.T) {
	cfg := &devModeConfig{}
	h := devModeFlashHandler(cfg)
	req := httptest.NewRequest(http.MethodPost, "/api/flash", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want 503", w.Code)
	}
}

func TestDevModeFlashHandler_BadMethod(t *testing.T) {
	cfg := &devModeConfig{}
	h := devModeFlashHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/flash", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want 405", w.Code)
	}
}

func TestDevModeSerialLogHandler_NoPath(t *testing.T) {
	cfg := &devModeConfig{}
	h := devModeSerialLogHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/log/serial", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no UART log") {
		t.Errorf("body = %q, want mention of no UART log", w.Body.String())
	}
}

func TestDevModeSerialLogHandler_WithFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "uart.log")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &devModeConfig{UARTLogPath: logPath}
	h := devModeSerialLogHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/log/serial", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if w.Body.String() != content {
		t.Errorf("body = %q, want %q", w.Body.String(), content)
	}
}
