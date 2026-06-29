package web

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

const hstsHeader = "max-age=31536000; includeSubDomains"

// isGateExempt reports whether a request path should bypass the welcome and
// onboarding redirect gates. /auth/*, /api/*, and /ws[/*] are always exempt
// because their consumers can't (or shouldn't) follow an HTML page redirect:
// SSE/fetch clients would parse the HTML as JSON, WS upgrades would fail,
// and the auth flow itself must reach /auth/login regardless of state. Each
// gate also passes its own redirect target (and any other gate's target it
// needs to defer to) as `extra` so a redirect-to-self can't loop.
func isGateExempt(path string, extra ...string) bool {
	for _, p := range extra {
		if path == p {
			return true
		}
	}
	if strings.HasPrefix(path, "/auth/") {
		return true
	}
	if strings.HasPrefix(path, "/api/") {
		return true
	}
	if path == "/ws" || strings.HasPrefix(path, "/ws/") {
		return true
	}
	return false
}

func securityHeadersMiddleware(baseURL string, next http.Handler) http.Handler {
	connectSrc := "'self' wss:"
	if baseURL != "" {
		wssOrigin := strings.Replace(baseURL, "https://", "wss://", 1)
		wssOrigin = strings.Replace(wssOrigin, "http://", "ws://", 1)
		connectSrc = "'self' " + wssOrigin
	}
	csp := fmt.Sprintf("default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src %s; frame-ancestors 'none'", connectSrc)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", hstsHeader)
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// csrfOriginCheck rejects state-changing requests (POST/PUT/DELETE/PATCH)
// whose Origin header does not match the configured base URL. GET/HEAD/OPTIONS
// are safe methods and pass through. Requests with no Origin header are allowed
// because non-browser clients (CLI tools, the Pi daemon) legitimately omit it.
func csrfOriginCheck(baseURL string, next http.Handler) http.Handler {
	if baseURL == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && origin != baseURL {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rootDomainRedirect redirects requests arriving on the bare root domain
// (e.g. digits.family) to the app URL (e.g. https://app.digits.family).
func rootDomainRedirect(appURL string, next http.Handler) http.Handler {
	if appURL == "" {
		return next
	}
	appHost := appURL
	if i := strings.Index(appURL, "://"); i >= 0 {
		appHost = appURL[i+3:]
	}
	if i := strings.Index(appHost, "/"); i >= 0 {
		appHost = appHost[:i]
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqHost := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			reqHost = h
		}
		appHostStripped := appHost
		if h, _, err := net.SplitHostPort(appHost); err == nil {
			appHostStripped = h
		}

		if reqHost == appHostStripped || reqHost == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Don't redirect WebSocket, API, or healthcheck paths. /healthz in
		// particular is hit over plain HTTP against localhost by the
		// autodeploy binary, which expects 200 + JSON and would mis-fire
		// on every tick if redirected to the canonical HTTPS origin.
		if strings.HasPrefix(r.URL.Path, "/ws") || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		target := appURL + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}
