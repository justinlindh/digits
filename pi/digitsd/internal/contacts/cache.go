package contacts

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"sync"
)

// Entry represents a contact (number + display name).
type Entry struct {
	Number string `json:"number"`
	Name   string `json:"name"`
}

// Cache is a thread-safe in-memory contact list with optional JSON file persistence.
type Cache struct {
	mu       sync.RWMutex
	contacts map[string]string // number → name
	path     string            // file path for persistence (empty = no persistence)
}

// NewCache creates a new Cache. If path is non-empty, Save/Load will use that file.
func NewCache(path string) *Cache {
	return &Cache{
		contacts: make(map[string]string),
		path:     path,
	}
}

// setLocked replaces the contact map. Caller must hold c.mu write lock.
func (c *Cache) setLocked(entries []Entry) {
	c.contacts = make(map[string]string, len(entries))
	for _, e := range entries {
		c.contacts[e.Number] = e.Name
	}
}

// Update replaces the entire contact list and persists to disk.
func (c *Cache) Update(entries []Entry) {
	c.mu.Lock()
	c.setLocked(entries)
	c.mu.Unlock()

	if c.path != "" {
		if err := c.Save(); err != nil {
			log.Printf("contacts: save failed: %v", err)
		}
	}
}

// IsContact returns true if the given number is in the contact list.
func (c *Cache) IsContact(number string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.contacts[number]
	return ok
}

// Count returns the number of contacts.
func (c *Cache) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.contacts)
}

// Save writes the contact list to the configured file path as JSON.
func (c *Cache) Save() error {
	if c.path == "" {
		return nil
	}
	c.mu.RLock()
	entries := make([]Entry, 0, len(c.contacts))
	for num, name := range c.contacts {
		entries = append(entries, Entry{Number: num, Name: name})
	}
	c.mu.RUnlock()

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0600)
}

// Load reads the contact list from the configured file path.
// Returns nil (empty cache) if the file doesn't exist.
func (c *Cache) Load() error {
	if c.path == "" {
		return nil
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	c.mu.Lock()
	c.setLocked(entries)
	c.mu.Unlock()
	return nil
}
