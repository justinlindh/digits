package assets

import (
	"os"
	"path/filepath"
	"strings"
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
	if !strings.HasPrefix(string(marker), "1.0.0:") {
		t.Errorf("marker = %q, want prefix %q", marker, "1.0.0:")
	}
}

func TestExtract_SkipsWhenMarkerMatches(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	markerPath := filepath.Join(dataDir, "asset-version")

	embedded := fstest.MapFS{
		"rootfs/usr/local/bin/flash-pico.sh": &fstest.MapFile{Data: []byte("#!/bin/bash\necho v1")},
	}

	// First run: populates the marker with the current hash.
	first := &Extractor{
		FS:         embedded,
		RootDir:    root,
		DataDir:    dataDir,
		MarkerPath: markerPath,
		Remount:    func(rw bool) error { return nil },
	}
	if err := first.Extract("1.0.0"); err != nil {
		t.Fatalf("Extract (first): %v", err)
	}

	// Second run with identical embedded content: should short-circuit.
	remountCalled := false
	second := &Extractor{
		FS:         embedded,
		RootDir:    root,
		DataDir:    dataDir,
		MarkerPath: markerPath,
		Remount:    func(rw bool) error { remountCalled = true; return nil },
	}
	if err := second.Extract("1.0.0"); err != nil {
		t.Fatalf("Extract (second): %v", err)
	}
	if remountCalled {
		t.Error("remount should not be called when marker matches")
	}
}

// Regression: a rebuild under the same version string but with different
// embedded content must re-extract. Before the content-hash marker, the
// extractor skipped because the version string matched, leaving stale
// files (e.g. an old digits-mixer.service) on disk forever.
func TestExtract_ReExtractsWhenContentChangesUnderSameVersion(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	markerPath := filepath.Join(dataDir, "asset-version")
	scriptPath := filepath.Join(root, "usr/local/bin/flash-pico.sh")

	oldFS := fstest.MapFS{
		"rootfs/usr/local/bin/flash-pico.sh": &fstest.MapFile{Data: []byte("old content")},
	}
	newFS := fstest.MapFS{
		"rootfs/usr/local/bin/flash-pico.sh": &fstest.MapFile{Data: []byte("new content")},
	}

	old := &Extractor{FS: oldFS, RootDir: root, DataDir: dataDir, MarkerPath: markerPath, Remount: func(rw bool) error { return nil }}
	if err := old.Extract("1.0.0"); err != nil {
		t.Fatalf("Extract (old): %v", err)
	}
	if got, _ := os.ReadFile(scriptPath); string(got) != "old content" {
		t.Fatalf("after old extract: got %q", got)
	}

	// Same version, different embedded content. Must re-extract.
	fresh := &Extractor{FS: newFS, RootDir: root, DataDir: dataDir, MarkerPath: markerPath, Remount: func(rw bool) error { return nil }}
	if err := fresh.Extract("1.0.0"); err != nil {
		t.Fatalf("Extract (new): %v", err)
	}
	got, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read after re-extract: %v", err)
	}
	if string(got) != "new content" {
		t.Errorf("re-extract did not update file: got %q, want %q", got, "new content")
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
