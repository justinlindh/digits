package contacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCache_IsContact_Empty(t *testing.T) {
	c := NewCache("")
	if c.IsContact("5551234") {
		t.Error("empty cache should return false")
	}
}

// writeContacts writes a contacts JSON file and returns its path.
func writeContacts(t *testing.T, json string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "contacts.json")
	if err := os.WriteFile(path, []byte(json), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestCache_Load_And_IsContact(t *testing.T) {
	path := writeContacts(t, `[
		{"number":"5551234","name":"Emma"},
		{"number":"5559876","name":"Liam"}
	]`)
	c := NewCache(path)
	if err := c.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if !c.IsContact("5551234") {
		t.Error("expected 5551234 to be a contact")
	}
	if !c.IsContact("5559876") {
		t.Error("expected 5559876 to be a contact")
	}
	if c.IsContact("5550000") {
		t.Error("expected 5550000 to NOT be a contact")
	}
}

func TestCache_Count(t *testing.T) {
	c := NewCache("")
	if c.Count() != 0 {
		t.Errorf("expected 0, got %d", c.Count())
	}

	path := writeContacts(t, `[
		{"number":"5551234","name":"Emma"},
		{"number":"5559876","name":"Liam"}
	]`)
	c = NewCache(path)
	if err := c.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Count() != 2 {
		t.Errorf("expected 2, got %d", c.Count())
	}
}

func TestCache_Load_NoFile(t *testing.T) {
	c := NewCache("/nonexistent/contacts.json")
	if err := c.Load(); err != nil {
		t.Fatalf("load with no file should succeed, got: %v", err)
	}
	if c.Count() != 0 {
		t.Errorf("expected 0 contacts, got %d", c.Count())
	}
}

func TestCache_Load_InvalidJSON(t *testing.T) {
	path := writeContacts(t, `{not json`)
	c := NewCache(path)
	if err := c.Load(); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if c.Count() != 0 {
		t.Errorf("failed load should leave cache empty, got %d", c.Count())
	}
}
