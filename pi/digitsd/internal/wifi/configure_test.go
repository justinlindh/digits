package wifi

import (
	"errors"
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

// --- SaveToBackup tests ---

func TestSaveToBackupSuccess(t *testing.T) {
	fs := newMockFS()

	req := ConfigRequest{SSID: "MyNetwork", Password: "secret123"}
	backupPath, err := saveToBackupWithDeps(req, fs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	filename := "digits-wifi-MyNetwork-" + uuidForSSID("MyNetwork")[:6] + ".nmconnection"
	wantPath := filepath.Join("/data/wifi", filename)
	if backupPath != wantPath {
		t.Errorf("backupPath = %q, want %q", backupPath, wantPath)
	}

	backup, ok := fs.files[backupPath]
	if !ok {
		t.Fatalf("backup not written to %s", backupPath)
	}
	if backup.perm != 0600 {
		t.Errorf("perm = %o, want 0600", backup.perm)
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

	// SaveToBackup must NOT write the flag or operational file.
	if _, ok := fs.files[ConfiguredFlagPath]; ok {
		t.Error("wifi-configured flag must not be written by SaveToBackup")
	}
	opPath := filepath.Join(operationalDir, filename)
	if _, ok := fs.files[opPath]; ok {
		t.Error("operational file must not be written by SaveToBackup")
	}
}

func TestSaveToBackupMissingSSID(t *testing.T) {
	fs := newMockFS()

	_, err := saveToBackupWithDeps(ConfigRequest{Password: "secret"}, fs)
	if err == nil {
		t.Fatal("expected error for missing SSID")
	}
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if !strings.Contains(err.Error(), "ssid") {
		t.Errorf("error = %q, want mention of ssid", err.Error())
	}
	if _, ok := fs.files[ConfiguredFlagPath]; ok {
		t.Error("flag must not be set on error")
	}
}

func TestSaveToBackupHiddenNetwork(t *testing.T) {
	fs := newMockFS()

	req := ConfigRequest{SSID: "SecretNet", Password: "pass123", Hidden: true}
	backupPath, err := saveToBackupWithDeps(req, fs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	f, ok := fs.files[backupPath]
	if !ok {
		t.Fatalf("backup not written to %s", backupPath)
	}
	if !strings.Contains(string(f.data), "hidden=true") {
		t.Errorf("hidden network should have hidden=true, got: %s", f.data)
	}
}

func TestSaveToBackupVisibleNetworkNoHidden(t *testing.T) {
	fs := newMockFS()

	req := ConfigRequest{SSID: "VisibleNet", Password: "pass123"}
	backupPath, err := saveToBackupWithDeps(req, fs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	f, ok := fs.files[backupPath]
	if !ok {
		t.Fatalf("backup not written to %s", backupPath)
	}
	if strings.Contains(string(f.data), "hidden=") {
		t.Errorf("visible network should not have hidden=, got: %s", f.data)
	}
}

func TestSaveToBackupFilenameCollisionPrevention(t *testing.T) {
	fs := newMockFS()

	req1 := ConfigRequest{SSID: "Network 1", Password: "pass1"}
	req2 := ConfigRequest{SSID: "Network-1", Password: "pass2"}

	path1, err := saveToBackupWithDeps(req1, fs)
	if err != nil {
		t.Fatalf("first save failed: %v", err)
	}
	if _, exists := fs.files[path1]; !exists {
		t.Fatalf("first network file not written to %s", path1)
	}

	path2, err := saveToBackupWithDeps(req2, fs)
	if err != nil {
		t.Fatalf("second save failed: %v", err)
	}
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

// --- CommitToOperational tests ---

func TestCommitToOperationalSuccess(t *testing.T) {
	// Write a real backup file so commitWithDeps can os.ReadFile it.
	tmpDir := t.TempDir()
	backupPath := filepath.Join(tmpDir, "test.nmconnection")
	backupData := []byte("[connection]\nid=test\n")
	if err := os.WriteFile(backupPath, backupData, 0600); err != nil {
		t.Fatalf("write temp backup: %v", err)
	}

	fs := newMockFS()
	mnt := &mockMounter{}

	if err := commitWithDeps(backupPath, fs, mnt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	opPath := filepath.Join(operationalDir, "test.nmconnection")
	op, ok := fs.files[opPath]
	if !ok {
		t.Fatalf("operational file not written to %s", opPath)
	}
	if string(op.data) != string(backupData) {
		t.Errorf("operational data = %q, want %q", op.data, backupData)
	}
	if op.perm != 0600 {
		t.Errorf("perm = %o, want 0600", op.perm)
	}

	flag, ok := fs.files[ConfiguredFlagPath]
	if !ok {
		t.Fatal("wifi-configured flag not written")
	}
	if string(flag.data) != "1\n" {
		t.Errorf("flag = %q, want '1\\n'", string(flag.data))
	}

	if mnt.rwCalls != 1 {
		t.Errorf("rwCalls = %d, want 1", mnt.rwCalls)
	}
	if mnt.roCalls != 1 {
		t.Errorf("roCalls = %d, want 1", mnt.roCalls)
	}
}

func TestCommitToOperationalRemountRWFailure(t *testing.T) {
	tmpDir := t.TempDir()
	backupPath := filepath.Join(tmpDir, "test.nmconnection")
	if err := os.WriteFile(backupPath, []byte("data"), 0600); err != nil {
		t.Fatalf("write temp backup: %v", err)
	}

	fs := newMockFS()
	mnt := &mockMounter{failRW: true}

	err := commitWithDeps(backupPath, fs, mnt)
	if err == nil {
		t.Fatal("expected error from failing remount rw")
	}
	if _, ok := fs.files[ConfiguredFlagPath]; ok {
		t.Error("flag must not be set when remount rw fails")
	}
	if mnt.roCalls != 0 {
		t.Error("remount ro must not be called when remount rw failed")
	}
}

func TestCommitToOperationalMissingBackup(t *testing.T) {
	fs := newMockFS()
	mnt := &mockMounter{}

	err := commitWithDeps("/nonexistent/backup.nmconnection", fs, mnt)
	if err == nil {
		t.Fatal("expected error for missing backup file")
	}
	if !strings.Contains(err.Error(), "read backup") {
		t.Errorf("error = %q, want mention of read backup", err.Error())
	}
}

// failOpWriteFS fails WriteFile once, for the operational path only.
type failOpWriteFS struct {
	*mockFS
}

func (f *failOpWriteFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	if strings.HasPrefix(name, "/etc/NetworkManager/system-connections/") {
		return fmt.Errorf("simulated operational write failure")
	}
	return f.mockFS.WriteFile(name, data, perm)
}

func TestCommitToOperationalWriteFailureOmitsFlag(t *testing.T) {
	tmpDir := t.TempDir()
	backupPath := filepath.Join(tmpDir, "test.nmconnection")
	if err := os.WriteFile(backupPath, []byte("data"), 0600); err != nil {
		t.Fatalf("write temp backup: %v", err)
	}

	base := newMockFS()
	fs := &failOpWriteFS{mockFS: base}
	mnt := &mockMounter{}

	err := commitWithDeps(backupPath, fs, mnt)
	if err == nil {
		t.Fatal("expected error from failing operational write")
	}
	if _, ok := base.files[ConfiguredFlagPath]; ok {
		t.Error("flag must not be set when operational write fails")
	}
	if mnt.roCalls != 1 {
		t.Errorf("remount ro should run via defer, got roCalls=%d", mnt.roCalls)
	}
}

func TestCommitToOperationalLegacyCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	backupPath := filepath.Join(tmpDir, "test.nmconnection")
	if err := os.WriteFile(backupPath, []byte("data"), 0600); err != nil {
		t.Fatalf("write temp backup: %v", err)
	}

	fs := newMockFS()
	fs.files["/data/wifi/digits-wifi.nmconnection"] = mockFile{data: []byte("old"), perm: 0600}
	fs.files["/etc/NetworkManager/system-connections/digits-wifi.nmconnection"] = mockFile{data: []byte("old"), perm: 0600}
	mnt := &mockMounter{}

	if err := commitWithDeps(backupPath, fs, mnt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := fs.files["/data/wifi/digits-wifi.nmconnection"]; ok {
		t.Error("legacy backup file should be removed")
	}
	if _, ok := fs.files["/etc/NetworkManager/system-connections/digits-wifi.nmconnection"]; ok {
		t.Error("legacy operational file should be removed")
	}
}

func TestCommitToOperationalLegacyCleanupMissingIsOK(t *testing.T) {
	tmpDir := t.TempDir()
	backupPath := filepath.Join(tmpDir, "test.nmconnection")
	if err := os.WriteFile(backupPath, []byte("data"), 0600); err != nil {
		t.Fatalf("write temp backup: %v", err)
	}

	fs := newMockFS()
	if err := commitWithDeps(backupPath, fs, &mockMounter{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- iw scan tests (unchanged) ---

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
