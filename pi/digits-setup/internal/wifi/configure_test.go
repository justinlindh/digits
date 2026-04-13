package wifi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func (m *mockFS) Remove(name string) error {
	if _, ok := m.files[name]; !ok {
		return os.ErrNotExist
	}
	delete(m.files, name)
	return nil
}

func (m *mockFS) Rename(oldpath, newpath string) error {
	f, ok := m.files[oldpath]
	if !ok {
		return os.ErrNotExist
	}
	delete(m.files, oldpath)
	m.files[newpath] = f
	return nil
}

type mockMounter struct {
	rwCalls int
	roCalls int
	failRW  bool
}

func (m *mockMounter) RemountRW() error {
	m.rwCalls++
	if m.failRW {
		return fmt.Errorf("simulated remount rw failure")
	}
	return nil
}

func (m *mockMounter) RemountRO() error {
	m.roCalls++
	return nil
}

func TestConfigureSuccess(t *testing.T) {
	fs := newMockFS()
	mounter := &mockMounter{}

	req := ConfigRequest{SSID: "MyNetwork", Password: "secret123"}
	if err := ConfigureWithDeps(req, fs, mounter); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	filename := "digits-wifi-MyNetwork-" + uuidForSSID("MyNetwork")[:6] + ".nmconnection"
	backupPath := filepath.Join("/data/wifi", filename)
	opPath := filepath.Join("/etc/NetworkManager/system-connections", filename)

	backup, ok := fs.files[backupPath]
	if !ok {
		t.Fatalf("backup not written to %s", backupPath)
	}
	op, ok := fs.files[opPath]
	if !ok {
		t.Fatalf("operational not written to %s", opPath)
	}
	if string(backup.data) != string(op.data) {
		t.Errorf("backup and operational content differ")
	}
	if backup.perm != 0600 || op.perm != 0600 {
		t.Errorf("perms backup=%o op=%o, want 0600/0600", backup.perm, op.perm)
	}
	if !strings.Contains(string(backup.data), "ssid=MyNetwork") {
		t.Errorf("missing ssid, got: %s", backup.data)
	}
	if !strings.Contains(string(backup.data), "psk=secret123") {
		t.Errorf("missing psk, got: %s", backup.data)
	}
	for name := range fs.files {
		if strings.HasSuffix(name, ".tmp") {
			t.Errorf("stray temp file left behind: %s", name)
		}
	}

	flag, ok := fs.files["/data/wifi-configured"]
	if !ok {
		t.Fatal("wifi-configured flag not written")
	}
	if string(flag.data) != "1\n" {
		t.Errorf("flag = %q, want '1\\n'", string(flag.data))
	}
}

func TestConfigureMissingSSID(t *testing.T) {
	fs := newMockFS()
	mounter := &mockMounter{}

	err := ConfigureWithDeps(ConfigRequest{Password: "secret"}, fs, mounter)
	if err == nil {
		t.Fatal("expected error for missing SSID")
	}
	if !strings.Contains(err.Error(), "ssid") {
		t.Errorf("error = %q, want mention of ssid", err.Error())
	}
	if mounter.rwCalls != 0 {
		t.Error("remount must not be called on validation error")
	}
	if _, ok := fs.files["/data/wifi-configured"]; ok {
		t.Error("flag must not be set on error")
	}
}

func TestConfigureHiddenNetwork(t *testing.T) {
	fs := newMockFS()
	mounter := &mockMounter{}

	req := ConfigRequest{SSID: "SecretNet", Password: "pass123", Hidden: true}
	if err := ConfigureWithDeps(req, fs, mounter); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	filename := "digits-wifi-SecretNet-" + uuidForSSID("SecretNet")[:6] + ".nmconnection"
	opPath := filepath.Join("/etc/NetworkManager/system-connections", filename)
	f, ok := fs.files[opPath]
	if !ok {
		t.Fatalf("nmconnection not written to %s", opPath)
	}
	if !strings.Contains(string(f.data), "hidden=true") {
		t.Errorf("hidden network should have hidden=true, got: %s", f.data)
	}
}

