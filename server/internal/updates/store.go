// Package updates manages firmware and software artifacts for phone updates.
package updates

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Component selectors used by SortedReleases and RangeReleases.
const (
	ComponentPi       = "pi"
	ComponentFirmware = "firmware"
)

// Release describes a single versioned artifact (Pi binary or firmware).
type Release struct {
	Version    string    `json:"version"`
	Commit     string    `json:"commit,omitempty"`
	SHA256     string    `json:"sha256,omitempty"`
	URL        string    `json:"url"`
	Date       string    `json:"date"`
	Notes      string    `json:"notes,omitempty"`
	ReleasedAt time.Time `json:"-"`
}

// ReleaseIndex is the structure served at /api/updates/releases.
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
// component must be ComponentPi or ComponentFirmware. Returns nil for unknown
// components.
func (idx *ReleaseIndex) SortedReleases(component string) []Release {
	var m map[string]*Release
	switch component {
	case ComponentPi:
		m = idx.Pi.Releases
	case ComponentFirmware:
		m = idx.Firmware.Releases
	default:
		return nil
	}
	releases := make([]Release, 0, len(m))
	for _, r := range m {
		releases = append(releases, *r)
	}
	sort.Slice(releases, func(i, j int) bool {
		return CompareSemver(releases[i].Version, releases[j].Version) > 0
	})
	return releases
}

// RangeReleases returns releases where fromVersion < v <= toVersion,
// newest-first. An empty fromVersion means "everything up to and
// including toVersion". component must be ComponentPi or ComponentFirmware.
// Returns nil for unknown components or when the range is empty.
func (idx *ReleaseIndex) RangeReleases(component, fromVersion, toVersion string) []Release {
	all := idx.SortedReleases(component)
	if all == nil {
		return nil
	}
	var out []Release
	for _, r := range all {
		if CompareSemver(r.Version, toVersion) > 0 {
			continue
		}
		if fromVersion != "" && CompareSemver(r.Version, fromVersion) <= 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}

// CompareSemver compares two semver strings "X.Y.Z".
// Returns 1 if a > b, -1 if a < b, 0 if equal.
func CompareSemver(a, b string) int {
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
