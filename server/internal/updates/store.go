// Package updates manages firmware and software artifacts for phone updates.
package updates

import (
	"slices"
	"strconv"
	"strings"
)

// Component selectors identifying which artifact a release describes.
const (
	ComponentPi       = "pi"
	ComponentFirmware = "firmware"
	ComponentServer   = "server"
)

// Release describes a single versioned artifact (Pi binary or firmware).
type Release struct {
	Version  string `json:"version"`
	SHA256   string `json:"sha256,omitempty"`
	URL      string `json:"url"`
	Date     string `json:"date"`
	Notes    string `json:"notes,omitempty"`
	AudioURL string `json:"audio_url,omitempty"`
}

// ReleaseIndex is the structure served at /api/updates/releases.
type ReleaseIndex struct {
	Pi       ComponentIndex `json:"pi"`
	Firmware ComponentIndex `json:"firmware"`
	Server   ComponentIndex `json:"server"`
}

// ComponentIndex holds the latest version and full history for one component.
type ComponentIndex struct {
	Latest   string              `json:"latest"`
	Releases map[string]*Release `json:"releases"`
}

// Component returns the ComponentIndex for the given component selector, or
// nil for an unrecognized one. It is the single place that maps a component
// string to its field on the index; lookups and the release fetch all route
// through it so a new component is wired up in exactly one switch.
func (idx *ReleaseIndex) Component(component string) *ComponentIndex {
	switch component {
	case ComponentPi:
		return &idx.Pi
	case ComponentFirmware:
		return &idx.Firmware
	case ComponentServer:
		return &idx.Server
	default:
		return nil
	}
}

// SortedReleases returns releases for the given component sorted newest-first.
// Returns nil for unknown components.
func (idx *ReleaseIndex) SortedReleases(component string) []Release {
	ci := idx.Component(component)
	if ci == nil {
		return nil
	}
	m := ci.Releases
	releases := make([]Release, 0, len(m))
	for _, r := range m {
		releases = append(releases, *r)
	}
	slices.SortFunc(releases, func(a, b Release) int {
		return CompareSemver(b.Version, a.Version)
	})
	return releases
}

// RangeReleases returns releases where fromVersion < v <= toVersion,
// newest-first. An empty fromVersion means "everything up to and
// including toVersion".
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
	for i := range 3 {
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
