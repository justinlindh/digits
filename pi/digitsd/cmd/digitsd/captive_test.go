package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMountCaptivePortalRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mountCaptivePortalRedirects(mux, "/")

	for _, path := range captivePortalProbePaths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusFound {
			t.Errorf("%s: status = %d, want %d (302)", path, rec.Code, http.StatusFound)
		}
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("%s: Location = %q, want %q", path, loc, "/")
		}
	}
}

// A path that isn't a probe must not be claimed by the redirect handlers, so
// the static file server / other routes still see it.
func TestMountCaptivePortalRedirects_LeavesOtherPathsAlone(t *testing.T) {
	mux := http.NewServeMux()
	mountCaptivePortalRedirects(mux, "/")
	hit := false
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) { hit = true })

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	if !hit {
		t.Fatal("/api/status was not routed to its own handler")
	}
}
