package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Upstream handler that rootDomainRedirect should pass through.
func testOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pass"))
	})
}

func TestRootDomainRedirect(t *testing.T) {
	const appURL = "https://app.digits.family"

	cases := map[string]struct {
		host     string
		path     string
		wantCode int
		wantLoc  string // expected Location header when a redirect is expected
	}{
		"canonical host passes through": {
			host:     "app.digits.family",
			path:     "/",
			wantCode: http.StatusOK,
		},
		"empty host passes through": {
			host:     "",
			path:     "/",
			wantCode: http.StatusOK,
		},
		"non-canonical host redirects root": {
			host:     "digits.family",
			path:     "/",
			wantCode: http.StatusMovedPermanently,
			wantLoc:  "https://app.digits.family/",
		},
		"non-canonical host redirects arbitrary page": {
			host:     "digits.family",
			path:     "/phones",
			wantCode: http.StatusMovedPermanently,
			wantLoc:  "https://app.digits.family/phones",
		},
		"/ws is exempt": {
			host:     "localhost:8090",
			path:     "/ws",
			wantCode: http.StatusOK,
		},
		"/api/* is exempt": {
			host:     "localhost:8090",
			path:     "/api/version",
			wantCode: http.StatusOK,
		},
		"/healthz is exempt": {
			host:     "localhost:8090",
			path:     "/healthz",
			wantCode: http.StatusOK,
		},
		"canonical host with port is treated as canonical": {
			host:     "app.digits.family:443",
			path:     "/",
			wantCode: http.StatusOK,
		},
	}

	h := rootDomainRedirect(appURL, testOKHandler())

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("code=%d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantLoc != "" {
				if got := rec.Header().Get("Location"); got != tc.wantLoc {
					t.Errorf("Location=%q, want %q", got, tc.wantLoc)
				}
			}
		})
	}
}

func TestRootDomainRedirectEmptyAppURLDisables(t *testing.T) {
	// If appURL is unset, the middleware should be a no-op on every request.
	h := rootDomainRedirect("", testOKHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "anywhere.example"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("code=%d, want 200 (middleware disabled)", rec.Code)
	}
}
