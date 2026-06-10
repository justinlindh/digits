package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
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

func TestDownload_VerifiesAndWrites(t *testing.T) {
	body := []byte("digitsd-binary-payload")
	sum := sha256.Sum256(body)
	wantSHA := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	u := New(Config{ServerBaseURL: srv.URL, StagingDir: t.TempDir()})

	dest, err := u.Download(srv.URL+"/artifact", "digitsd", wantSHA)
	if err != nil {
		t.Fatalf("Download() error: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("downloaded content = %q, want %q", got, body)
	}
}

func TestDownload_StalledBodyHitsDeadline(t *testing.T) {
	// Server sends headers then stalls forever without sending the body. The
	// per-request context deadline must abort the download instead of blocking
	// io.Copy indefinitely.
	block := make(chan struct{})
	// srv.Close() (deferred first, so runs last) waits for in-flight handlers;
	// unblock the handler before that by deferring close(block) afterwards so
	// LIFO runs it first.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-block // never send the body until the test unblocks on cleanup
	}))
	defer srv.Close()
	defer close(block)

	u := New(Config{ServerBaseURL: srv.URL, StagingDir: t.TempDir()})
	u.downloadTimeout = 200 * time.Millisecond

	start := time.Now()
	_, err := u.Download(srv.URL+"/artifact", "digitsd", "")
	if err == nil {
		t.Fatal("expected error from stalled download, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Download took %v; deadline was not enforced", elapsed)
	}
}