func TestConfigureVisibleNetworkNoScanSSID(t *testing.T) {
	fs := newMockFS()
	mounter := &mockMounter{}

	req := ConfigRequest{SSID: "VisibleNet", Password: "pass123"}
	if err := ConfigureWithDeps(req, fs, mounter); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	filename := "digits-wifi-VisibleNet-" + uuidForSSID("VisibleNet")[:6] + ".nmconnection"
	opPath := filepath.Join("/etc/NetworkManager/system-connections", filename)
	f, ok := fs.files[opPath]
	if !ok {
		t.Fatalf("nmconnection not written to %s", opPath)
	}
	if strings.Contains(string(f.data), "hidden=") {
		t.Errorf("visible network should not have hidden=, got: %s", f.data)
	}
}

func TestConfigureFilenameCollisionPrevention(t *testing.T) {
	fs := newMockFS()

	req1 := ConfigRequest{SSID: "Network 1", Password: "pass1"}
	req2 := ConfigRequest{SSID: "Network-1", Password: "pass2"}

	if err := ConfigureWithDeps(req1, fs, &mockMounter{}); err != nil {
		t.Fatalf("first configure failed: %v", err)
	}
	path1 := filepath.Join("/data/wifi", "digits-wifi-Network-1-"+uuidForSSID("Network 1")[:6]+".nmconnection")
	if _, exists := fs.files[path1]; !exists {
		t.Fatalf("first network file not written to %s", path1)
	}

	if err := ConfigureWithDeps(req2, fs, &mockMounter{}); err != nil {
		t.Fatalf("second configure failed: %v", err)
	}
	path2 := filepath.Join("/data/wifi", "digits-wifi-Network-1-"+uuidForSSID("Network-1")[:6]+".nmconnection")
	if _, exists := fs.files[path2]; !exists {
		t.Fatalf("second network file not written to %s", path2)
	}

	if _, exists := fs.files[path1]; !exists {
		t.Error("first network file was overwritten; hash suffix should prevent collision")
	}
	if path1 == path2 {
		t.Error("filenames are identical; hash suffix should differentiate them")
	}
}

func TestConfigureRemountRWFailureAbortsWithoutFlag(t *testing.T) {
	fs := newMockFS()
	mounter := &mockMounter{failRW: true}

	req := ConfigRequest{SSID: "Net", Password: "pw"}
	err := ConfigureWithDeps(req, fs, mounter)
	if err == nil {
		t.Fatal("expected error from failing remount rw")
	}
	if _, ok := fs.files["/data/wifi-configured"]; ok {
		t.Error("flag must not be set when operational write is skipped")
	}
	filename := "digits-wifi-Net-" + uuidForSSID("Net")[:6] + ".nmconnection"
	backupPath := filepath.Join("/data/wifi", filename)
	if _, ok := fs.files[backupPath]; !ok {
		t.Error("backup should be written before remount is attempted")
	}
	if mounter.roCalls != 0 {
		t.Error("remount ro must not be called when remount rw failed")
	}
}

func TestConfigureLegacyCleanupBothDirs(t *testing.T) {
	fs := newMockFS()
	fs.files["/data/wifi/digits-wifi.nmconnection"] = mockFile{data: []byte("old"), perm: 0600}
	fs.files["/etc/NetworkManager/system-connections/digits-wifi.nmconnection"] = mockFile{data: []byte("old"), perm: 0600}

	req := ConfigRequest{SSID: "Net", Password: "pw"}
	if err := ConfigureWithDeps(req, fs, &mockMounter{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := fs.files["/data/wifi/digits-wifi.nmconnection"]; ok {
		t.Error("legacy backup file should be removed")
	}
	if _, ok := fs.files["/etc/NetworkManager/system-connections/digits-wifi.nmconnection"]; ok {
		t.Error("legacy operational file should be removed")
	}
}

func TestConfigureLegacyCleanupMissingIsOK(t *testing.T) {
	fs := newMockFS()
	req := ConfigRequest{SSID: "Net", Password: "pw"}
	if err := ConfigureWithDeps(req, fs, &mockMounter{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
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
