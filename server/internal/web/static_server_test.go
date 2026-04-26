package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Production path: /static/* must be served from the embedded FS so the
// binary is self-contained on deploy. A response from the embed path
// carries the dialup.css top comment.
func TestStaticFileServer_ProductionServesFromEmbed(t *testing.T) {
	h := staticFileServer(false, "")

	req := httptest.NewRequest(http.MethodGet, "/static/dialup.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/dialup.css: got %d, want 200", rec.Code)
	}
	// dialup.css opens with a comment block naming the theme; match
	// a stable substring that should not drift without intent.
	if !strings.Contains(rec.Body.String(), "dial-up online service 1997 theme") {
		t.Error("embedded dialup.css did not contain expected header comment")
	}
}

// Dev path: /static/* must be served from whatever disk directory is
// passed via HandlerConfig.DevStaticDir so CSS edits don't require a
// rebuild. Uses a temp directory so the test is independent of CWD.
func TestStaticFileServer_DevServesFromDisk(t *testing.T) {
	dir := t.TempDir()
	marker := []byte("/* dev-mode disk-serve smoke test */\n")
	if err := os.WriteFile(filepath.Join(dir, "probe.css"), marker, 0o644); err != nil {
		t.Fatalf("write probe file: %v", err)
	}

	h := staticFileServer(true, dir)
	req := httptest.NewRequest(http.MethodGet, "/static/probe.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/probe.css: got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "dev-mode disk-serve smoke test") {
		t.Errorf("dev-mode handler did not serve the on-disk probe content; got body: %q", rec.Body.String())
	}
}

// DTMF tone wavs must ship in the embedded FS so the keypad on /phones can
// fetch /static/audio/dtmf/dtmf_<n>.wav without a side-channel asset deploy.
func TestStaticFileServer_DTMFWavsEmbedded(t *testing.T) {
	h := staticFileServer(false, "")

	for d := 0; d < 10; d++ {
		path := fmt.Sprintf("/static/audio/dtmf/dtmf_%d.wav", d)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200", path, rec.Code)
			continue
		}
		body := rec.Body.Bytes()
		if len(body) < 12 || string(body[0:4]) != "RIFF" || string(body[8:12]) != "WAVE" {
			t.Errorf("GET %s: response did not start with WAV magic", path)
		}
	}
}

// Empty DevStaticDir falls back to the devStaticDirDefault constant so
// callers that only flip DevMode get the expected behavior.
func TestStaticFileServer_DevDefaultDirResolves(t *testing.T) {
	h := staticFileServer(true, "")
	if h == nil {
		t.Fatal("staticFileServer(true, \"\") returned nil")
	}
	// We don't assert on response content here because CWD may not host
	// the default directory; the presence of a non-nil handler is enough
	// to confirm the fallback branch doesn't blow up at construction.
}
