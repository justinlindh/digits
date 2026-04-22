package autodeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Release struct {
	TagName     string
	CommitSHA   string
	ETag        string
	NotModified bool
}

type GitHubClient struct {
	base  string
	token string
	hc    *http.Client
}

func NewGitHubClient(base, token string) *GitHubClient {
	if base == "" {
		base = "https://api.github.com"
	}
	return &GitHubClient{
		base:  strings.TrimRight(base, "/"),
		token: token,
		hc:    &http.Client{Timeout: 15 * time.Second},
	}
}

type releaseJSON struct {
	TagName         string `json:"tag_name"`
	TargetCommitish string `json:"target_commitish"`
	Draft           bool   `json:"draft"`
	Prerelease      bool   `json:"prerelease"`
}

// LatestRelease returns the newest non-draft, non-prerelease release whose
// tag_name starts with tagPrefix.
func (c *GitHubClient) LatestRelease(ctx context.Context, repo, tagPrefix string) (Release, error) {
	return c.LatestReleaseWithETag(ctx, repo, tagPrefix, "")
}

// LatestReleaseWithETag is like LatestRelease but sends If-None-Match so an
// unchanged release list returns 304 and does not consume rate-limit budget.
func (c *GitHubClient) LatestReleaseWithETag(ctx context.Context, repo, tagPrefix, priorETag string) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases", c.base, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if priorETag != "" {
		req.Header.Set("If-None-Match", priorETag)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return Release{NotModified: true, ETag: priorETag}, nil
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return Release{}, fmt.Errorf("github auth: %d %s", resp.StatusCode, resp.Status)
	case http.StatusNotFound:
		return Release{}, fmt.Errorf("github 404: repo %s", repo)
	default:
		if resp.StatusCode >= 500 {
			return Release{}, fmt.Errorf("github 5xx: %d %s", resp.StatusCode, resp.Status)
		}
		return Release{}, fmt.Errorf("github unexpected: %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Release{}, fmt.Errorf("github read body: %w", err)
	}
	var items []releaseJSON
	if err := json.Unmarshal(body, &items); err != nil {
		return Release{}, fmt.Errorf("github decode: %w", err)
	}

	for _, r := range items {
		if r.Draft || r.Prerelease {
			continue
		}
		if !strings.HasPrefix(r.TagName, tagPrefix) {
			continue
		}
		return Release{
			TagName:   r.TagName,
			CommitSHA: r.TargetCommitish,
			ETag:      resp.Header.Get("ETag"),
		}, nil
	}
	return Release{ETag: resp.Header.Get("ETag")}, nil
}
