package bootcount

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRead_NoFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "boot-counter")
	n, err := Read(p)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 0 {
		t.Errorf("got %d, want 0 for missing file", n)
	}
}

func TestReadWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "boot-counter")
	if err := Write(p, 3); err != nil {
		t.Fatalf("Write: %v", err)
	}
	n, err := Read(p)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 3 {
		t.Errorf("got %d, want 3", n)
	}
}

func TestClear(t *testing.T) {
	p := filepath.Join(t.TempDir(), "boot-counter")
	os.WriteFile(p, []byte("5"), 0644)
	if err := Clear(p); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	n, _ := Read(p)
	if n != 0 {
		t.Errorf("got %d, want 0 after clear", n)
	}
}

func TestSetThreshold(t *testing.T) {
	p := filepath.Join(t.TempDir(), "boot-counter")
	if err := SetThreshold(p, 3); err != nil {
		t.Fatalf("SetThreshold: %v", err)
	}
	n, _ := Read(p)
	if n != 3 {
		t.Errorf("got %d, want 3", n)
	}
}
