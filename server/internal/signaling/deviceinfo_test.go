package signaling

import (
	"context"
	"testing"
)

func TestSanitizeLocalAddr(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"192.168.1.10", "192.168.1.10"},
		{"10.0.0.5", "10.0.0.5"},
		{"172.16.0.1", "172.16.0.1"},
		{"127.0.0.1", "127.0.0.1"},
		{"169.254.1.1", "169.254.1.1"},
		// Public or garbage input must be dropped so a compromised client
		// cannot push an attacker-controlled address into the owner UI.
		{"8.8.8.8", ""},
		{"203.0.113.7", ""},
		{"not-an-ip", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeLocalAddr(c.in); got != c.want {
			t.Errorf("sanitizeLocalAddr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestApplyDeviceInfo(t *testing.T) {
	conn := &Conn{}
	p := DeviceInfoParams{
		PiVersion:       "1.2.3",
		PiCommit:        "abc123",
		FirmwareVersion: "0.9.0",
		FirmwareCommit:  "def456",
		RemoteAddr:      "192.168.1.10",
		DevMode:         true,
	}
	presence := applyDeviceInfo(conn, p)

	if conn.PiVersion != p.PiVersion || conn.PiCommit != p.PiCommit ||
		conn.FirmwareVersion != p.FirmwareVersion || conn.FirmwareCommit != p.FirmwareCommit ||
		conn.RemoteAddr != p.RemoteAddr || conn.DevMode != p.DevMode {
		t.Errorf("conn fields not applied: %+v", conn)
	}
	if presence.PiVersion != p.PiVersion || presence.PiCommit != p.PiCommit ||
		presence.FirmwareVersion != p.FirmwareVersion || presence.FirmwareCommit != p.FirmwareCommit ||
		presence.RemoteAddr != p.RemoteAddr || presence.DevMode != p.DevMode {
		t.Errorf("presence fields mismatch: %+v", presence)
	}
}

func TestRouteExtensionSignaling(t *testing.T) {
	relay := NewRelay(NewHub(), nil, nil, nil)
	relay.extensions["hw-ext"] = &activeExtension{
		HardwareID: "hw-ext",
		LineNumber: "100",
		PeerNumber: "200",
	}

	// Extension device to its remote peer.
	if !relay.routeExtensionSignaling(context.Background(), "100", &Message{HardwareID: "hw-ext", To: "200"}) {
		t.Error("extension-to-peer message should be handled")
	}
	// Remote peer back to the extension device's line.
	if !relay.routeExtensionSignaling(context.Background(), "200", &Message{To: "100"}) {
		t.Error("peer-to-extension message should be handled")
	}
	// Known hardware ID but mismatched target falls through to the reverse
	// scan and, finding nothing, is not handled.
	if relay.routeExtensionSignaling(context.Background(), "100", &Message{HardwareID: "hw-ext", To: "999"}) {
		t.Error("mismatched target should not be handled")
	}
	// Unrelated sender and target.
	if relay.routeExtensionSignaling(context.Background(), "300", &Message{To: "400"}) {
		t.Error("unrelated message should not be handled")
	}
}
