package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStatusEndpoint(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "boot-counter")
	os.WriteFile(counterPath, []byte("3"), 0644)

	srv := &recoveryServer{
		counterPath: counterPath,
		hostname:    "digits-abc1",
	}

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	srv.handleStatus(w, req)

	var resp statusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.BootCount != 3 {
		t.Errorf("boot_count = %d, want 3", resp.BootCount)
	}
	if resp.Hostname != "digits-abc1" {
		t.Errorf("hostname = %q, want %q", resp.Hostname, "digits-abc1")
	}
}

func TestFactoryResetStatusEndpoint(t *testing.T) {
	srv := &recoveryServer{}

	// Initially not in progress
	req := httptest.NewRequest("GET", "/factory-reset/status", nil)
	w := httptest.NewRecorder()
	srv.handleFactoryResetStatus(w, req)

	var resp resetStatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.InProgress {
		t.Error("expected in_progress=false initially")
	}
	if resp.Status != "" {
		t.Errorf("expected empty status, got %q", resp.Status)
	}

	// After setting status
	srv.setResetStatus("Restoring rootfs...")
	req = httptest.NewRequest("GET", "/factory-reset/status", nil)
	w = httptest.NewRecorder()
	srv.handleFactoryResetStatus(w, req)

	var resp2 resetStatusResponse
	json.NewDecoder(w.Body).Decode(&resp2)
	if !resp2.InProgress {
		t.Error("expected in_progress=true after setResetStatus")
	}
	if resp2.Status != "Restoring rootfs..." {
		t.Errorf("status = %q, want %q", resp2.Status, "Restoring rootfs...")
	}
}

func TestTryAgainEndpoint(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "boot-counter")
	os.WriteFile(counterPath, []byte("3"), 0644)

	srv := &recoveryServer{
		counterPath: counterPath,
		rebootFunc:  func() error { return nil },
	}

	req := httptest.NewRequest("POST", "/try-again", nil)
	w := httptest.NewRecorder()
	srv.handleTryAgain(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	data, _ := os.ReadFile(counterPath)
	if string(data) != "0" {
		t.Errorf("counter = %q, want %q", data, "0")
	}
}
