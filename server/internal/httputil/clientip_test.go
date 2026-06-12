package httputil

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	cases := []struct {
		name           string
		remoteAddr     string
		xff            string
		trustedProxies int
		want           string
	}{
		// Direct connection from an untrusted network: ignore XFF entirely.
		{name: "direct_no_xff", remoteAddr: "203.0.113.7:34567", xff: "", trustedProxies: 1, want: "203.0.113.7"},
		{name: "direct_ignores_spoofed_xff", remoteAddr: "203.0.113.7:34567", xff: "8.8.8.8", trustedProxies: 1, want: "203.0.113.7"},

		// Single trusted proxy (Traefik) appends the real client to the right.
		{name: "single_proxy_real_client", remoteAddr: "172.18.0.1:34567", xff: "192.168.1.42", trustedProxies: 1, want: "192.168.1.42"},
		{name: "single_proxy_with_spaces", remoteAddr: "172.18.0.1:34567", xff: "  192.168.1.42  ", trustedProxies: 1, want: "192.168.1.42"},

		// A client that prepends its own XFF entries cannot control the key:
		// with one trusted proxy we take the rightmost entry, which is what the
		// proxy itself appended.
		{name: "spoofed_leftmost_ignored", remoteAddr: "172.18.0.1:34567", xff: "8.8.8.8, 1.1.1.1, 192.168.1.42", trustedProxies: 1, want: "192.168.1.42"},

		// Two trusted proxies: skip both appended hops, take the entry to their left.
		{name: "two_proxies_real_client", remoteAddr: "172.18.0.1:34567", xff: "8.8.8.8, 192.168.1.42, 10.0.0.5", trustedProxies: 2, want: "192.168.1.42"},

		// Fewer XFF entries than the configured hop count: fall back to rightmost.
		{name: "hop_count_exceeds_entries", remoteAddr: "172.18.0.1:34567", xff: "8.8.8.8, 192.168.1.42", trustedProxies: 5, want: "192.168.1.42"},

		// Zero trusted proxies ignores XFF entirely, even behind a proxy.
		{name: "zero_proxies_ignores_xff", remoteAddr: "172.18.0.1:34567", xff: "8.8.8.8", trustedProxies: 0, want: "172.18.0.1"},

		// No XFF behind a proxy: fall back to RemoteAddr.
		{name: "proxy_no_xff_uses_remoteaddr", remoteAddr: "192.168.1.42:34567", xff: "", trustedProxies: 1, want: "192.168.1.42"},
		{name: "no_xff_ipv6_brackets", remoteAddr: "[fe80::1]:34567", xff: "", trustedProxies: 1, want: "fe80::1"},
		{name: "no_xff_no_port_returns_raw", remoteAddr: "192.168.1.42", xff: "", trustedProxies: 1, want: "192.168.1.42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{
				RemoteAddr: tc.remoteAddr,
				Header:     make(http.Header),
			}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := ClientIP(r, tc.trustedProxies); got != tc.want {
				t.Errorf("ClientIP: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsPrivateAddr(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{ip: "10.0.0.1", want: true},
		{ip: "10.255.255.255", want: true},
		{ip: "172.16.0.1", want: true},
		{ip: "172.31.255.254", want: true},
		{ip: "172.32.0.1", want: false},
		{ip: "192.168.0.1", want: true},
		{ip: "192.168.255.255", want: true},
		{ip: "127.0.0.1", want: true},
		{ip: "127.255.255.255", want: true},
		{ip: "169.254.1.1", want: true},
		{ip: "::1", want: true},
		{ip: "fe80::1", want: true},
		{ip: "fc00::1", want: true},
		{ip: "fd00::beef", want: true},
		{ip: "8.8.8.8", want: false},
		{ip: "1.1.1.1", want: false},
		{ip: "100.64.0.1", want: false},
		{ip: "100.127.255.254", want: false},
		{ip: "2001:4860:4860::8888", want: false},
		{ip: "", want: false},
		{ip: "not-an-ip", want: false},
		{ip: "192.168.1.999", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			if got := IsPrivateAddr(tc.ip); got != tc.want {
				t.Errorf("IsPrivateAddr(%q) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}
