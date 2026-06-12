package signaling

import "testing"

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
