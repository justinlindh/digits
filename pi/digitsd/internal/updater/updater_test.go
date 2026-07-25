package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

func TestDownload_ResumesPartialAfterFailure(t *testing.T) {
	body := []byte("digitsd-binary-payload-large-enough-to-split")
	sum := sha256.Sum256(body)
	wantSHA := hex.EncodeToString(sum[:])
	half := len(body) / 2

	var attempt int
	var resumeRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			// Advertise the full length but send only half, so the client's
			// io.Copy fails with an unexpected EOF mid-body.
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body[:half])
			return
		}
		resumeRange = r.Header.Get("Range")
		if resumeRange == "" {
			t.Error("second attempt sent no Range header")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
			return
		}
		w.Header().Set("Content-Range",
			fmt.Sprintf("bytes %d-%d/%d", half, len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[half:])
	}))
	defer srv.Close()

	staging := t.TempDir()
	u := New(Config{ServerBaseURL: srv.URL, StagingDir: staging})

	if _, err := u.Download(srv.URL+"/artifact", "digitsd", wantSHA); err == nil {
		t.Fatal("expected error from truncated body, got nil")
	}
	tmp := filepath.Join(staging, "digitsd.tmp")
	if got, err := os.ReadFile(tmp); err != nil || len(got) != half {
		t.Fatalf("partial not kept: err=%v len=%d want %d", err, len(got), half)
	}

	dest, err := u.Download(srv.URL+"/artifact", "digitsd", wantSHA)
	if err != nil {
		t.Fatalf("resumed Download() error: %v", err)
	}
	if want := fmt.Sprintf("bytes=%d-", half); resumeRange != want {
		t.Errorf("Range header = %q, want %q", resumeRange, want)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("resumed content = %q, want %q", got, body)
	}
	if _, err := os.Stat(tmp + ".sha"); !os.IsNotExist(err) {
		t.Errorf("resume marker not cleaned up after success: %v", err)
	}
}

func TestDownload_RangeIgnoredRestartsFresh(t *testing.T) {
	body := []byte("digitsd-binary-payload")
	sum := sha256.Sum256(body)
	wantSHA := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignore any Range header and always send the full body with 200.
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	staging := t.TempDir()
	// Pre-seed a stale partial for the same artifact; the 200 response must
	// discard it rather than append.
	if err := os.WriteFile(filepath.Join(staging, "digitsd.tmp"), []byte("stale-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "digitsd.tmp.sha"), []byte(wantSHA), 0644); err != nil {
		t.Fatal(err)
	}

	u := New(Config{ServerBaseURL: srv.URL, StagingDir: staging})
	dest, err := u.Download(srv.URL+"/artifact", "digitsd", wantSHA)
	if err != nil {
		t.Fatalf("Download() error: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("content = %q, want %q", got, body)
	}
}

func TestDownload_MarkerMismatchRestartsFresh(t *testing.T) {
	body := []byte("digitsd-binary-payload")
	sum := sha256.Sum256(body)
	wantSHA := hex.EncodeToString(sum[:])

	var sawRange bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			sawRange = true
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	staging := t.TempDir()
	// Partial from a previous release: marker holds a different sha, so the
	// download must not attempt to resume it.
	if err := os.WriteFile(filepath.Join(staging, "digitsd.tmp"), []byte("old-release-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "digitsd.tmp.sha"), []byte("deadbeef"), 0644); err != nil {
		t.Fatal(err)
	}

	u := New(Config{ServerBaseURL: srv.URL, StagingDir: staging})
	dest, err := u.Download(srv.URL+"/artifact", "digitsd", wantSHA)
	if err != nil {
		t.Fatalf("Download() error: %v", err)
	}
	if sawRange {
		t.Error("sent Range header despite marker mismatch")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("content = %q, want %q", got, body)
	}
}

func TestCheckVersion_StaleIndexDoesNotDowngrade(t *testing.T) {
	// A replica whose release index has not refreshed yet advertises the
	// previous release as Latest; implicit-latest resolution must not offer
	// that downgrade.
	srv := newTestServer(ReleaseIndex{
		Pi: ComponentIndex{Latest: "1.44.7", Releases: map[string]*Release{
			"1.44.7": {Version: "1.44.7"},
		}},
		Firmware: ComponentIndex{Latest: "1.14.2", Releases: map[string]*Release{
			"1.14.2": {Version: "1.14.2"},
		}},
	})
	defer srv.Close()

	u := New(Config{ServerBaseURL: srv.URL, CurrentPiVersion: "1.44.8", CurrentFWVersion: "1.14.3"})
	res, err := u.CheckVersion("", "")
	if err != nil {
		t.Fatalf("CheckVersion() error: %v", err)
	}
	if res.PiAvailable {
		t.Errorf("PiAvailable = true for stale index (current 1.44.8, latest 1.44.7)")
	}
	if res.FWAvailable {
		t.Errorf("FWAvailable = true for stale index (current 1.14.3, latest 1.14.2)")
	}
}

func TestCheckVersion_ExplicitTargetStillDowngrades(t *testing.T) {
	// Operator-pinned targets keep exact-match semantics: a deliberate
	// rollback to an older release must still be offered.
	srv := newTestServer(ReleaseIndex{
		Pi: ComponentIndex{Latest: "1.44.8", Releases: map[string]*Release{
			"1.44.6": {Version: "1.44.6"},
			"1.44.8": {Version: "1.44.8"},
		}},
	})
	defer srv.Close()

	u := New(Config{ServerBaseURL: srv.URL, CurrentPiVersion: "1.44.8"})
	res, err := u.CheckVersion("1.44.6", "")
	if err != nil {
		t.Fatalf("CheckVersion() error: %v", err)
	}
	if !res.PiAvailable || res.PiVersion != "1.44.6" {
		t.Errorf("explicit downgrade not offered: available=%v version=%q", res.PiAvailable, res.PiVersion)
	}
}

func TestVersionIsNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.14.3", "1.14.2", true},
		{"1.14.2", "1.14.3", false},
		{"1.14.3", "1.14.3", false},
		{"1.44.10", "1.44.9", true},
		{"1.14.3", "1.14.1-33-g0a4f0773-dirty", true},
		{"weird-build", "other-build", true},
	}
	for _, c := range cases {
		if got := versionIsNewer(c.candidate, c.current); got != c.want {
			t.Errorf("versionIsNewer(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}
