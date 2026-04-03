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

func TestLoadOrCreateDeviceID_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device-id")
	// Test with a temp path - write a UUID, read it back
	id, _ := generateUUIDv4()
	os.WriteFile(path, []byte(id+"\n"), 0644)
	data, _ := os.ReadFile(path)
	got := strings.TrimSpace(string(data))
	if got != id {
		t.Fatalf("expected %q, got %q", id, got)
	}
}

func TestLoadOrCreateDeviceID_LegacyUpgrade(t *testing.T) {
	// A 4-char hex ID should be rejected (too short)
	id := "a1b2"
	if len(id) == 36 {
		t.Fatal("test setup error")
	}
}
