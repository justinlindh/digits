package devmode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnabled_MissingFile(t *testing.T) {
	dir := t.TempDir()
	if Enabled(filepath.Join(dir, "dev-mode")) {
		t.Error("Enabled returned true for missing file")
	}
}

func TestEnable_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-mode")
	if err := Enable(path); err != nil {
		t.Fatal(err)
	}
	if !Enabled(path) {
		t.Error("Enabled returned false after Enable")
	}
}

func TestDisable_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-mode")
	if err := Enable(path); err != nil {
		t.Fatal(err)
	}
	if err := Disable(path); err != nil {
		t.Fatal(err)
	}
	if Enabled(path) {
		t.Error("Enabled returned true after Disable")
	}
}

func TestDisable_NoErrorWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-mode")
	if err := Disable(path); err != nil {
		t.Errorf("Disable on missing file returned error: %v", err)
	}
}

func TestSkipFWReflash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skip-fw-reflash")
	if SkipFWReflash(path) {
		t.Error("SkipFWReflash returned true for missing file")
	}
	if err := SetSkipFWReflash(path, true); err != nil {
		t.Fatal(err)
	}
	if !SkipFWReflash(path) {
		t.Error("SkipFWReflash returned false after SetSkipFWReflash(true)")
	}
	if err := SetSkipFWReflash(path, false); err != nil {
		t.Fatal(err)
	}
	if SkipFWReflash(path) {
		t.Error("SkipFWReflash returned true after SetSkipFWReflash(false)")
	}
}

func TestSkipAutoUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skip-auto-update")
	if SkipAutoUpdate(path) {
		t.Error("SkipAutoUpdate returned true for missing file")
	}
	if err := SetSkipAutoUpdate(path, true); err != nil {
		t.Fatal(err)
	}
	if !SkipAutoUpdate(path) {
		t.Error("SkipAutoUpdate returned false after set true")
	}
	if err := SetSkipAutoUpdate(path, false); err != nil {
		t.Fatal(err)
	}
	if SkipAutoUpdate(path) {
		t.Error("SkipAutoUpdate returned true after set false")
	}
}

func TestEnable_CreatesDirIfNeeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dev-mode")
	if err := Enable(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}
