package wifi

import (
	"os"
	"strings"
	"testing"
	"time"
)

// mockFS records all file operations.
type mockFS struct {
	dirs  map[string]os.FileMode
	files map[string]mockFile
}

type mockFile struct {
	data []byte
	perm os.FileMode
}

func newMockFS() *mockFS {
	return &mockFS{
		dirs:  make(map[string]os.FileMode),
		files: make(map[string]mockFile),
	}
}

func (m *mockFS) MkdirAll(path string, perm os.FileMode) error {
	m.dirs[path] = perm
	return nil
}

func (m *mockFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	m.files[name] = mockFile{data: data, perm: perm}
	return nil
}

func (m *mockFS) ReadFile(name string) ([]byte, error) {
	f, ok := m.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return f.data, nil
}

// mockRebooter records if reboot was scheduled.
type mockRebooter struct {
	called bool
	delay  time.Duration
}

func (m *mockRebooter) ScheduleReboot(delay time.Duration) {
	m.called = true
	m.delay = delay
}

func TestConfigureSuccess(t *testing.T) {
	fs := newMockFS()
	rebooter := &mockRebooter{}

	req := ConfigRequest{
		SSID:     "MyNetwork",
		Password: "secret123",
	}

	err := ConfigureWithDeps(req, fs, rebooter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check wpa_supplicant.conf
	nm, ok := fs.files["/data/wifi/digits-wifi.nmconnection"]
	if !ok {
		t.Fatal("nmconnection not written")
	}
	if nm.perm != 0600 {
		t.Errorf("nm perm = %o, want 0600", nm.perm)
	}
	nmStr := string(nm.data)
	if !strings.Contains(nmStr, "ssid=MyNetwork") {
		t.Errorf("nm missing ssid, got: %s", nmStr)
	}
	if !strings.Contains(nmStr, "psk=secret123") {
		t.Errorf("nm missing psk, got: %s", nmStr)
	}

	// Check flag
	flag, ok := fs.files["/data/wifi-configured"]
	if !ok {
		t.Fatal("wifi-configured flag not written")
	}
	if string(flag.data) != "1\n" {
		t.Errorf("flag = %q, want '1\\n'", string(flag.data))
	}

	// Check reboot
	if !rebooter.called {
		t.Error("reboot not scheduled")
	}
	if rebooter.delay != 5*time.Second {
		t.Errorf("reboot delay = %v, want 5s", rebooter.delay)
	}
}

func TestConfigureMissingSSID(t *testing.T) {
	fs := newMockFS()
	rebooter := &mockRebooter{}

	req := ConfigRequest{
		Password: "secret",
	}

	err := ConfigureWithDeps(req, fs, rebooter)
	if err == nil {
		t.Fatal("expected error for missing SSID")
	}
	if !strings.Contains(err.Error(), "ssid") {
		t.Errorf("error = %q, want mention of ssid", err.Error())
	}
	if rebooter.called {
		t.Error("reboot should not be scheduled on error")
	}
}

func TestConfigureHiddenNetwork(t *testing.T) {
	fs := newMockFS()
	rebooter := &mockRebooter{}

	req := ConfigRequest{
		SSID:     "SecretNet",
		Password: "pass123",
		Hidden:   true,
	}

	err := ConfigureWithDeps(req, fs, rebooter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	nm := fs.files["/data/wifi/digits-wifi.nmconnection"]
	nmStr := string(nm.data)
	if !strings.Contains(nmStr, "hidden=true") {
		t.Errorf("hidden network should have hidden=true, got: %s", nmStr)
	}
}

func TestConfigureVisibleNetworkNoScanSSID(t *testing.T) {
	fs := newMockFS()
	rebooter := &mockRebooter{}

	req := ConfigRequest{
		SSID:     "VisibleNet",
		Password: "pass123",
		Hidden:   false,
	}

	err := ConfigureWithDeps(req, fs, rebooter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	nm := fs.files["/data/wifi/digits-wifi.nmconnection"]
	nmStr := string(nm.data)
	if strings.Contains(nmStr, "hidden=") {
		t.Errorf("visible network should not have hidden=, got: %s", nmStr)
	}
}

// corruptingFS writes null bytes instead of actual data, simulating filesystem corruption.
type corruptingFS struct {
	mockFS
}

func (c *corruptingFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	corrupt := make([]byte, len(data))
	c.files[name] = mockFile{data: corrupt, perm: perm}
	return nil
}

func TestConfigureCorruptWrite(t *testing.T) {
	fs := &corruptingFS{mockFS: *newMockFS()}
	rebooter := &mockRebooter{}

	req := ConfigRequest{
		SSID:     "MyNetwork",
		Password: "secret123",
	}

	err := ConfigureWithDeps(req, fs, rebooter)
	if err == nil {
		t.Fatal("expected error for corrupt write")
	}
	if !strings.Contains(err.Error(), "read-back mismatch") {
		t.Errorf("error = %q, want read-back mismatch", err.Error())
	}
	if rebooter.called {
		t.Error("reboot should not be scheduled on corrupt write")
	}
}

func TestParseIWScan(t *testing.T) {
	input := `BSS aa:bb:cc:dd:ee:ff(on wlan0)
	TSF: 123456 usec
	signal: -45.00 dBm
	SSID: HomeNetwork
BSS 11:22:33:44:55:66(on wlan0)
	signal: -72.00 dBm
	SSID: Neighbor
BSS 77:88:99:aa:bb:cc(on wlan0)
	signal: -80.00 dBm
	SSID: HomeNetwork
BSS dd:ee:ff:00:11:22(on wlan0)
	signal: -90.00 dBm
	SSID: 
`

	networks := parseIWScan([]byte(input))

	if len(networks) != 2 {
		t.Fatalf("got %d networks, want 2 (deduped, no empty)", len(networks))
	}
	if networks[0].SSID != "HomeNetwork" || networks[0].Signal != -45 {
		t.Errorf("network[0] = %+v, want HomeNetwork/-45", networks[0])
	}
	if networks[1].SSID != "Neighbor" || networks[1].Signal != -72 {
		t.Errorf("network[1] = %+v, want Neighbor/-72", networks[1])
	}
}
