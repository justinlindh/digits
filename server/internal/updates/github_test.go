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
	sha256fw := strings.Repeat("a", 64)
	sha256pi := strings.Repeat("b", 64)

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
	gh.refresh(context.Background())

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

	// Pi: releases without checksum sidecars are not installable and are omitted.
	if idx.Pi.Latest != "1.1.0" {
		t.Errorf("pi latest = %q, want %q", idx.Pi.Latest, "1.1.0")
	}
	if len(idx.Pi.Releases) != 1 {
		t.Errorf("pi releases count = %d, want 1", len(idx.Pi.Releases))
	}
	if _, ok := idx.Pi.Releases["1.0.0"]; ok {
		t.Error("pi 1.0.0 without checksum was advertised")
	}
	if _, ok := idx.Server.Releases["1.0.0"]; !ok {
		t.Error("server container release without checksum was omitted")
	}
}

func TestGitHubReleases_ChecksumHTTPFailureOmitsBinaryRelease(t *testing.T) {
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test-owner/test-repo/releases":
			_ = json.NewEncoder(w).Encode([]ghRelease{{
				TagName: "pi/v1.2.0",
				Assets: []ghAsset{
					{Name: "digitsd-1.2.0-aarch64", BrowserDownloadURL: baseURL + "/digitsd"},
					{Name: "digitsd-1.2.0-aarch64.sha256", BrowserDownloadURL: baseURL + "/digitsd.sha256"},
				},
			}})
		case "/digitsd.sha256":
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	gh := newTestGitHubReleases("test-owner", "test-repo")
	gh.apiBase = srv.URL
	gh.client = srv.Client()
	gh.refresh(context.Background())
	if idx := gh.ReleaseIndex(); idx != nil {
		t.Fatalf("release index created from incomplete metadata: %+v", idx.Pi)
	}
}

func TestGitHubReleases_ChecksumHTTPFailurePreservesCachedIndex(t *testing.T) {
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test-owner/test-repo/releases":
			_ = json.NewEncoder(w).Encode([]ghRelease{{
				TagName: "pi/v1.2.0",
				Assets: []ghAsset{
					{Name: "digitsd-1.2.0-aarch64", BrowserDownloadURL: baseURL + "/digitsd"},
					{Name: "digitsd-1.2.0-aarch64.sha256", BrowserDownloadURL: baseURL + "/digitsd.sha256"},
				},
			}})
		case "/digitsd.sha256":
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	cached := &ReleaseIndex{Pi: ComponentIndex{Latest: "1.1.0", Releases: map[string]*Release{
		"1.1.0": {Version: "1.1.0", SHA256: strings.Repeat("a", 64)},
	}}}
	gh := NewGitHubReleasesWithIndex(cached)
	gh.owner = "test-owner"
	gh.repo = "test-repo"
	gh.apiBase = srv.URL
	gh.client = srv.Client()
	gh.refresh(context.Background())

	if idx := gh.ReleaseIndex(); idx != cached {
		t.Fatalf("transient checksum failure replaced cached index: %+v", idx)
	}
}

func TestGitHubReleases_InvalidChecksumOmitsBinaryRelease(t *testing.T) {
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test-owner/test-repo/releases":
			_ = json.NewEncoder(w).Encode([]ghRelease{{
				TagName: "fw/v1.2.0",
				Assets: []ghAsset{
					{Name: "firmware.elf", BrowserDownloadURL: baseURL + "/firmware.elf"},
					{Name: "firmware.elf.sha256", BrowserDownloadURL: baseURL + "/firmware.elf.sha256"},
				},
			}})
		case "/firmware.elf.sha256":
			_, _ = w.Write([]byte("not-a-sha256"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	gh := newTestGitHubReleases("test-owner", "test-repo")
	gh.apiBase = srv.URL
	gh.client = srv.Client()
	idx, err := gh.fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch() error: %v", err)
	}
	if idx.Firmware.Latest != "" || len(idx.Firmware.Releases) != 0 {
		t.Fatalf("release with invalid checksum advertised: %+v", idx.Firmware)
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
	gh.refresh(context.Background())
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

	gh.refresh(context.Background())
	idx := gh.ReleaseIndex()
	if idx != nil {
		t.Errorf("expected nil ReleaseIndex on API error, got %+v", idx)
	}
}

func TestClassifyAssets_SHA256NotPickedAsBinary(t *testing.T) {
	// GitHub doesn't guarantee asset order: .sha256 might come last and
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
		{"server/v1.0.0", "server", "1.0.0", true},
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
				{"name": "firmware-1.4.0.elf", "browser_download_url": "BINARY_URL"},
				{"name": "firmware-1.4.0.elf.sha256", "browser_download_url": "SHA256_URL"}
			]
		}
	]`
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/firmware.sha256" {
			_, _ = w.Write([]byte(strings.Repeat("c", 64)))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		body := strings.ReplaceAll(respBody, "BINARY_URL", srv.URL+"/firmware.elf")
		body = strings.ReplaceAll(body, "SHA256_URL", srv.URL+"/firmware.sha256")
		_, _ = w.Write([]byte(body))
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

func TestOnChangeCalledWhenVersionChanges(t *testing.T) {
	idx1 := &ReleaseIndex{
		Pi:       ComponentIndex{Latest: "1.0.0", Releases: map[string]*Release{"1.0.0": {Version: "1.0.0"}}},
		Firmware: ComponentIndex{Latest: "1.0.0", Releases: map[string]*Release{"1.0.0": {Version: "1.0.0"}}},
	}
	g := NewGitHubReleasesWithIndex(idx1)

	var called bool
	var gotPi, gotFW string
	g.OnChange = func(piLatest, fwLatest string) {
		called = true
		gotPi = piLatest
		gotFW = fwLatest
	}

	idx2 := &ReleaseIndex{
		Pi:       ComponentIndex{Latest: "2.0.0", Releases: map[string]*Release{"2.0.0": {Version: "2.0.0"}}},
		Firmware: ComponentIndex{Latest: "1.5.0", Releases: map[string]*Release{"1.5.0": {Version: "1.5.0"}}},
	}
	g.SetIndex(idx2)

	if !called {
		t.Fatal("OnChange was not called")
	}
	if gotPi != "2.0.0" {
		t.Errorf("pi version: got %q, want %q", gotPi, "2.0.0")
	}
	if gotFW != "1.5.0" {
		t.Errorf("fw version: got %q, want %q", gotFW, "1.5.0")
	}
}

func TestOnChangeNotCalledWhenVersionsSame(t *testing.T) {
	idx := &ReleaseIndex{
		Pi:       ComponentIndex{Latest: "1.0.0", Releases: map[string]*Release{"1.0.0": {Version: "1.0.0"}}},
		Firmware: ComponentIndex{Latest: "1.0.0", Releases: map[string]*Release{"1.0.0": {Version: "1.0.0"}}},
	}
	g := NewGitHubReleasesWithIndex(idx)

	called := false
	g.OnChange = func(piLatest, fwLatest string) {
		called = true
	}

	same := &ReleaseIndex{
		Pi:       ComponentIndex{Latest: "1.0.0", Releases: map[string]*Release{"1.0.0": {Version: "1.0.0"}}},
		Firmware: ComponentIndex{Latest: "1.0.0", Releases: map[string]*Release{"1.0.0": {Version: "1.0.0"}}},
	}
	g.SetIndex(same)

	if called {
		t.Error("OnChange should not be called when versions are unchanged")
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
			got := StripGroomedSentinel(tt.in)
			if got != tt.want {
				t.Errorf("StripGroomedSentinel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
