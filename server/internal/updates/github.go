package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ghRelease is a subset of the GitHub release API response.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	PublishedAt string    `json:"published_at"`
	Body        string    `json:"body"`
	Assets      []ghAsset `json:"assets"`
}

// ghAsset is a subset of the GitHub release asset API response.
type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// httpError represents a non-2xx HTTP response.
type httpError struct {
	StatusCode int
	Body       string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// GitHubReleases fetches and caches release metadata from a GitHub repository.
type GitHubReleases struct {
	owner   string
	repo    string
	apiBase string
	token   string
	client  *http.Client
	ttl     time.Duration

	mu     sync.RWMutex
	cached *ReleaseIndex

	// OnChange is called when refresh detects a new latest version for
	// either component. Called synchronously from SetIndex; the callback
	// should not block. Nil means no notification.
	OnChange func(piLatest, fwLatest string)
}

// NewGitHubReleases creates a GitHubReleases that polls the given repo.
// It fetches immediately in the background and refreshes every ttlSeconds.
func NewGitHubReleases(ctx context.Context, owner, repo, token string, ttlSeconds int, onChange func(piLatest, fwLatest string)) *GitHubReleases {
	g := &GitHubReleases{
		owner:    owner,
		repo:     repo,
		apiBase:  "https://api.github.com",
		token:    token,
		client:   &http.Client{Timeout: 15 * time.Second},
		ttl:      time.Duration(ttlSeconds) * time.Second,
		OnChange: onChange,
	}
	go g.poll(ctx)
	return g
}

// ReleaseIndex returns the cached release index, or nil if not yet fetched.
func (g *GitHubReleases) ReleaseIndex() *ReleaseIndex {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cached
}

// poll fetches immediately, then refreshes on the TTL interval until ctx
// is cancelled. Binding the loop to ctx matters for graceful shutdown: the
// signald caller passes its run-scoped ctx so this goroutine doesn't
// outlive the process's serve loop.
func (g *GitHubReleases) poll(ctx context.Context) {
	g.refresh(ctx)
	ticker := time.NewTicker(g.ttl)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.refresh(ctx)
		}
	}
}

// SetIndex replaces the cached release index. If OnChange is set and either
// component's latest version differs from the previous index, the callback is
// fired synchronously after the lock is released.
func (g *GitHubReleases) SetIndex(idx *ReleaseIndex) {
	g.mu.Lock()
	old := g.cached
	g.cached = idx
	cb := g.OnChange
	g.mu.Unlock()

	if cb != nil && old != nil && (old.Pi.Latest != idx.Pi.Latest || old.Firmware.Latest != idx.Firmware.Latest) {
		cb(idx.Pi.Latest, idx.Firmware.Latest)
	}
}

func (g *GitHubReleases) refresh(ctx context.Context) {
	idx, err := g.fetch(ctx)
	if err != nil {
		slog.Error("failed to fetch GitHub releases", "error", err)
		return
	}
	g.SetIndex(idx)
}

// ServeReleases returns an HTTP handler that serves the release index as JSON.
func (g *GitHubReleases) ServeReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idx := g.ReleaseIndex()
		if idx == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(idx); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
		}
	}
}

