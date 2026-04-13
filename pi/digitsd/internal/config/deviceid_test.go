package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateUUIDv4(t *testing.T) {
	id, err := generateUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 36 {
		t.Fatalf("expected 36-char UUID, got %d: %q", len(id), id)
	}
	if id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("invalid UUID format: %q", id)
	}
	// Version 4
	if id[14] != '4' {
		t.Fatalf("expected version 4, got %c", id[14])
	}
	// Two calls produce different IDs
	id2, _ := generateUUIDv4()
	if id == id2 {
		t.Fatal("two UUIDs should not be equal")
	}
}

func TestLoadOrCreateDeviceID_ExistingValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device-id")
	want := "fa224572-e794-4ce5-a88d-788524adfe45"
	if err := os.WriteFile(path, []byte(want+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := loadOrCreateDeviceIDAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestLoadOrCreateDeviceID_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device-id")
	got, err := loadOrCreateDeviceIDAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 36 {
		t.Fatalf("expected 36-char UUID, got %q", got)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(persisted)) != got {
		t.Fatalf("persisted %q does not match returned %q", persisted, got)
	}
}

// A file of 36 bytes that is not actually a UUID slips past the
// length-only check and gets returned as the hardware ID. In
// particular, a post-crash ext4 zero-write artifact can leave a
// fixed-size file full of NULs on disk; TrimSpace does not strip NULs,
// so shape validation is required, not just a length check.
func TestLoadOrCreateDeviceID_CorruptedFixedLengthFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device-id")
	if err := os.WriteFile(path, make([]byte, 36), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := loadOrCreateDeviceIDAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(got, '\x00') {
		t.Fatalf("expected regenerated UUID, got NUL-containing %q", got)
	}
	if len(got) != 36 || got[8] != '-' || got[13] != '-' || got[18] != '-' || got[23] != '-' {
		t.Fatalf("expected regenerated UUID, got %q", got)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(persisted)) != got {
		t.Fatalf("persisted %q does not match returned %q", persisted, got)
	}
}

// Legacy provisioning created /data/digits/device-id as root:root while
// digitsd runs as the digits user. When the file contents are invalid
// and need regeneration, os.WriteFile cannot open the existing file for
// writing and the whole call fails. Simulate with a read-only file that
// still lives in a writable directory.
func TestLoadOrCreateDeviceID_UnwritableExistingFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file mode permissions do not apply to root")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "device-id")
	if err := os.WriteFile(path, []byte("not-a-uuid"), 0444); err != nil {
		t.Fatal(err)
	}
	got, err := loadOrCreateDeviceIDAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 36 {
		t.Fatalf("expected regenerated UUID, got %q", got)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(persisted)) != got {
		t.Fatalf("persisted %q does not match returned %q", persisted, got)
	}
}
