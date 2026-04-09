package updater

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(idx ReleaseIndex) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/updates/releases" {
			if err := json.NewEncoder(w).Encode(idx); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		http.NotFound(w, r)
	}))
}

func TestCheckVersion_TargetedUpdate(t *testing.T) {
	idx := ReleaseIndex{
		Pi: ComponentIndex{
			Latest: "1.1.0",
			Releases: map[string]*Release{
				"1.0.0": {Version: "1.0.0", URL: "https://example.com/pi/1.0.0", SHA256: "aaa"},
				"1.1.0": {Version: "1.1.0", URL: "https://example.com/pi/1.1.0", SHA256: "bbb"},
			},
		},
		Firmware: ComponentIndex{
			Latest: "0.5.0",
			Releases: map[string]*Release{
				"0.5.0": {Version: "0.5.0", URL: "https://example.com/fw/0.5.0", SHA256: "ccc"},
			},
		},
	}
	srv := newTestServer(idx)
	defer srv.Close()

	u := New(Config{
		ServerBaseURL:    srv.URL,
		CurrentPiVersion: "1.0.0",
		CurrentFWVersion: "0.5.0",
	})

	result, err := u.CheckVersion("1.1.0", "")
	if err != nil {
		t.Fatalf("CheckVersion() error: %v", err)
	}
	if !result.PiAvailable {
		t.Error("expected Pi update available")
	}
	if result.FWAvailable {
		t.Error("expected no FW update")
	}
	if result.PiURL != "https://example.com/pi/1.1.0" {
		t.Errorf("PiURL = %q, want https://example.com/pi/1.1.0", result.PiURL)
	}
}

func TestCheckVersion_EmptyTargetsUsesLatest(t *testing.T) {
	idx := ReleaseIndex{
		Pi: ComponentIndex{
			Latest: "1.1.0",
			Releases: map[string]*Release{
				"1.1.0": {Version: "1.1.0", URL: "https://example.com/pi/1.1.0", SHA256: "bbb"},
			},
		},
		Firmware: ComponentIndex{
			Latest: "0.5.0",
			Releases: map[string]*Release{
				"0.5.0": {Version: "0.5.0", URL: "https://example.com/fw/0.5.0", SHA256: "ccc"},
			},
		},
	}
	srv := newTestServer(idx)
	defer srv.Close()

	u := New(Config{
		ServerBaseURL:    srv.URL,
		CurrentPiVersion: "1.0.0",
		CurrentFWVersion: "0.4.0",
	})

	result, err := u.CheckVersion("", "")
	if err != nil {
		t.Fatalf("CheckVersion() error: %v", err)
	}
	if !result.PiAvailable {
		t.Error("expected Pi update available (latest is newer)")
	}
	if !result.FWAvailable {
		t.Error("expected FW update available (latest is newer)")
	}
}

func TestCheckVersion_AlreadyCurrent(t *testing.T) {
	idx := ReleaseIndex{
		Pi: ComponentIndex{
			Latest:   "1.0.0",
			Releases: map[string]*Release{"1.0.0": {Version: "1.0.0"}},
		},
		Firmware: ComponentIndex{
			Latest:   "0.5.0",
			Releases: map[string]*Release{"0.5.0": {Version: "0.5.0"}},
		},
	}
	srv := newTestServer(idx)
	defer srv.Close()

	u := New(Config{
		ServerBaseURL:    srv.URL,
		CurrentPiVersion: "1.0.0",
		CurrentFWVersion: "0.5.0",
	})

	result, err := u.CheckVersion("", "")
	if err != nil {
		t.Fatalf("CheckVersion() error: %v", err)
	}
	if result.PiAvailable {
		t.Error("expected no Pi update (already current)")
	}
	if result.FWAvailable {
		t.Error("expected no FW update (already current)")
	}
}

func TestCheckVersion_UnknownVersion(t *testing.T) {
	idx := ReleaseIndex{
		Pi: ComponentIndex{
			Latest:   "1.0.0",
			Releases: map[string]*Release{"1.0.0": {Version: "1.0.0"}},
		},
		Firmware: ComponentIndex{Latest: "", Releases: map[string]*Release{}},
	}
	srv := newTestServer(idx)
	defer srv.Close()

	u := New(Config{ServerBaseURL: srv.URL, CurrentPiVersion: "1.0.0"})

	_, err := u.CheckVersion("9.9.9", "")
	if err == nil {
		t.Error("expected error for unknown version")
	}
}