// fetch retrieves releases from the GitHub API and builds a ReleaseIndex.
// Note: fetches up to 100 releases (no pagination). If the repo exceeds 100
// total releases across all tag types, older ones will be silently omitted.
func (g *GitHubReleases) fetch(ctx context.Context) (*ReleaseIndex, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", g.apiBase, g.owner, g.repo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &httpError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var releases []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decoding releases: %w", err)
	}

	idx := &ReleaseIndex{
		Pi:       ComponentIndex{Releases: make(map[string]*Release)},
		Firmware: ComponentIndex{Releases: make(map[string]*Release)},
		Server:   ComponentIndex{Releases: make(map[string]*Release)},
	}

	for _, rel := range releases {
		component, version, ok := parseTag(rel.TagName)
		if !ok {
			continue
		}

		var binaryURL, sha256 string
		if component == ComponentServer {
			binaryURL = fmt.Sprintf("ghcr.io/%s/%s/signald:v%s", g.owner, g.repo, version)
		} else {
			var sha256URL string
			binaryURL, sha256URL = classifyAssets(rel.Assets)
			if binaryURL == "" {
				continue
			}
			if sha256URL != "" {
				sha256 = g.fetchSHA256(ctx, sha256URL)
			}
		}

		date := ""
		if t, err := time.Parse(time.RFC3339, rel.PublishedAt); err == nil {
			date = t.Format("2006-01-02")
		}

		r := &Release{
			Version:  version,
			SHA256:   sha256,
			URL:      binaryURL,
			Date:     date,
			Notes:    StripGroomedSentinel(rel.Body),
			AudioURL: findAudioAsset(rel.Assets),
		}

		var ci *ComponentIndex
		switch component {
		case ComponentPi:
			ci = &idx.Pi
		case ComponentFirmware:
			ci = &idx.Firmware
		case ComponentServer:
			ci = &idx.Server
		default:
			continue
		}

		ci.Releases[version] = r
		if ci.Latest == "" || CompareSemver(version, ci.Latest) > 0 {
			ci.Latest = version
		}
	}

	return idx, nil
}

// fetchSHA256 downloads a .sha256 asset and returns the trimmed content.
// Returns "" on any error.
func (g *GitHubReleases) fetchSHA256(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ""
	}
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(body))
}

// parseTag parses a GitHub release tag into a component name and version.
// Recognized prefixes: "fw/v", "pi/v", "server/v".
// Returns ("", "", false) for unrecognized tags.
func parseTag(tag string) (component, version string, ok bool) {
	parts := strings.SplitN(tag, "/v", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", false
	}

	switch parts[0] {
	case "fw":
		return ComponentFirmware, parts[1], true
	case "pi":
		return ComponentPi, parts[1], true
	case "server":
		return ComponentServer, parts[1], true
	default:
		return "", "", false
	}
}

// classifyAssets finds the main binary asset URL and the .sha256 companion URL
// from a list of release assets. Only recognizes known artifact patterns
// (firmware .elf files and digitsd aarch64 binaries).
func classifyAssets(assets []ghAsset) (binaryURL, sha256URL string) {
	var binaryName string
	for _, a := range assets {
		if a.BrowserDownloadURL == "" {
			continue
		}
		if strings.HasSuffix(a.Name, ".sha256") {
			continue
		}
		if strings.HasSuffix(a.Name, ".elf") || strings.Contains(a.Name, "aarch64") {
			binaryURL = a.BrowserDownloadURL
			binaryName = a.Name
		}
	}
	if binaryName != "" {
		for _, a := range assets {
			if a.Name == binaryName+".sha256" {
				sha256URL = a.BrowserDownloadURL
				break
			}
		}
	}
	return
}

// findAudioAsset returns the download URL of the first release-notes mp3
// asset, or "" if none is attached.
func findAudioAsset(assets []ghAsset) string {
	for _, a := range assets {
		if strings.HasPrefix(a.Name, "release-notes") && strings.HasSuffix(a.Name, ".mp3") {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

// NewGitHubReleasesWithIndex returns a GitHubReleases prepopulated with
// the given index. Intended for tests that do not want to hit the real
// API. The returned instance does not poll.
func NewGitHubReleasesWithIndex(idx *ReleaseIndex) *GitHubReleases {
	g := &GitHubReleases{}
	g.mu.Lock()
	g.cached = idx
	g.mu.Unlock()
	return g
}

// GroomedSentinel is the idempotency marker the release-groom workflow
// prepends to groomed release bodies. Exported so tests and external
// callers can reference the same literal as the production code.
const GroomedSentinel = "<!-- groomed:v1 -->"

// StripGroomedSentinel removes the GroomedSentinel prefix from a release
// body if present. Leaves non-groomed bodies untouched.
func StripGroomedSentinel(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if !strings.HasPrefix(trimmed, GroomedSentinel) {
		return s
	}
	return strings.TrimLeft(trimmed[len(GroomedSentinel):], " \t\r\n")
}
