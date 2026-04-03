// Package updates manages firmware and software artifacts for phone updates.
package updates

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Manifest describes the latest available versions (legacy format for device check endpoint).
type Manifest struct {
	PiVersion       string `json:"pi_version"`
	PiCommit        string `json:"pi_commit"`
	PiSHA256        string `json:"pi_sha256"`
	FirmwareVersion string `json:"firmware_version"`
	FirmwareCommit  string `json:"firmware_commit"`
	FirmwareSHA256  string `json:"firmware_sha256"`
}

// Release describes a single versioned artifact (Pi binary or firmware).
type Release struct {
	Version    string    `json:"version"`
	Commit     string    `json:"commit,omitempty"`
	SHA256     string    `json:"sha256,omitempty"`
	URL        string    `json:"url"`
	Date       string    `json:"date"`
	ReleasedAt time.Time `json:"-"`
}

// ReleaseIndex is the structure served at /api/updates/releases.json.
type ReleaseIndex struct {
	Pi       ComponentIndex `json:"pi"`
	Firmware ComponentIndex `json:"firmware"`
}

// ComponentIndex holds the latest version and full history for one component.
type ComponentIndex struct {
	Latest   string              `json:"latest"`
	Releases map[string]*Release `json:"releases"`
}

// SortedReleases returns releases for the given component sorted newest-first.
// component must be "pi" or "firmware". Returns nil for unknown components.
func (idx *ReleaseIndex) SortedReleases(component string) []Release {
	var m map[string]*Release
	switch component {
	case "pi":
		m = idx.Pi.Releases
	case "firmware":
		m = idx.Firmware.Releases
	default:
		return nil
	}
	releases := make([]Release, 0, len(m))
	for _, r := range m {
		releases = append(releases, *r)
	}
	sort.Slice(releases, func(i, j int) bool {
		return compareSemver(releases[i].Version, releases[j].Version) > 0
	})
	return releases
}

// compareSemver compares two semver strings "X.Y.Z".
// Returns 1 if a > b, -1 if a < b, 0 if equal.
func compareSemver(a, b string) int {
	partsA := strings.SplitN(a, ".", 3)
	partsB := strings.SplitN(b, ".", 3)
	for i := 0; i < 3; i++ {
		var pa, pb string
		if i < len(partsA) {
			pa = partsA[i]
		}
		if i < len(partsB) {
			pb = partsB[i]
		}
		na, _ := strconv.Atoi(pa)
		nb, _ := strconv.Atoi(pb)
		if na > nb {
			return 1
		}
		if na < nb {
			return -1
		}
	}
	return 0
}

// Store serves update artifacts from a directory.
// Expected layout:
//
//	artifacts/
//	  latest.json          — version manifest (legacy)
//	  releases.json        — full release index written by CI
//	  digitsd-aarch64      — Pi binary
//	  firmware.elf         — Pico firmware
type Store struct {
	dir string
}

// NewStore creates a Store that serves artifacts from dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// ReleaseIndex reads releases.json from disk on every call. Returns nil if the
// file is missing or contains invalid JSON. No caching — hot-reload is inherent.
func (s *Store) ReleaseIndex() *ReleaseIndex {
	data, err := os.ReadFile(filepath.Join(s.dir, "releases.json"))
	if err != nil {
		return nil
	}
	var idx ReleaseIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil
	}
	return &idx
}

// Latest reads and returns the current manifest.
func (s *Store) Latest() (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "latest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// ServeManifest returns an HTTP handler for GET /api/updates/latest.
func (s *Store) ServeManifest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, err := s.Latest()
		if err != nil {
			http.Error(w, "no updates available", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(m)
	}
}

// ServeArtifact returns an HTTP handler for GET /api/updates/download/{name}.
// Only allows known artifact names.
func (s *Store) ServeArtifact() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		switch name {
		case "digitsd-aarch64", "firmware.elf":
			// allowed
		default:
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		path := filepath.Join(s.dir, name)
		http.ServeFile(w, r, path)
	}
}

// ServeReleases returns an HTTP handler for GET /api/updates/releases.
// Reads releases.json from disk on every request. Returns 404 if not present.
func (s *Store) ServeReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idx := s.ReleaseIndex()
		if idx == nil {
			http.Error(w, "no updates available", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(idx)
	}
}
