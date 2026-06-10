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
// address, meaning the request arrived through a local reverse proxy (in
// production, Traefik in the cluster). Direct connections from untrusted
// networks use RemoteAddr.
//
// trustedProxies is the number of reverse-proxy hops between this process and
// the real client (1 for a single Traefik in front of signald). Each trusted
// proxy appends the address it received the connection from to the right of
// X-Forwarded-For, so the real client is the entry just to the left of the
// trusted-proxy hops, counted from the right. Reading the leftmost entry
// instead would let a client spoof its own X-Forwarded-For value and control
// the rate-limit key, so the rightmost-minus-hops entry is the only one the
// chain actually vouches for. A trustedProxies of zero or less ignores
// X-Forwarded-For entirely and always uses RemoteAddr. If fewer XFF entries
// are present than the hop count, the rightmost entry is used: it is the one
// our immediate proxy appended and is never client-controlled.
func ClientIP(r *http.Request, trustedProxies int) string {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	if trustedProxies <= 0 || !IsPrivateAddr(remoteHost) {
		return remoteHost
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remoteHost
	}
	parts := strings.Split(xff, ",")
	// The entry the outermost trusted proxy appended is the real client when
	// trustedProxies == 1. Clamp to the rightmost entry when fewer entries are
	// present than the configured hop count.
	idx := len(parts) - trustedProxies
	if idx < 0 {
		idx = len(parts) - 1
	}
	return strings.TrimSpace(parts[idx])
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
