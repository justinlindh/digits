package httputil

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{name: "xff_single", remoteAddr: "172.18.0.1:34567", xff: "192.168.1.42", want: "192.168.1.42"},
		{name: "xff_with_spaces", remoteAddr: "172.18.0.1:34567", xff: "  192.168.1.42  ", want: "192.168.1.42"},
		{name: "xff_multiple_takes_first", remoteAddr: "172.18.0.1:34567", xff: "192.168.1.42, 10.0.0.1, 8.8.8.8", want: "192.168.1.42"},
		{name: "no_xff_uses_remoteaddr", remoteAddr: "192.168.1.42:34567", xff: "", want: "192.168.1.42"},
		{name: "no_xff_ipv6_brackets", remoteAddr: "[fe80::1]:34567", xff: "", want: "fe80::1"},
		{name: "no_xff_no_port_returns_raw", remoteAddr: "192.168.1.42", xff: "", want: "192.168.1.42"},
		{name: "empty_xff_falls_through", remoteAddr: "192.168.1.42:34567", xff: "", want: "192.168.1.42"},
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
			if got := ClientIP(r); got != tc.want {
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
