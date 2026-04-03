package updater

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckUpdate_NewVersionAvailable(t *testing.T) {
	manifest := Manifest{
		PiVersion:       "1.1.0",
		FirmwareVersion: "1.1.0",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(manifest)
	}))
	defer srv.Close()

	u := New(Config{
		ServerBaseURL:    srv.URL,
		CurrentPiVersion: "1.0.0",
		CurrentFWVersion: "1.0.0",
	})

	result, err := u.Check()
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if !result.PiUpdateAvailable {
		t.Error("expected Pi update available")
	}
	if !result.FWUpdateAvailable {
		t.Error("expected FW update available")
	}
}

func TestCheckUpdate_AlreadyCurrent(t *testing.T) {
	manifest := Manifest{
		PiVersion:       "1.0.0",
		FirmwareVersion: "1.0.0",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(manifest)
	}))
	defer srv.Close()

	u := New(Config{
		ServerBaseURL:    srv.URL,
		CurrentPiVersion: "1.0.0",
		CurrentFWVersion: "1.0.0",
	})

	result, err := u.Check()
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if result.PiUpdateAvailable {
		t.Error("expected no Pi update")
	}
	if result.FWUpdateAvailable {
		t.Error("expected no FW update")
	}
}
