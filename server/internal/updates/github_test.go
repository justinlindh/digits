package updates

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestGitHubReleases_BuildsIndex(t *testing.T) {
	sha256fw := "abc123deadbeef"
	sha256pi := "def456cafebabe"

	releases := []ghRelease{
		{
			TagName:     "fw/v1.0.0",
			PublishedAt: "2026-03-01T00:00:00Z",
			Assets: []ghAsset{
				{Name: "firmware.elf", BrowserDownloadURL: "https://example.com/firmware.elf"},
				{Name: "firmware.elf.sha256", BrowserDownloadURL: "SHA256_FW_URL"},
			},
		},
		{
			TagName:     "pi/v1.1.0",
			PublishedAt: "2026-03-15T00:00:00Z",
			Assets: []ghAsset{
				{Name: "digitsd", BrowserDownloadURL: "https://example.com/digitsd-1.1.0"},
				{Name: "digitsd.sha256", BrowserDownloadURL: "SHA256_PI_URL"},
			},
		},
		{
			TagName:     "pi/v1.0.0",
			PublishedAt: "2026-03-10T00:00:00Z",
			Assets: []ghAsset{
				{Name: "digitsd", BrowserDownloadURL: "https://example.com/digitsd-1.0.0"},
				// no sha256 asset
			},
		},
		{
			TagName:     "server/v1.0.0",
			PublishedAt: "2026-04-01T00:00:00Z",
			Assets: []ghAsset{
				{Name: "server-binary", BrowserDownloadURL: "https://example.com/server"},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test-owner/test-repo/releases":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(releases)
		default:
			// Serve SHA256 content based on URL query or path
			if r.URL.String() == "/sha256fw" {
				w.Write([]byte(sha256fw + "\n"))
			} else if r.URL.String() == "/sha256pi" {
				w.Write([]byte(sha256pi + "\n"))
			} else {
				http.NotFound(w, r)
			}
		}
	}))
	defer srv.Close()

	// Patch sha256 URLs to point to our mock server
	releases[0].Assets[1].BrowserDownloadURL = srv.URL + "/sha256fw"
	releases[1].Assets[1].BrowserDownloadURL = srv.URL + "/sha256pi"

	gh := NewGitHubReleases("test-owner", "test-repo", 300)
	gh.apiBase = srv.URL
	gh.client = srv.Client()

	idx := gh.ReleaseIndex()
	if idx == nil {
		t.Fatal("expected non-nil ReleaseIndex")
	}

	// Firmware: latest=1.0.0, 1 release, has SHA256
	if idx.Firmware.Latest != "1.0.0" {
		t.Errorf("firmware latest = %q, want %q", idx.Firmware.Latest, "1.0.0")
	}
	if len(idx.Firmware.Releases) != 1 {
		t.Errorf("firmware releases count = %d, want 1", len(idx.Firmware.Releases))
	}
	fwRel := idx.Firmware.Releases["1.0.0"]
	if fwRel == nil {
		t.Fatal("firmware 1.0.0 release is nil")
	}
	if fwRel.SHA256 != sha256fw {
		t.Errorf("firmware sha256 = %q, want %q", fwRel.SHA256, sha256fw)
	}

	// Pi: latest=1.1.0, 2 releases
	if idx.Pi.Latest != "1.1.0" {
		t.Errorf("pi latest = %q, want %q", idx.Pi.Latest, "1.1.0")
	}
	if len(idx.Pi.Releases) != 2 {
		t.Errorf("pi releases count = %d, want 2", len(idx.Pi.Releases))
	}
	piOld := idx.Pi.Releases["1.0.0"]
	if piOld == nil {
		t.Fatal("pi 1.0.0 release is nil")
	}
	if piOld.SHA256 != "" {
		t.Errorf("pi 1.0.0 sha256 = %q, want empty", piOld.SHA256)
	}
}

func TestGitHubReleases_CachesTTL(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/test-owner/test-repo/releases" {
			callCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]ghRelease{
				{
					TagName:     "fw/v1.0.0",
					PublishedAt: "2026-03-01T00:00:00Z",
					Assets: []ghAsset{
						{Name: "firmware.elf", BrowserDownloadURL: "https://example.com/fw"},
					},
				},
			})
		}
	}))
	defer srv.Close()

	gh := NewGitHubReleases("test-owner", "test-repo", 300)
	gh.apiBase = srv.URL
	gh.client = srv.Client()

	// Call multiple times
	gh.ReleaseIndex()
	gh.ReleaseIndex()
	gh.ReleaseIndex()

	if got := callCount.Load(); got != 1 {
		t.Errorf("API called %d times, want 1", got)
	}
}

func TestGitHubReleases_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	gh := NewGitHubReleases("test-owner", "test-repo", 300)
	gh.apiBase = srv.URL
	gh.client = srv.Client()

	idx := gh.ReleaseIndex()
	if idx != nil {
		t.Errorf("expected nil ReleaseIndex on API error, got %+v", idx)
	}
}

func TestParseTag(t *testing.T) {
	cases := []struct {
		tag           string
		wantComponent string
		wantVersion   string
		wantOK        bool
	}{
		{"fw/v1.0.0", "firmware", "1.0.0", true},
		{"pi/v2.3.1", "pi", "2.3.1", true},
		{"server/v1.0.0", "", "", false},
		{"v1.0.0", "", "", false},
		{"garbage", "", "", false},
	}
	for _, c := range cases {
		component, version, ok := parseTag(c.tag)
		if component != c.wantComponent || version != c.wantVersion || ok != c.wantOK {
			t.Errorf("parseTag(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.tag, component, version, ok, c.wantComponent, c.wantVersion, c.wantOK)
		}
	}
}
