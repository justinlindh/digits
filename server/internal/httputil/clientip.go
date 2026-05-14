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
// X-Forwarded-For is only trusted when RemoteAddr is a private or loopback
// address, meaning the request arrived through a local reverse proxy. Direct
// connections from untrusted networks use RemoteAddr so an attacker cannot
// rotate XFF to bypass rate limiters.
func ClientIP(r *http.Request) string {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	if IsPrivateAddr(remoteHost) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.SplitN(xff, ",", 2)
			return strings.TrimSpace(parts[0])
		}
	}
	return remoteHost
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
