package devmode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsSet_MissingFile(t *testing.T) {
	dir := t.TempDir()
	if IsSet(filepath.Join(dir, "dev-mode")) {
		t.Error("IsSet returned true for missing file")
	}
}

func TestSet_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-mode")
	if err := Set(path, true); err != nil {
		t.Fatal(err)
	}
	if !IsSet(path) {
		t.Error("IsSet returned false after Set(true)")
	}
}

func TestSet_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-mode")
	if err := Set(path, true); err != nil {
		t.Fatal(err)
	}
	if err := Set(path, false); err != nil {
		t.Fatal(err)
	}
	if IsSet(path) {
		t.Error("IsSet returned true after Set(false)")
	}
}

func TestSet_NoErrorWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-mode")
	if err := Set(path, false); err != nil {
		t.Errorf("Set(false) on missing file returned error: %v", err)
	}
}

func TestSet_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skip-fw-reflash")
	if IsSet(path) {
		t.Error("IsSet returned true for missing file")
	}
	if err := Set(path, true); err != nil {
		t.Fatal(err)
	}
	if !IsSet(path) {
		t.Error("IsSet returned false after Set(true)")
	}
	if err := Set(path, false); err != nil {
		t.Fatal(err)
	}
	if IsSet(path) {
		t.Error("IsSet returned true after Set(false)")
	}
}

func TestSet_CreatesDirIfNeeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dev-mode")
	if err := Set(path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}
