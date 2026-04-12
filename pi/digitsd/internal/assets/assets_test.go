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

// Regression: one bad file should not abort the whole extract. Before,
// the extractor returned on the first write error, skipping every later
// file AND skipping the data/* pass AND skipping the marker — so a single
// inaccessible file would roll back an entire update. Now: log-and-continue,
// write every file we can, return an error at the end, withhold the marker
// so the next boot retries.
func TestExtract_ContinuesPastFileErrors(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	markerPath := filepath.Join(dataDir, "asset-version")

	fsys := fstest.MapFS{
		"rootfs/etc/systemd/system/a.service": &fstest.MapFile{Data: []byte("a")},
		"rootfs/etc/systemd/system/b.service": &fstest.MapFile{Data: []byte("b")},
		"rootfs/etc/systemd/system/c.service": &fstest.MapFile{Data: []byte("c")},
		"data/tones/dial.wav":                 &fstest.MapFile{Data: []byte("wav")},
	}

	e := &Extractor{
		FS:         fsys,
		RootDir:    root,
		DataDir:    dataDir,
		MarkerPath: markerPath,
		Remount:    func(rw bool) error { return nil },
		RootfsWriteFile: func(data []byte, dest string, perm os.FileMode) error {
			if filepath.Base(dest) == "b.service" {
				return os.ErrPermission
			}
			return defaultWriteFile(data, dest, perm)
		},
	}

	err := e.Extract("1.0.0")
	if err == nil {
		t.Fatal("expected error for partial failure, got nil")
	}
	if !strings.Contains(err.Error(), "b.service") {
		t.Errorf("error should mention failing file: %v", err)
	}

	// Other rootfs files landed despite the one failure.
	for _, name := range []string{"a.service", "c.service"} {
		if _, err := os.Stat(filepath.Join(root, "etc/systemd/system", name)); err != nil {
			t.Errorf("expected %s on disk after partial extract, got %v", name, err)
		}
	}
	// Data files still ran.
	if _, err := os.Stat(filepath.Join(dataDir, "tones/dial.wav")); err != nil {
		t.Errorf("data file missing after partial extract: %v", err)
	}
	// Marker withheld so the next boot retries.
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Errorf("marker should not exist on partial failure, err=%v", err)
	}
}

func TestExtract_CallsReloadSystemdAfterRootfsWrites(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	markerPath := filepath.Join(dataDir, "asset-version")

	reloadCalls := 0
	e := &Extractor{
		FS: fstest.MapFS{
			"rootfs/etc/systemd/system/x.service": &fstest.MapFile{Data: []byte("unit")},
		},
		RootDir:       root,
		DataDir:       dataDir,
		MarkerPath:    markerPath,
		Remount:       func(rw bool) error { return nil },
		ReloadSystemd: func() error { reloadCalls++; return nil },
	}
	if err := e.Extract("1.0.0"); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if reloadCalls != 1 {
		t.Errorf("ReloadSystemd called %d times, want 1", reloadCalls)
	}
}

func TestExtract_SkipsReloadWhenNoRootfsFiles(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	markerPath := filepath.Join(dataDir, "asset-version")

	reloadCalls := 0
	e := &Extractor{
		FS: fstest.MapFS{
			"data/tones/dial.wav": &fstest.MapFile{Data: []byte("wav")},
		},
		RootDir:       root,
		DataDir:       dataDir,
		MarkerPath:    markerPath,
		Remount:       func(rw bool) error { return nil },
		ReloadSystemd: func() error { reloadCalls++; return nil },
	}
	if err := e.Extract("1.0.0"); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if reloadCalls != 0 {
		t.Errorf("ReloadSystemd called %d times, want 0 (no rootfs writes)", reloadCalls)
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
