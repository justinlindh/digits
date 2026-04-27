package autodeploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLatestReleaseHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases" {
			http.Error(w, "bad path", 400)
			return
		}
		w.Header().Set("ETag", `W/"abc"`)
		_, _ = w.Write([]byte(`[
			{"tag_name":"firmware/v2.0.0","target_commitish":"aaa","draft":false,"prerelease":false},
			{"tag_name":"server/v1.9.1","target_commitish":"bbb","draft":false,"prerelease":false},
			{"tag_name":"server/v1.9.0","target_commitish":"ccc","draft":false,"prerelease":false}
		]`))
	}))
	defer srv.Close()

	c := NewGitHubClient(srv.URL, "")
	rel, err := c.LatestReleaseWithETag(context.Background(), "owner/repo", "server/v", "")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "server/v1.9.1" {
		t.Errorf("TagName=%q", rel.TagName)
	}
	if rel.CommitSHA != "bbb" {
		t.Errorf("CommitSHA=%q", rel.CommitSHA)
	}
	if rel.ETag != `W/"abc"` {
		t.Errorf("ETag=%q", rel.ETag)
	}
	if rel.NotModified {
		t.Error("NotModified=true, want false")
	}
}

func TestLatestReleaseETagNotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `W/"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		t.Fatalf("expected If-None-Match header")
	}))
	defer srv.Close()

	c := NewGitHubClient(srv.URL, "")
	rel, err := c.LatestReleaseWithETag(context.Background(), "owner/repo", "server/v", `W/"abc"`)
	if err != nil {
		t.Fatal(err)
	}
	if !rel.NotModified {
		t.Error("NotModified=false, want true")
	}
}

func TestLatestReleaseSkipsDraftsAndPrereleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"tag_name":"server/v2.0.0","draft":true,"target_commitish":"xxx"},
			{"tag_name":"server/v1.9.9","prerelease":true,"target_commitish":"yyy"},
			{"tag_name":"server/v1.9.1","target_commitish":"bbb"}
		]`))
	}))
	defer srv.Close()

	c := NewGitHubClient(srv.URL, "")
	rel, err := c.LatestReleaseWithETag(context.Background(), "owner/repo", "server/v", "")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "server/v1.9.1" {
		t.Errorf("TagName=%q", rel.TagName)
	}
}

func TestLatestReleaseNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"firmware/v1.0.0"}]`))
	}))
	defer srv.Close()

	c := NewGitHubClient(srv.URL, "")
	rel, err := c.LatestReleaseWithETag(context.Background(), "owner/repo", "server/v", "")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "" {
		t.Errorf("TagName=%q, want empty", rel.TagName)
	}
}

func TestLatestReleaseHTTPErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		msg  string
	}{
		{"401", 401, "github auth"},
		{"403", 403, "github auth"},
		{"404", 404, "github 404"},
		{"500", 500, "github 5xx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()
			c := NewGitHubClient(srv.URL, "")
			_, err := c.LatestReleaseWithETag(context.Background(), "owner/repo", "server/v", "")
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("err=%q, want containing %q", err.Error(), tc.msg)
			}
		})
	}
}
