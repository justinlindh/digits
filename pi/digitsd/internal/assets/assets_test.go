package assets

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestExtract_WritesFilesOnFirstRun(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	markerPath := filepath.Join(dataDir, "asset-version")

	fs := fstest.MapFS{
		"rootfs/usr/local/bin/flash-pico.sh": &fstest.MapFile{Data: []byte("#!/bin/bash\necho flash")},
		"data/tones/dial.wav":                &fstest.MapFile{Data: []byte("RIFF-fake-wav")},
	}

	e := &Extractor{
		FS:         fs,
		RootDir:    root,
		DataDir:    dataDir,
		MarkerPath: markerPath,
		Remount:    func(rw bool) error { return nil },
	}

	if err := e.Extract("1.0.0"); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "usr/local/bin/flash-pico.sh"))
	if err != nil {
		t.Fatalf("read rootfs file: %v", err)
	}
	if string(got) != "#!/bin/bash\necho flash" {
		t.Errorf("rootfs file content = %q", got)
	}

	got, err = os.ReadFile(filepath.Join(dataDir, "tones/dial.wav"))
	if err != nil {
		t.Fatalf("read data file: %v", err)
	}
	if string(got) != "RIFF-fake-wav" {
		t.Errorf("data file content = %q", got)
	}

	marker, _ := os.ReadFile(markerPath)
	if string(marker) != "1.0.0" {
		t.Errorf("marker = %q, want %q", marker, "1.0.0")
	}
}

func TestExtract_SkipsWhenVersionMatches(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	markerPath := filepath.Join(dataDir, "asset-version")
	os.WriteFile(markerPath, []byte("1.0.0"), 0644)

	remountCalled := false
	e := &Extractor{
		FS:         fstest.MapFS{},
		RootDir:    root,
		DataDir:    dataDir,
		MarkerPath: markerPath,
		Remount:    func(rw bool) error { remountCalled = true; return nil },
	}

	if err := e.Extract("1.0.0"); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if remountCalled {
		t.Error("remount should not be called when version matches")
	}
}

func TestExtract_SetsPermissionsForScripts(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	markerPath := filepath.Join(dataDir, "asset-version")

	fs := fstest.MapFS{
		"rootfs/usr/local/bin/flash-pico.sh":  &fstest.MapFile{Data: []byte("#!/bin/bash")},
		"rootfs/etc/sudoers.d/digits-updater": &fstest.MapFile{Data: []byte("digits ALL=...")},
	}

	e := &Extractor{
		FS:         fs,
		RootDir:    root,
		DataDir:    dataDir,
		MarkerPath: markerPath,
		Remount:    func(rw bool) error { return nil },
	}

	if err := e.Extract("1.0.0"); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	info, _ := os.Stat(filepath.Join(root, "usr/local/bin/flash-pico.sh"))
	if info.Mode().Perm() != 0755 {
		t.Errorf("script perm = %o, want 0755", info.Mode().Perm())
	}

	info, _ = os.Stat(filepath.Join(root, "etc/sudoers.d/digits-updater"))
	if info.Mode().Perm() != 0440 {
		t.Errorf("sudoers perm = %o, want 0440", info.Mode().Perm())
	}
}
