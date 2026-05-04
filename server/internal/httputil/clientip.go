// Package httputil provides small request-scoped helpers shared across
// signald handlers, such as client-IP resolution and a private-address
// predicate.
package httputil

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the resolved client IP for an inbound HTTP request.
//
// Preference order:
//  1. The first entry of X-Forwarded-For, trimmed.
//  2. Host portion of r.RemoteAddr (port stripped).
//  3. r.RemoteAddr verbatim if SplitHostPort cannot parse it.
//
// Assumption: a single trusted reverse proxy (nginx-proxy-manager) is the
// only writer of X-Forwarded-For. Behind that assumption the leftmost
// XFF entry is the original client. If the deployment grows additional
// proxies, this function must be revisited.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IsPrivateAddr reports whether ip parses as an address we treat as
// "LAN-only" for display purposes: RFC1918 / RFC4193 (IsPrivate), loopback
// (IsLoopback), or link-local unicast (IsLinkLocalUnicast). Non-private
// ranges (public, CGNAT 100.64/10) are excluded by all three predicates.
// Empty or unparseable input returns false.
func IsPrivateAddr(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast()
}
