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

// privateNets is built once at init from the canonical CIDRs we treat
// as "LAN-only" for display purposes: RFC1918 plus loopback (v4 and v6),
// link-local (v4 and v6), and IPv6 ULA. Non-private ranges (public,
// CGNAT 100.64/10) are explicitly NOT included.
var privateNets []*net.IPNet

func init() {
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fe80::/10",
		"fc00::/7",
	} {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("httputil: bad private CIDR " + cidr + ": " + err.Error())
		}
		privateNets = append(privateNets, n)
	}
}

// IsPrivateAddr reports whether ip parses as an IP address inside one of
// the private ranges enumerated above. Empty or unparseable input returns
// false.
func IsPrivateAddr(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range privateNets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}
