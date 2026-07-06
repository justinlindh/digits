package contacts

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sync"
)

// Entry represents a contact (number + display name).
type Entry struct {
	Number string `json:"number"`
	Name   string `json:"name"`
}

// Cache is a thread-safe in-memory safelist of contact numbers loaded from a
// JSON file. Only the set of numbers is retained; the display names in the
// file are for humans reading contacts.json and are not used in memory.
type Cache struct {
	mu      sync.RWMutex
	numbers map[string]struct{} // set of allowed numbers
	path    string              // file path to load from (empty = always empty cache)
}

// NewCache creates a new Cache. If path is non-empty, Load will read that file.
func NewCache(path string) *Cache {
	return &Cache{
		numbers: make(map[string]struct{}),
		path:    path,
	}
}

// IsContact returns true if the given number is in the contact list.
func (c *Cache) IsContact(number string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.numbers[number]
	return ok
}

// Count returns the number of contacts.
func (c *Cache) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.numbers)
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
	numbers := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		numbers[e.Number] = struct{}{}
	}
	c.mu.Lock()
	c.numbers = numbers
	c.mu.Unlock()
	return nil
}
