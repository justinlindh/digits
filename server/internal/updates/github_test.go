package updates

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestGitHubReleases(owner, repo string) *GitHubReleases {
	return &GitHubReleases{
		owner:  owner,
		repo:   repo,
		client: &http.Client{},
		ttl:    300 * time.Second,
	}
}

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
				{Name: "digitsd-1.1.0-aarch64", BrowserDownloadURL: "https://example.com/digitsd-1.1.0"},
				{Name: "digitsd-1.1.0-aarch64.sha256", BrowserDownloadURL: "SHA256_PI_URL"},
			},
		},
		{
			TagName:     "pi/v1.0.0",
			PublishedAt: "2026-03-10T00:00:00Z",
			Assets: []ghAsset{
				{Name: "digitsd-1.0.0-aarch64", BrowserDownloadURL: "https://example.com/digitsd-1.0.0"},
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
			_ = json.NewEncoder(w).Encode(releases)
		default:
			// Serve SHA256 content based on URL query or path
			if r.URL.String() == "/sha256fw" {
				_, _ = w.Write([]byte(sha256fw + "\n"))
			} else if r.URL.String() == "/sha256pi" {
				_, _ = w.Write([]byte(sha256pi + "\n"))
			} else {
				http.NotFound(w, r)
			}
		}
	}))
	defer srv.Close()

	// Patch sha256 URLs to point to our mock server
	releases[0].Assets[1].BrowserDownloadURL = srv.URL + "/sha256fw"
	releases[1].Assets[1].BrowserDownloadURL = srv.URL + "/sha256pi"

	gh := newTestGitHubReleases("test-owner", "test-repo")
	gh.apiBase = srv.URL
	gh.client = srv.Client()
	gh.refresh()

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
			_ = json.NewEncoder(w).Encode([]ghRelease{
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

	gh := newTestGitHubReleases("test-owner", "test-repo")
	gh.apiBase = srv.URL
	gh.client = srv.Client()

	// Fetch once, then read from cache
	gh.refresh()
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
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	gh := newTestGitHubReleases("test-owner", "test-repo")
	gh.apiBase = srv.URL
	gh.client = srv.Client()

	gh.refresh()
	idx := gh.ReleaseIndex()
	if idx != nil {
		t.Errorf("expected nil ReleaseIndex on API error, got %+v", idx)
	}
}

func TestClassifyAssets_SHA256NotPickedAsBinary(t *testing.T) {
	// GitHub doesn't guarantee asset order — .sha256 might come last and
	// its name also contains "aarch64", so it must be explicitly skipped.
	assets := []ghAsset{
		{Name: "digitsd-1.0.0-aarch64.sha256", BrowserDownloadURL: "https://example.com/digitsd.sha256"},
		{Name: "digitsd-1.0.0-aarch64", BrowserDownloadURL: "https://example.com/digitsd"},
	}
	binaryURL, sha256URL := classifyAssets(assets)
	if binaryURL != "https://example.com/digitsd" {
		t.Errorf("binaryURL = %q, want the binary not the checksum", binaryURL)
	}
	if sha256URL != "https://example.com/digitsd.sha256" {
		t.Errorf("sha256URL = %q, want the .sha256 URL", sha256URL)
	}

	// Also test reversed order (sha256 after binary)
	assets2 := []ghAsset{
		{Name: "digitsd-1.0.0-aarch64", BrowserDownloadURL: "https://example.com/digitsd"},
		{Name: "digitsd-1.0.0-aarch64.sha256", BrowserDownloadURL: "https://example.com/digitsd.sha256"},
	}
	binaryURL2, sha256URL2 := classifyAssets(assets2)
	if binaryURL2 != "https://example.com/digitsd" {
		t.Errorf("reversed: binaryURL = %q, want the binary", binaryURL2)
	}
	if sha256URL2 != "https://example.com/digitsd.sha256" {
		t.Errorf("reversed: sha256URL = %q, want the .sha256 URL", sha256URL2)
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

func TestFetchPopulatesNotes(t *testing.T) {
	respBody := `[
		{
			"tag_name": "fw/v1.4.0",
			"published_at": "2026-04-20T12:00:00Z",
			"body": "<!-- groomed:v1 -->\nThe 4-key, vanquished.",
			"assets": [
				{"name": "firmware-1.4.0.elf", "browser_download_url": "https://example.invalid/fw-1.4.0.elf"}
			]
		}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()

	g := &GitHubReleases{
		owner:   "test",
		repo:    "test",
		apiBase: srv.URL,
		client:  srv.Client(),
		ttl:     60,
	}
	idx, err := g.fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	rel, ok := idx.Firmware.Releases["1.4.0"]
	if !ok {
		t.Fatalf("firmware 1.4.0 not found in index")
	}
	if rel.Notes != "The 4-key, vanquished." {
		t.Errorf("Notes = %q, want %q", rel.Notes, "The 4-key, vanquished.")
	}
}

func TestStripGroomedSentinel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no sentinel", "hello world", "hello world"},
		{"sentinel only", "<!-- groomed:v1 -->", ""},
		{"sentinel with newline", "<!-- groomed:v1 -->\nhello", "hello"},
		{"sentinel with crlf", "<!-- groomed:v1 -->\r\nhello", "hello"},
		{"sentinel with surrounding whitespace", "  <!-- groomed:v1 -->  \n\nhello", "hello"},
		{"sentinel not at start", "prefix\n<!-- groomed:v1 -->\nhello", "prefix\n<!-- groomed:v1 -->\nhello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripGroomedSentinel(tt.in)
			if got != tt.want {
				t.Errorf("stripGroomedSentinel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
