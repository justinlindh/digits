package signaling

import "testing"

func TestParseCandidateHostUDP(t *testing.T) {
	// Typical host candidate as pion emits it (no "candidate:" prefix).
	in := "1 1 udp 2122260223 192.168.1.42 54321 typ host generation 0"
	got := ParseCandidate(in)
	if !got.Parsed() {
		t.Fatalf("expected parsed, got %+v", got)
	}
	if got.Type != "host" {
		t.Errorf("type: got %q want host", got.Type)
	}
	if got.Transport != "udp" {
		t.Errorf("transport: got %q want udp", got.Transport)
	}
	if got.Address != "192.168.1.42" {
		t.Errorf("address: got %q want 192.168.1.42", got.Address)
	}
	if got.Port != "54321" {
		t.Errorf("port: got %q want 54321", got.Port)
	}
	if got.RelatedAddress != "" || got.RelatedPort != "" {
		t.Errorf("host candidate should have no raddr/rport, got %q/%q", got.RelatedAddress, got.RelatedPort)
	}
}

func TestParseCandidateSrflxWithRelated(t *testing.T) {
	// Server-reflexive candidate carries raddr/rport pointing at its base.
	in := "candidate:2 1 UDP 1686052607 203.0.113.7 49152 typ srflx raddr 192.168.1.42 rport 54321"
	got := ParseCandidate(in)
	if got.Type != "srflx" {
		t.Errorf("type: got %q want srflx", got.Type)
	}
	if got.Transport != "udp" {
		t.Errorf("transport: got %q want udp (lowercased)", got.Transport)
	}
	if got.Address != "203.0.113.7" || got.Port != "49152" {
		t.Errorf("addr:port mismatch: %q:%q", got.Address, got.Port)
	}
	if got.RelatedAddress != "192.168.1.42" || got.RelatedPort != "54321" {
		t.Errorf("raddr/rport mismatch: %q/%q", got.RelatedAddress, got.RelatedPort)
	}
}

func TestParseCandidateRelayTCP(t *testing.T) {
	in := "a=candidate:3 1 TCP 1685987327 198.51.100.5 5349 typ relay raddr 0.0.0.0 rport 0 tcptype passive"
	got := ParseCandidate(in)
	if got.Type != "relay" {
		t.Errorf("type: got %q want relay", got.Type)
	}
	if got.Transport != "tcp" {
		t.Errorf("transport: got %q want tcp", got.Transport)
	}
	if got.Address != "198.51.100.5" || got.Port != "5349" {
		t.Errorf("addr:port mismatch: %q:%q", got.Address, got.Port)
	}
	if got.RelatedAddress != "0.0.0.0" {
		t.Errorf("raddr: got %q want 0.0.0.0", got.RelatedAddress)
	}
}

func TestParseCandidatePrflx(t *testing.T) {
	in := "candidate:4 1 udp 1845501695 203.0.113.99 60000 typ prflx"
	got := ParseCandidate(in)
	if got.Type != "prflx" {
		t.Errorf("type: got %q want prflx", got.Type)
	}
	if !got.Parsed() {
		t.Errorf("prflx without raddr should still parse: %+v", got)
	}
}

func TestParseCandidateIPv6(t *testing.T) {
	in := "candidate:5 1 udp 2122194687 2001:db8::1 51000 typ host"
	got := ParseCandidate(in)
	if got.Address != "2001:db8::1" {
		t.Errorf("ipv6 address: got %q", got.Address)
	}
	if got.Port != "51000" || got.Type != "host" {
		t.Errorf("ipv6 parse mismatch: %+v", got)
	}
}

func TestParseCandidateMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"end-of-candidates", "candidate:"},
		{"too few fields", "1 1 udp 2122260223 192.168.1.42 54321"},
		{"missing typ keyword", "1 1 udp 2122260223 192.168.1.42 54321 xyz host"},
		{"garbage", "not a candidate at all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCandidate(tc.in)
			if got.Parsed() {
				t.Errorf("expected unparsed for %q, got %+v", tc.in, got)
			}
		})
	}
}

func TestParseCandidateOddTrailingTokens(t *testing.T) {
	// A lone trailing extension token must not panic or mis-pair raddr/rport.
	in := "candidate:6 1 udp 1686052607 203.0.113.7 49152 typ srflx raddr 192.168.1.42 rport 54321 network-id"
	got := ParseCandidate(in)
	if got.RelatedAddress != "192.168.1.42" || got.RelatedPort != "54321" {
		t.Errorf("raddr/rport mismatch with trailing odd token: %q/%q", got.RelatedAddress, got.RelatedPort)
	}
}
