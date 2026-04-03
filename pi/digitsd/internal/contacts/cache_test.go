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

func TestCache_Update_And_IsContact(t *testing.T) {
	c := NewCache("")
	c.Update([]Entry{
		{Number: "5551234", Name: "Emma"},
		{Number: "5559876", Name: "Liam"},
	})

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

func TestCache_Update_Replaces(t *testing.T) {
	c := NewCache("")
	c.Update([]Entry{
		{Number: "5551234", Name: "Emma"},
		{Number: "5559876", Name: "Liam"},
	})
	c.Update([]Entry{
		{Number: "5550000", Name: "Olivia"},
	})

	if c.IsContact("5551234") {
		t.Error("old contact 5551234 should be gone after update")
	}
	if !c.IsContact("5550000") {
		t.Error("new contact 5550000 should be present")
	}
}

func TestCache_Count(t *testing.T) {
	c := NewCache("")
	if c.Count() != 0 {
		t.Errorf("expected 0, got %d", c.Count())
	}
	c.Update([]Entry{
		{Number: "5551234", Name: "Emma"},
		{Number: "5559876", Name: "Liam"},
	})
	if c.Count() != 2 {
		t.Errorf("expected 2, got %d", c.Count())
	}
}

func TestCache_PersistAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "contacts.json")

	c1 := NewCache(path)
	c1.Update([]Entry{
		{Number: "5551234", Name: "Emma"},
		{Number: "5559876", Name: "Liam"},
	})
	if err := c1.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}

	c2 := NewCache(path)
	if err := c2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if c2.Count() != 2 {
		t.Fatalf("expected 2 contacts after load, got %d", c2.Count())
	}
	if !c2.IsContact("5551234") {
		t.Error("expected 5551234 after load")
	}
	if !c2.IsContact("5559876") {
		t.Error("expected 5559876 after load")
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

func TestCache_Save_NoPath(t *testing.T) {
	c := NewCache("")
	if err := c.Save(); err != nil {
		t.Fatalf("save with no path should succeed, got: %v", err)
	}
}
