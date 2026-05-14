package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justinlindh/digits/pi/digitsd/internal/devmode"
)

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
	_ = devmode.Enable(cfg.FlagPath)

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
	if !devmode.Enabled(cfg.FlagPath) {
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
	if devmode.Enabled(cfg.FlagPath) {
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
	if !devmode.SkipFWReflash(cfg.SkipFWReflashPath) {
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
	if !devmode.SkipAutoUpdate(cfg.SkipAutoUpdatePath) {
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
